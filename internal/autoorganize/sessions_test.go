package autoorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

func sessionLine(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestSessionObjectionDoesNotGuessBetweenBatches(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{h}, ObjectionEnabledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}
	log := filepath.Join(h, "sessions", "session.jsonl")
	for _, name := range []string{"first.psd", "second.psd"} {
		path := filepath.Join(h, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		c := Candidate{Project: "品牌", WorkType: "设计·UI原型", Files: []string{path}, Signals: []FileSignal{{Path: path, Agent: "codex", Session: "same", SourcePath: log, Line: 1}}}
		if _, err := Execute(context.Background(), cfg, h, p, c); err != nil {
			t.Fatal(err)
		}
	}
	claim := sourceevidence.Claim{Agent: "codex", Session: "same", SourcePath: log, Line: 10, ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	ids, _, err := queueSessionObjection(context.Background(), cfg, p, claim, nil)
	if err != nil || len(ids) != 2 {
		t.Fatal(err)
	}
	requests, err := PendingObjections(cfg)
	if err != nil || len(requests) != 0 {
		t.Fatal("ambiguous objection selected a batch", requests, err)
	}
	named := claim
	named.Action, named.Message = "objection", "撤销「品牌」的整理"
	if namedIDs, _, e := queueSessionObjection(context.Background(), cfg, p, named, nil); e != nil || len(namedIDs) != 2 {
		t.Fatal("two batches of the same project should remain ambiguous", namedIDs, e)
	}
	if err := os.MkdirAll(filepath.Dir(log), 0700); err != nil {
		t.Fatal(err)
	}
	rows := sessionLine(t, map[string]any{"type": "session_meta", "payload": map[string]any{"id": "same", "cwd": h}})
	rows = append(rows, sessionLine(t, map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "撤销这次整理"}}}})...)
	if err := os.WriteFile(log, rows, 0600); err != nil {
		t.Fatal(err)
	}
	p.Sources = []SessionSource{{Adapter: "codex", Path: filepath.Dir(log)}}
	if _, err := ScanSessions(context.Background(), cfg, p); err != nil {
		t.Fatal(err)
	}
	_, issues, err := ReadSessionEvidence(cfg, p)
	if err != nil || len(issues) != 1 || !strings.Contains(issues[0], "多次整理") {
		t.Fatal("ambiguous objection was not shown", issues, err)
	}
	if _, err := Restore(cfg, ids[0]); err != nil {
		t.Fatal(err)
	}
	_, issues, err = ReadSessionEvidence(cfg, p)
	if err != nil || len(issues) != 0 {
		t.Fatal("resolved objection remained visible", issues, err)
	}
}

func TestSessionObjectionSelectsNamedProject(t *testing.T) {
	for _, useModel := range []bool{false, true} {
		name := "本地明确指令"
		if useModel {
			name = "模型判断"
		}
		t.Run(name, func(t *testing.T) {
			h := t.TempDir()
			cfg := config.Default(h)
			if err := os.MkdirAll(cfg.Root, 0700); err != nil {
				t.Fatal(err)
			}
			p := Policy{Enabled: true, Roots: []string{h}, ObjectionEnabledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}
			log := filepath.Join(h, "sessions", "session.jsonl")
			files := []string{filepath.Join(h, "old.psd"), filepath.Join(h, "new.psd")}
			var operations []Operation
			for i, project := range []string{"旧项目", "新项目"} {
				if err := os.WriteFile(files[i], []byte(project), 0600); err != nil {
					t.Fatal(err)
				}
				candidate := Candidate{Project: project, WorkType: "设计·UI原型", Files: []string{files[i]}, Signals: []FileSignal{{Path: files[i], Agent: "codex", Session: "same", SourcePath: log, Line: 1}}}
				op, err := Execute(context.Background(), cfg, h, p, candidate)
				if err != nil || op.Status != "organized" {
					t.Fatal(op, err)
				}
				operations = append(operations, op)
			}
			message := "撤销「旧项目」的整理"
			if useModel {
				message = "旧项目整理得不对，请把它恢复原样"
			}
			line := sessionLine(t, map[string]any{"type": "response_item", "timestamp": time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": message}}}})
			parser := sourceevidence.Parser{Session: "same", CWD: h}
			missingLine := sessionLine(t, map[string]any{"type": "response_item", "timestamp": time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "撤销「不存在」的整理"}}}})
			missing, ok := parser.FeedObjection("codex", 9, missingLine, false)
			if !ok {
				t.Fatal("explicit missing-project request was not recognized")
			}
			missing.SourcePath = log
			if _, _, err := queueSessionObjection(context.Background(), cfg, p, missing, nil); err != nil {
				t.Fatal(err)
			}
			if pending, err := PendingObjections(cfg); err != nil || len(pending) != 0 {
				t.Fatal("nonmatching project caused a restore", pending, err)
			}
			claim, ok := parser.FeedObjection("codex", 10, line, useModel)
			if !ok {
				t.Fatal("named objection was not recognized")
			}
			claim.SourcePath = log
			var judgedProject string
			var judge ObjectionJudge
			if useModel {
				judge = func(_ context.Context, _ sourceevidence.Claim, op Operation) (ObjectionJudgment, error) {
					judgedProject = op.Project
					return ObjectionJudgment{Accepted: true, Provider: "本机模拟", Model: "测试模型"}, nil
				}
			}
			ids, judged, err := queueSessionObjection(context.Background(), cfg, p, claim, judge)
			if err != nil || len(ids) != 0 || judged != useModel || useModel && judgedProject != "旧项目" {
				t.Fatal("wrong project selected for objection", ids, judgedProject, err)
			}
			requests, err := PendingObjections(cfg)
			if err != nil || len(requests) != 1 || requests[0].OperationID != operations[0].ID {
				t.Fatal("wrong restore requested", requests, err)
			}
			data, _ := json.Marshal(requests[0])
			if strings.Contains(string(data), message) {
				t.Fatal("conversation text was stored")
			}
			if _, err = Restore(cfg, requests[0].OperationID); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(files[0]); err != nil || string(data) != "旧项目" {
				t.Fatal("named project was not restored", err)
			}
			if _, err := os.Stat(files[1]); !os.IsNotExist(err) {
				t.Fatal("other project was restored unexpectedly", err)
			}
			if _, err = Restore(cfg, operations[1].ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInferTradeFromProjectContextForGenericFile(t *testing.T) {
	h := t.TempDir()
	cwd := filepath.Join(h, "project")
	os.MkdirAll(cwd, 0700)
	os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module demo\n"), 0600)
	claim := sourceevidence.Claim{Path: filepath.Join(h, "notes.md"), WorkingDir: cwd}
	got := inferWorkType(claim, Policy{Roots: []string{h}}, map[string]string{})
	if got != "开发·功能开发" {
		t.Fatal("project context did not identify development", got)
	}
}

func TestGeminiNewSessionCreatesProjectAndRestores(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	desktop, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "sessions")
	if err := os.MkdirAll(filepath.Join(desktop, "Gemini品牌"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{desktop}, Sources: []SessionSource{{Adapter: "gemini", Path: sessions}}}
	if records, err := ScanSessions(context.Background(), cfg, p); err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	file := filepath.Join(desktop, "Gemini品牌", "draft.psd")
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sessions, "projecthash", "chats", "session-new.jsonl")
	if err := os.MkdirAll(filepath.Dir(log), 0700); err != nil {
		t.Fatal(err)
	}
	metadata := sessionLine(t, map[string]any{"sessionId": "gem-session", "projectHash": "hash"})
	message := sessionLine(t, map[string]any{"id": "message", "type": "gemini", "timestamp": "2026-11-09T10:00:00Z", "toolCalls": []any{map[string]any{"id": "call-1", "name": "write_file", "status": "success", "args": map[string]any{"file_path": file, "content": "design"}}}})
	data := append(append(metadata, message...), message...)
	if err := os.WriteFile(log, data, 0600); err != nil {
		t.Fatal(err)
	}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 || records[0].Agent != "gemini" || records[0].Project != "Gemini品牌" {
		t.Fatal(records, err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].WorkType != "设计·UI原型" {
		t.Fatal(groups)
	}
	o, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || o.Status != "organized" {
		t.Fatal(o, err)
	}
	if _, err = Restore(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(file); err != nil || string(contents) != "design" {
		t.Fatal("artifact not restored", err)
	}
}

func TestGeminiProjectHashGroupsScatteredOutputs(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	for _, dir := range []string{cfg.Root, filepath.Join(h, "Projects", "品牌项目"), filepath.Join(h, "Desktop"), filepath.Join(h, "Downloads"), filepath.Join(h, "sessions")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{filepath.Join(h, "Desktop", "首页.psd"), filepath.Join(h, "Downloads", "封面.psd")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := sessionLine(t, map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte(filepath.Join(h, "Projects", "品牌项目")))})
	calls := []any{}
	for i, file := range files {
		calls = append(calls, map[string]any{"id": fmt.Sprintf("call-%d", i), "name": "write_file", "status": "success", "args": map[string]any{"file_path": file}})
	}
	message := sessionLine(t, map[string]any{"id": "message", "type": "gemini", "timestamp": "2026-11-09T10:00:00Z", "toolCalls": calls})
	if err := os.WriteFile(filepath.Join(h, "sessions", "session.jsonl"), append(metadata, message...), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "gemini", Path: filepath.Join(h, "sessions")}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].Project != "品牌项目" || len(groups[0].Files) != 2 {
		t.Fatal(groups)
	}
	// A prior user choice remains authoritative after improving inference.
	p.SessionProjects = map[string]string{SessionKey(records[0]): "用户指定项目"}
	groups = Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].Project != "用户指定项目" {
		t.Fatal("explicit choice was ignored", groups)
	}
	o, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || o.Status != "organized" || o.Summary.Moved != 2 {
		t.Fatal(o, err)
	}
	if _, err = Restore(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if data, err := os.ReadFile(file); err != nil || string(data) != "design" {
			t.Fatal(file, err)
		}
	}
}

func TestGeminiRelativeFileUsesAuthorizedProjectHash(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project, desktop, sessions := filepath.Join(h, "Projects", "品牌项目"), filepath.Join(h, "Desktop"), filepath.Join(h, "sessions")
	for _, path := range []string{project, desktop, sessions} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(desktop, "首页.psd")
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte(project))}, map[string]any{"id": "message", "type": "gemini", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "toolCalls": []any{map[string]any{"id": "write", "name": "write_file", "status": "success", "args": map[string]any{"file_path": "../../Desktop/首页.psd"}}}}}
	log := filepath.Join(sessions, "session.jsonl")
	if err := os.WriteFile(log, sessionLine(t, rows[0]), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "gemini", Path: sessions}}}
	if records, err := ScanSessions(context.Background(), cfg, p); err != nil || len(records) != 0 {
		t.Fatal("metadata-only scan failed", records, err)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(sessionLine(t, rows[1])); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 || records[0].Path != file || records[0].Project != "品牌项目" {
		t.Fatalf("relative Gemini output was not discovered: %+v %v", records, err)
	}
	candidates := Candidates(cfg, p, records)
	if len(candidates) != 1 || candidates[0].Project != "品牌项目" {
		t.Fatal(candidates)
	}
	op, err := Execute(context.Background(), cfg, h, p, candidates[0])
	if err != nil || op.Status != "organized" {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "design" {
		t.Fatal("relative-path output was not restored", err)
	}
}

func TestGeminiRelativeFileWithUnknownProjectHashStaysUnresolved(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	sessions := filepath.Join(h, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte("/unknown/project"))}, map[string]any{"id": "message", "type": "gemini", "toolCalls": []any{map[string]any{"id": "write", "name": "write_file", "status": "success", "args": map[string]any{"file_path": "design.psd"}}}}}
	var data []byte
	for _, row := range rows {
		data = append(data, sessionLine(t, row)...)
	}
	if err := os.WriteFile(filepath.Join(sessions, "session.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "gemini", Path: sessions}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 0 {
		t.Fatal("unmatched project produced a file claim", records, err)
	}
	_, issues, err := ReadSessionEvidence(cfg, p)
	if err != nil || len(issues) == 0 {
		t.Fatal("unresolved file was not reported", issues, err)
	}
}
func writeCodexSession(t *testing.T, log, cwd, file, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(log), 0700); err != nil {
		t.Fatal(err)
	}
	rows := [][]byte{sessionLine(t, map[string]any{"type": "session_meta", "payload": map[string]any{"id": "session-1", "cwd": cwd}}), sessionLine(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": "*** Begin Patch\n*** Add File: " + file + "\n+" + body + "\n*** End Patch\n"}}), sessionLine(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA " + file + "\n"}})}
	var data []byte
	for _, row := range rows {
		data = append(data, row...)
	}
	if err := os.WriteFile(log, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexParserUpgradeBackfillsExistingSession(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	sessions := filepath.Join(h, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	oldFile, newFile := filepath.Join(h, "old.txt"), filepath.Join(h, "new.txt")
	patch := "*** Begin Patch\n*** Add File: " + newFile + "\n+new\n*** End Patch\n"
	literal, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	program := "text(await tools.apply_patch(" + string(literal) + "));"
	rows := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "session-one", "cwd": h}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "old", "input": "*** Begin Patch\n*** Add File: " + oldFile + "\n+old\n*** End Patch\n"}},
		map[string]any{"type": "response_item", "timestamp": "2026-11-10T10:00:00Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "old", "output": "Success. Updated the following files:\nA old.txt\n"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "new", "input": program}},
		map[string]any{"type": "response_item", "timestamp": "2026-11-10T10:01:00Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "new", "output": []any{map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.5 seconds\nOutput:\n"}, map[string]any{"type": "input_text", "text": "{}"}}}},
	}
	var data []byte
	for _, row := range rows {
		data = append(data, sessionLine(t, row)...)
	}
	if err = os.WriteFile(filepath.Join(sessions, "session.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{ObserveSessions: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	if _, err = ScanSessions(context.Background(), cfg, p); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir(cfg), "会话进度.json")
	data, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state sessionState
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Grants) != 1 || len(state.Grants[0].Documents) != 1 || state.Grants[0].Documents[0].Cursor.Offset == 0 {
		t.Fatal("initial scan did not save a checkpoint")
	}
	delete(state.Records, newFile)
	state.CodexParserVersion = 1
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 2 {
		t.Fatal("historical exec patch was not backfilled", records, err)
	}
	data, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.CodexParserVersion != 2 || state.Records[oldFile].Path != oldFile || state.Records[newFile].Path != newFile {
		t.Fatal("upgrade lost existing evidence")
	}
}

func TestOutsideWorkingSubdirectoryNeedsProjectJudgment(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "sessions")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(desktop, "首页.psd")
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(h, "Projects", "品牌项目", "packages", "web")
	writeCodexSession(t, filepath.Join(sessions, "new.jsonl"), cwd, file, "design")
	p := Policy{Enabled: true, Roots: []string{desktop}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 || records[0].Project != "" {
		t.Fatalf("working subdirectory was mistaken for a project: %+v %v", records, err)
	}
	if candidates := Candidates(cfg, p, records); len(candidates) != 0 {
		t.Fatalf("unconfirmed project would be organized automatically: %+v", candidates)
	}
	if tasks := ModelTasks(cfg, p, records); len(tasks) != 1 || tasks[0].Count != 1 {
		t.Fatalf("file group was not offered for project judgment: %+v", tasks)
	}
}

func TestExistingRootProjectSubdirectoryKeepsProjectIdentity(t *testing.T) {
	for _, insideProject := range []bool{false, true} {
		t.Run(fmt.Sprintf("insideProject=%v", insideProject), func(t *testing.T) {
			h := t.TempDir()
			cfg := config.Default(h)
			project, desktop := filepath.Join(cfg.Root, "品牌项目"), filepath.Join(h, "Desktop")
			cwd := filepath.Join(project, "packages", "web")
			for _, dir := range []string{cwd, desktop} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(desktop, "首页.psd")
			if insideProject {
				file = filepath.Join(project, "首页.psd")
			}
			if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
				t.Fatal(err)
			}
			sessions := filepath.Join(h, "sessions")
			writeCodexSession(t, filepath.Join(sessions, "new.jsonl"), cwd, file, "design")
			p := Policy{Enabled: true, Roots: []string{project, desktop}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
			records, err := ScanSessions(context.Background(), cfg, p)
			if err != nil || len(records) != 1 || records[0].Project != "品牌项目" {
				t.Fatalf("existing project subdirectory misidentified: %+v %v", records, err)
			}
			candidates := Candidates(cfg, p, records)
			if insideProject {
				if len(candidates) != 0 {
					t.Fatalf("file already in project would be moved: %+v", candidates)
				}
				return
			}
			if len(candidates) != 1 || candidates[0].Project != "品牌项目" {
				t.Fatalf("scattered file was not assigned to existing project: %+v", candidates)
			}
			op, err := Execute(context.Background(), cfg, h, p, candidates[0])
			if err != nil || op.Status != "organized" || op.Target != project {
				t.Fatal("wrong destination", op, err)
			}
			if _, err = Restore(cfg, op.ID); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "design" {
				t.Fatal("file not restored", err)
			}
		})
	}
}

func TestRestoredSessionFileIsNotCollectedAgainWithNewFiles(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop, cwd, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "Projects", "品牌"), filepath.Join(h, "sessions")
	for _, path := range []string{desktop, cwd} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	oldFile, newFile := filepath.Join(desktop, "old.psd"), filepath.Join(desktop, "new.psd")
	if err := os.WriteFile(oldFile, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sessions, "session.jsonl")
	writeCodexSession(t, log, cwd, oldFile, "old")
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	first := Candidates(cfg, p, records)
	if len(first) != 1 {
		t.Fatal(first)
	}
	op, err := Execute(context.Background(), cfg, h, p, first[0])
	if err != nil || op.Status != "organized" {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	appendWrite := func(id, file, content string) {
		t.Helper()
		if e := os.WriteFile(file, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		rows := []any{map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": id, "input": "*** Begin Patch\n*** Add File: " + file + "\n+" + content + "\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": "Success. Updated the following files:\nA " + file + "\n"}}}
		f, e := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			if _, e = f.Write(sessionLine(t, row)); e != nil {
				t.Fatal(e)
			}
		}
		if e = f.Close(); e != nil {
			t.Fatal(e)
		}
	}
	appendWrite("write-2", newFile, "new")
	records, err = ScanSessions(context.Background(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0] != newFile {
		t.Fatalf("restored file was collected again with new files: %+v", groups)
	}
	appendWrite("write-3", oldFile, "updated")
	records, err = ScanSessions(context.Background(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	groups = Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("new file operation was not eligible: %+v", groups)
	}
}

func TestNewSessionWriteOverridesOlderRestoredMark(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop, cwd, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "Projects", "新项目"), filepath.Join(h, "sessions")
	for _, path := range []string{desktop, cwd, sessions} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(desktop, "design.psd")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{file}, Agent: "codex", Project: "旧项目", WorkType: "设计·UI原型", Now: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	marks, err := activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	first := Candidates(cfg, p, marks)
	if len(first) != 1 {
		t.Fatal(first)
	}
	op, err := Execute(context.Background(), cfg, h, p, first[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sessions, "new.jsonl")
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "new-session", "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "new-write", "input": "*** Begin Patch\n*** Update File: " + file + "\n@@\n-old\n+new\n*** End Patch\n"}}, map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "new-write", "output": "Success. Updated the following files:\nM " + file + "\n"}}}
	var data []byte
	for _, row := range rows {
		data = append(data, sessionLine(t, row)...)
	}
	if err = os.WriteFile(log, data, 0600); err != nil {
		t.Fatal(err)
	}
	session, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(session) != 1 {
		t.Fatal(session, err)
	}
	groups := Candidates(cfg, p, append(session, marks...))
	if len(groups) != 1 || groups[0].Project != "新项目" || groups[0].Signals[0].RecordSHA == "" {
		t.Fatalf("new session write was hidden by old mark: %+v", groups)
	}
	unassigned := append([]activity.Record(nil), session...)
	unassigned[0].Project = ""
	if tasks := ModelTasks(cfg, p, append(unassigned, marks...)); len(tasks) != 1 || tasks[0].Kind != "session" {
		t.Fatalf("new session was hidden from model judgment: %+v", tasks)
	}
	newerMark := marks[0]
	newerMark.Project = "更新的标记项目"
	newerMark.Time = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	if recent := Candidates(cfg, p, append(session, newerMark)); len(recent) != 1 || recent[0].Project != newerMark.Project {
		t.Fatalf("newer mark did not take precedence: %+v", recent)
	}
	updated, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || updated.Status != "organized" {
		t.Fatal(updated, err)
	}
	if _, err = Restore(cfg, updated.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSessionWriteWinsAcrossSourcesAndScans(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "sessions")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(desktop, "design.psd")
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(log, id, project string, at time.Time, create bool) {
		t.Helper()
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		var rows []any
		if create {
			rows = append(rows, map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": filepath.Join(h, "Projects", project)}})
		}
		rows = append(rows, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": id + at.String(), "input": "*** Begin Patch\n*** Update File: " + file + "\n@@\n-old\n+design\n*** End Patch\n"}}, map[string]any{"type": "response_item", "timestamp": at.UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call_output", "call_id": id + at.String(), "output": "Success. Updated the following files:\nM " + file + "\n"}})
		for _, row := range rows {
			if _, err = f.Write(sessionLine(t, row)); err != nil {
				t.Fatal(err)
			}
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Minute)
	newLog, oldLog := filepath.Join(sessions, "a-new.jsonl"), filepath.Join(sessions, "z-old.jsonl")
	write(newLog, "new", "新项目", base.Add(20*time.Second), true)
	write(oldLog, "old", "旧项目", base, true)
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	check := func(want string) {
		t.Helper()
		records, err := ScanSessions(context.Background(), cfg, p)
		if err != nil || len(records) != 1 || records[0].Project != want {
			t.Fatalf("wrong latest session: %+v %v", records, err)
		}
		groups := Candidates(cfg, p, records)
		if len(groups) != 1 || groups[0].Project != want {
			t.Fatalf("wrong project candidate: %+v", groups)
		}
	}
	check("新项目")
	write(oldLog, "old", "旧项目", base.Add(10*time.Second), false)
	check("新项目")
	write(oldLog, "old", "旧项目", base.Add(30*time.Second), false)
	check("旧项目")
}

func TestCodexShellPatchNewSessionOrganizesAndRestores(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop, projects, sessions := filepath.Join(h, "Desktop"), filepath.Join(h, "Projects"), filepath.Join(h, "sessions")
	cwd := filepath.Join(projects, "终端项目")
	for _, dir := range []string{cfg.Root, desktop, cwd, sessions} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Enabled: true, Roots: []string{desktop, projects}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	if records, err := ScanSessions(context.Background(), cfg, p); err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	file := filepath.Join(desktop, "设计稿.psd")
	if err := os.WriteFile(file, []byte("design\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: " + file + "\n+design\n*** End Patch\nPATCH\n"
	args, _ := json.Marshal(map[string]any{"cmd": command, "workdir": cwd})
	log := filepath.Join(sessions, "new.jsonl")
	rows := sessionLine(t, map[string]any{"type": "session_meta", "payload": map[string]any{"id": "shell-session", "cwd": h}})
	rows = append(rows, sessionLine(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "shell-1", "arguments": string(args)}})...)
	if err := os.WriteFile(log, rows, 0600); err != nil {
		t.Fatal(err)
	}
	if records, err := ScanSessions(context.Background(), cfg, p); err != nil || len(records) != 0 {
		t.Fatal("unfinished command attributed", records, err)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"type": "response_item", "timestamp": "2026-11-10T10:00:00Z", "payload": map[string]any{"type": "function_call_output", "call_id": "shell-1", "output": "Process exited with code 0\nFinal output:\nSuccess. Updated the following files:\nA " + file + "\n"}}
	unknownArgs, _ := json.Marshal(map[string]any{"cmd": "cat notes.txt"})
	unknown := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "other", "arguments": string(unknownArgs)}}
	if _, err = f.Write(append(sessionLine(t, result), sessionLine(t, unknown)...)); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 || records[0].Agent != "codex" || records[0].Project != "终端项目" {
		t.Fatal(records, err)
	}
	_, issues, err := ReadSessionEvidence(cfg, p)
	if err != nil || len(issues) != 1 || !strings.Contains(issues[0], "未映射为文件操作") {
		t.Fatal("coverage gap missing", issues, err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].WorkType != "设计·UI原型" {
		t.Fatal(groups)
	}
	op, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || op.Status != "organized" {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "design\n" {
		t.Fatal("artifact not restored", err)
	}
}

func TestSessionEvidenceCreatesProjectAndRestores(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	desktop := filepath.Join(h, "Desktop")
	projects := filepath.Join(h, "Projects")
	sessions := filepath.Join(h, "sessions")
	cwd := filepath.Join(projects, "品牌项目")
	for _, p := range []string{desktop, cwd, sessions} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(desktop, "设计稿.psd")
	if err := os.WriteFile(file, []byte("design\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeCodexSession(t, filepath.Join(sessions, "2026", "first.jsonl"), cwd, file, "design")
	p := Policy{Enabled: true, Roots: []string{desktop, projects}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].Project != "品牌项目" || groups[0].WorkType != "设计·UI原型" {
		t.Fatal(groups)
	}
	o, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || o.Status != "organized" {
		t.Fatal(o, err)
	}
	if _, err = Restore(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "design\n" {
		t.Fatal("artifact was not restored", err)
	}
	second := filepath.Join(desktop, "另一张.psd")
	os.WriteFile(second, []byte("other\n"), 0600)
	writeCodexSession(t, filepath.Join(sessions, "2026", "new.jsonl"), filepath.Join(projects, "第二项目"), second, "other")
	records, err = ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 2 {
		t.Fatal("new session not discovered", records, err)
	}
}

func TestSessionSwitchesToRegisteredProjectAfterEarlierFileMoved(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	oldProject := filepath.Join(h, "Projects", "旧项目")
	newProject := filepath.Join(h, "Projects", "新项目")
	desktop := filepath.Join(h, "Desktop")
	sessions := filepath.Join(h, "sessions")
	for _, dir := range []string{cfg.Root, oldProject, filepath.Join(newProject, ".git"), desktop, sessions, filepath.Join(h, "archive")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "new", Name: "新项目", Path: newProject}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	oldFile, newFile := filepath.Join(desktop, "old.psd"), filepath.Join(desktop, "new.psd")
	for _, file := range []string{oldFile, newFile} {
		if err := os.WriteFile(file, []byte("design\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(sessions, "session.jsonl")
	writeCodexSession(t, log, oldProject, oldFile, "design")
	p := Policy{Enabled: true, Roots: []string{h}, Sources: []SessionSource{{Adapter: "codex", Path: sessions}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	p.SessionProjects = map[string]string{SessionKey(records[0]): "用户选择的旧项目"}
	if err = os.Rename(oldFile, filepath.Join(h, "archive", "old.psd")); err != nil {
		t.Fatal(err)
	}
	rows := sessionLine(t, map[string]any{"type": "turn_context", "payload": map[string]any{"cwd": newProject, "turn_id": "new-turn"}})
	rows = append(rows, sessionLine(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-2", "input": "*** Begin Patch\n*** Add File: " + newFile + "\n+design\n*** End Patch\n"}})...)
	rows = append(rows, sessionLine(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-2", "output": "Success. Updated the following files:\nA " + newFile + "\n"}})...)
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	records, err = ScanSessions(context.Background(), cfg, p)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].ProjectKey != "place:new" || groups[0].TargetPath != newProject || len(groups[0].Files) != 1 || groups[0].Files[0] != newFile {
		t.Fatalf("new work followed the old session assignment: %+v", groups)
	}
	op, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || op.Target != newProject || op.Summary == nil || op.Summary.Moved != 1 {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(newFile); err != nil || string(data) != "design\n" {
		t.Fatal("new project file not restored", err)
	}
}

func TestCodexAndClaudeSessionsContributeToSameProject(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	projects := filepath.Join(h, "Projects")
	desktop := filepath.Join(h, "Desktop")
	cwd := filepath.Join(projects, "共同项目")
	codex := filepath.Join(h, "codex-sessions")
	claude := filepath.Join(h, "claude-sessions")
	for _, dir := range []string{cwd, desktop, codex, claude} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := filepath.Join(desktop, "first.psd")
	b := filepath.Join(desktop, "second.psd")
	os.WriteFile(a, []byte("first\n"), 0600)
	os.WriteFile(b, []byte("second"), 0600)
	writeCodexSession(t, filepath.Join(codex, "first.jsonl"), cwd, a, "first")
	items := [][]byte{sessionLine(t, map[string]any{"type": "assistant", "sessionId": "claude-1", "cwd": cwd, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "write-1", "name": "Write", "input": map[string]any{"file_path": b, "content": "second"}}}}}), sessionLine(t, map[string]any{"type": "user", "sessionId": "claude-1", "cwd": cwd, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "write-1", "is_error": false}}}})}
	if err := os.WriteFile(filepath.Join(claude, "second.jsonl"), append(items[0], items[1]...), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Enabled: true, Roots: []string{projects, desktop}, Sources: []SessionSource{{Adapter: "codex", Path: codex}, {Adapter: "claude", Path: claude}}}
	records, err := ScanSessions(context.Background(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || groups[0].Project != "共同项目" || len(groups[0].Files) != 2 || groups[0].WorkType != "设计·UI原型" {
		t.Fatal(groups)
	}
	seen := map[string]bool{}
	for _, s := range groups[0].Signals {
		seen[s.Agent] = true
		if s.Session == "" || s.SourcePath == "" || s.Line == 0 {
			t.Fatal("session provenance missing", s)
		}
	}
	if !seen["codex"] || !seen["claude"] {
		t.Fatal("both agents not attributed", seen)
	}
}
