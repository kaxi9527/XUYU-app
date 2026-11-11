package projectmemory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

func sourceMarkLine(t *testing.T, path, agent, content string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "agent": agent, "sha256": sourceevidence.Hash([]byte(content)), "task": "task-one", "note": "private text must not be copied"})
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}
func grantSource(t *testing.T, s *Store, scope Scope, path string, enabled bool, operation string) {
	t.Helper()
	v := readMemory(t, s)
	_, err := s.SetSource(context.Background(), v.State.Revision, operation, scope.Digest(), "workshelf", path, enabled, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}
func scanSource(t *testing.T, s *Store, scope Scope, operation string) Result {
	t.Helper()
	v := readMemory(t, s)
	r, err := s.Observe(context.Background(), v.State.Revision, operation, scope.Digest())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSourceHistoryRewriteResetsPendingSessionBeyondTailAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := `{"type":"session_meta","payload":{"id":"old-session","cwd":"/project"}}` + "\n" + strings.Repeat("{}\n", 2000)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	g := SourceGrant{ID: sourceID("codex", path), Adapter: "codex", Path: path}
	r, _, err := readSource(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cursor.Parser.Session != "old-session" {
		t.Fatal(r.Cursor.Parser)
	}
	g.Cursor = r.Cursor
	// Same length, same inode and unchanged tail: only the prefix fingerprint detects this edit.
	data = strings.Replace(data, "old-session", "new-session", 1)
	if err = os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	r, _, err = readSource(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reset || r.Cursor.Parser.Session != "new-session" {
		t.Fatal("rewritten history reused stale session", r)
	}
}

func TestSourceOversizedLineDoesNotBlockFollowingRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	data := append([]byte(strings.Repeat("x", (4<<20)+100)+"\n"), sourceMarkLine(t, "/project/result.txt", "codex", "result")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	g := SourceGrant{ID: sourceID("workshelf", path), Adapter: "workshelf", Path: path}
	r, claims, err := readSource(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 || !r.More || !r.Cursor.Skipping {
		t.Fatal("oversized line not deferred", r, len(claims))
	}
	g.Cursor = r.Cursor
	r, claims, err = readSource(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Line != 2 || r.Cursor.Skipping {
		t.Fatal("following record lost", r, claims)
	}
}

func TestGeminiRelativeFileRecordedInProjectMemory(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	file := filepath.Join(scope.Roots[0], "design.psd")
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "session.jsonl")
	rows := []any{map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte(scope.Roots[0]))}, map[string]any{"id": "message", "type": "gemini", "toolCalls": []any{map[string]any{"id": "write", "name": "write_file", "status": "success", "args": map[string]any{"file_path": "design.psd"}}}}}
	var data []byte
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, append(b, '\n')...)
	}
	if err := os.WriteFile(log, data, 0600); err != nil {
		t.Fatal(err)
	}
	v := readMemory(t, s)
	if _, err := s.SetSource(context.Background(), v.State.Revision, "grant-gemini", scope.Digest(), "gemini", log, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "scan-gemini")
	state := readMemory(t, s).State
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil || len(state.Origins) != 1 || state.Origins[0].Claim.Path != resolved || state.Origins[0].Claim.Agent != "gemini" {
		t.Fatalf("relative file origin missing: %+v", state.Origins)
	}
}

func TestSourcesGrantAppendRestartRevokeAndSnapshotBinding(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	file := filepath.Join(scope.Roots[0], "result.txt")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "records.jsonl")
	if err := os.WriteFile(log, sourceMarkLine(t, file, "codex", "first"), 0600); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "before-grant")
	if len(readMemory(t, s).State.Origins) != 0 {
		t.Fatal("ungranted source read")
	}
	grantSource(t, s, scope, log, true, "grant")
	scanSource(t, s, scope, "first-source")
	v := readMemory(t, s)
	if len(v.State.Origins) != 1 || v.State.Origins[0].Binding != "snapshot_match" || v.State.Origins[0].FileID == "" {
		t.Fatal(v.State.Origins)
	}
	first := v.State.Origins[0]
	scanSource(t, s, scope, "same-source")
	if len(readMemory(t, s).State.Origins) != 1 {
		t.Fatal("source duplicated")
	}
	// Reopen the store: the cursor and project evidence survive model/app state.
	reopened, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	line := sourceMarkLine(t, file, "claude", "different")
	if _, err = f.Write(line[:len(line)-1]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	scanSource(t, s, scope, "partial")
	if len(readMemory(t, s).State.Origins) != 1 {
		t.Fatal("partial JSONL line imported")
	}
	f, _ = os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	_, err = f.Write([]byte("\n"))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "append")
	v = readMemory(t, s)
	if len(v.State.Origins) != 2 || v.State.Origins[1].Binding != "snapshot_differs" || v.State.Origins[0].ID != first.ID {
		t.Fatal(v.State.Origins)
	}
	bytes, err := os.ReadFile(filepath.Join(s.directory, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "private text must not be copied") {
		t.Fatal("raw record note copied")
	}
	grantSource(t, s, scope, log, false, "revoke")
	f, _ = os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	_, err = f.Write(sourceMarkLine(t, file, "unknown", "first"))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "revoked")
	if len(readMemory(t, s).State.Origins) != 2 {
		t.Fatal("read continued after revoke")
	}
	content, err := os.ReadFile(file)
	if err != nil || string(content) != "first" {
		t.Fatal("source processing changed artifact")
	}
}

func TestSourceRotationScopeSymlinkAndMalformedLines(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	file := filepath.Join(scope.Roots[0], "a.txt")
	os.WriteFile(file, []byte("a"), 0600)
	log := filepath.Join(t.TempDir(), "marks.jsonl")
	data := append([]byte("broken json\n"), sourceMarkLine(t, filepath.Join(t.TempDir(), "outside.txt"), "codex", "outside")...)
	data = append(data, sourceMarkLine(t, file, "codex", "a")...)
	os.WriteFile(log, data, 0600)
	grantSource(t, s, scope, log, true, "grant")
	scanSource(t, s, scope, "scan")
	v := readMemory(t, s)
	if len(v.State.Origins) != 1 || v.State.Sources[0].Skipped != 1 {
		t.Fatal(v.State.Sources, v.State.Origins)
	}
	if err := os.Rename(log, log+".old"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(log, sourceMarkLine(t, file, "claude", "a"), 0600)
	scanSource(t, s, scope, "rotation")
	v = readMemory(t, s)
	if len(v.State.Origins) != 2 {
		t.Fatal("rotation lost new or old evidence", v.State.Origins)
	}
	os.Remove(log)
	os.Symlink(log+".old", log)
	scanSource(t, s, scope, "link")
	v = readMemory(t, s)
	if len(v.State.Origins) != 2 || v.State.Sources[0].Problem == "" {
		t.Fatal("source symlink followed")
	}
}
