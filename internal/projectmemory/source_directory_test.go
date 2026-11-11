package projectmemory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func grantSourceDirectory(t *testing.T, s *Store, scope Scope, path string, enabled bool, operation string) {
	t.Helper()
	v := readMemory(t, s)
	if _, err := s.SetSourceLocation(context.Background(), v.State.Revision, operation, scope.Digest(), "workshelf", path, true, enabled, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestSourceDirectoryDiscoveryBatchRestartAndRevoke(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	file := filepath.Join(scope.Roots[0], "result.txt")
	if err := os.WriteFile(file, []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	nested := filepath.Join(dir, "2026", "11", "09")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(nested, fmt.Sprintf("%02d.jsonl", i)), sourceMarkLine(t, file, "codex", "result"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	grantSourceDirectory(t, s, scope, dir, true, "directory-on")
	scanSource(t, s, scope, "directory-first")
	v := readMemory(t, s)
	if len(v.State.Origins) != 32 || len(v.State.Sources[0].Documents) != 40 || !v.State.Sources[0].More {
		t.Fatal("first batch", len(v.State.Origins), v.State.Sources[0].More)
	}
	var err error
	s, err = NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "directory-resume")
	v = readMemory(t, s)
	if len(v.State.Origins) != 40 || v.State.Sources[0].More {
		t.Fatal("resume", len(v.State.Origins), v.State.Sources[0].More)
	}
	// A separate explicit file grant must not duplicate the same source record.
	grantSource(t, s, scope, filepath.Join(nested, "00.jsonl"), true, "overlap-file")
	scanSource(t, s, scope, "directory-overlap")
	if len(readMemory(t, s).State.Origins) != 40 {
		t.Fatal("overlapping grant duplicated provenance")
	}
	newDir := filepath.Join(dir, "2026", "11", "10")
	if err = os.MkdirAll(newDir, 0700); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(newDir, "new-session.jsonl")
	if err = os.WriteFile(newFile, sourceMarkLine(t, file, "claude", "result"), 0600); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "new-session")
	v = readMemory(t, s)
	if len(v.State.Origins) != 41 || v.State.Origins[40].SourcePath != newFile || v.State.Origins[40].Binding != "snapshot_match" {
		t.Fatal("new session not linked", len(v.State.Origins))
	}
	grantSourceDirectory(t, s, scope, dir, false, "directory-off")
	if err = os.WriteFile(filepath.Join(newDir, "later.jsonl"), sourceMarkLine(t, file, "claude", "result"), 0600); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "after-directory-off")
	if len(readMemory(t, s).State.Origins) != 41 {
		t.Fatal("revoked directory still read")
	}
	if bytes, err := os.ReadFile(file); err != nil || string(bytes) != "result" {
		t.Fatal("artifact changed", err)
	}
}

func TestSourceDirectoryReadsSubagentsAndRejectsLinks(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	dir := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(scope.Roots[0], "result.txt")
	os.WriteFile(file, []byte("ok"), 0600)
	os.WriteFile(filepath.Join(outside, "outside.jsonl"), sourceMarkLine(t, file, "outside", "ok"), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"subagents", "archived_sessions", "observer-sessions"} {
		os.Mkdir(filepath.Join(dir, name), 0700)
		os.WriteFile(filepath.Join(dir, name, "internal.jsonl"), sourceMarkLine(t, file, name, "ok"), 0600)
	}
	os.WriteFile(filepath.Join(dir, "selected.jsonl"), sourceMarkLine(t, file, "codex", "ok"), 0600)
	grantSourceDirectory(t, s, scope, dir, true, "directory-on")
	scanSource(t, s, scope, "directory-filter")
	v := readMemory(t, s)
	if len(v.State.Origins) != 4 || len(v.State.Sources[0].Documents) != 4 || !strings.Contains(v.State.Sources[0].Problem, "未纳入") {
		t.Fatal("source exclusions not visible", v.State.Sources)
	}
	if err := os.Remove(filepath.Join(dir, "selected.jsonl")); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "directory-missing")
	if len(readMemory(t, s).State.Origins) != 4 {
		t.Fatal("missing log erased provenance")
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir + "-old")
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	scanSource(t, s, scope, "directory-replaced-link")
	v = readMemory(t, s)
	if len(v.State.Origins) != 4 || !strings.Contains(v.State.Sources[0].Problem, "链接") {
		t.Fatal("followed replacement link")
	}
}
