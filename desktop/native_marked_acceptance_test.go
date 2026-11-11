package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestNativeMarkedObjectionAcceptance(t *testing.T) {
	t.Setenv("WORKSHELF_DISABLE_NOTIFICATIONS", "1")
	fixture, binary, cli := os.Getenv("WORKSHELF_NATIVE_AGENT_FIXTURE"), os.Getenv("WORKSHELF_NATIVE_AGENT_BINARY"), os.Getenv("WORKSHELF_NATIVE_AGENT_CLI")
	mixedSource := os.Getenv("WORKSHELF_NATIVE_MIXED_SOURCE") == "1"
	staleSession := os.Getenv("WORKSHELF_NATIVE_STALE_SESSION") == "1"
	if fixture == "" {
		t.Skip("isolated native acceptance only")
	}
	if !filepath.IsAbs(fixture) || !filepath.IsAbs(binary) || !filepath.IsAbs(cli) {
		t.Fatal("isolated fixture and executables required")
	}
	if err := os.MkdirAll(filepath.Join(fixture, "Desktop"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(fixture, "config.json")
	t.Setenv("WORKSHELF_CONFIG", configPath)
	cfg := config.Default(fixture)
	cfg.Root = filepath.Join(fixture, "Projects")
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	writeVersionConfig(t, configPath, cfg)
	if err := os.WriteFile(filepath.Join(fixture, "background.json"), []byte(`{"version":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &ShelfService{}
	var sources []autoorganize.SessionSource
	if mixedSource {
		sessions := filepath.Join(fixture, "sessions")
		if err := os.MkdirAll(sessions, 0700); err != nil {
			t.Fatal(err)
		}
		sources = []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}
	}
	if _, err := s.SetAutoOrganize(true, []string{fixture}, sources); err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) {
		t.Helper()
		out, err := exec.Command(cli, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("workshelf command failed: %s: %v", out, err)
		}
	}
	oldFile, newFile := filepath.Join(fixture, "Desktop", "old.psd"), filepath.Join(fixture, "Desktop", "new.psd")
	mark := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		command("mark", "--config", configPath, "--agent", "codex", "--worktype", "设计·UI原型", "--project", "品牌", file)
	}
	mark(oldFile, "old")
	log, err := os.Create(filepath.Join(fixture, "native-process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	app := exec.Command(binary, "--background")
	app.Stdout, app.Stderr = log, log
	if err = app.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = app.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = app.Process.Signal(syscall.SIGTERM)
			<-done
		}
	}()
	wait := func(label string, ready func() bool) {
		t.Helper()
		for deadline := time.Now().Add(55 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
			select {
			case <-done:
				t.Fatalf("native process exited during %s", label)
			default:
			}
			if ready() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", label)
	}
	var first, second, third autoorganize.Operation
	wait("first marked file", func() bool {
		operations, e := autoorganize.List(cfg)
		if e == nil && len(operations) == 1 && operations[0].Status == "organized" && operations[0].MemoryInitialized {
			first = operations[0]
			return true
		}
		return false
	})
	t.Log("first marked file organized; objecting")
	command("auto", "reject", first.ID, "--agent", "codex", "--config", configPath)
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("first file was not restored", err)
	}
	mark(newFile, "new")
	wait("second marked file", func() bool {
		operations, e := autoorganize.List(cfg)
		if e != nil || len(operations) != 2 {
			return false
		}
		for _, op := range operations {
			if op.ID != first.ID && op.Status == "organized" && op.MemoryInitialized {
				second = op
				return len(op.Files) == 1 && op.Files[0] == newFile
			}
		}
		return false
	})
	t.Log("new marked file organized; checking restored file after another scan")
	time.Sleep(32 * time.Second)
	if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old" {
		t.Fatal("restored file was collected again", err)
	}
	if mixedSource {
		if err := os.WriteFile(oldFile, []byte("updated"), 0600); err != nil {
			t.Fatal(err)
		}
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "new-session", "cwd": filepath.Join(cfg.Root, "品牌")}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write", "input": "*** Begin Patch\n*** Update File: " + oldFile + "\n@@\n-old\n+updated\n*** End Patch\n"}}, map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write", "output": "Success. Updated the following files:\nM " + oldFile + "\n"}}}
		var data []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			data = append(data, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(fixture, "sessions", "new.jsonl"), data, 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		mark(oldFile, "updated")
	}
	wait("new mark on restored file", func() bool {
		operations, e := autoorganize.List(cfg)
		if e != nil || len(operations) != 3 {
			return false
		}
		for _, op := range operations {
			if op.ID != first.ID && op.ID != second.ID && op.Status == "organized" && op.MemoryInitialized {
				third = op
				return len(op.Files) == 1 && op.Files[0] == oldFile && (!mixedSource || len(op.Signals) == 1 && op.Signals[0].RecordSHA != "")
			}
		}
		return false
	})
	command("auto", "reject", second.ID, "--agent", "codex", "--config", configPath)
	command("auto", "reject", third.ID, "--agent", "codex", "--config", configPath)
	for path, content := range map[string]string{oldFile: "updated", newFile: "new"} {
		if data, e := os.ReadFile(path); e != nil || string(data) != content {
			t.Fatal("fixture was not restored", e)
		}
	}
	wait("objection records", func() bool {
		requests, e := autoorganize.PendingObjections(cfg)
		return e == nil && len(requests) == 0
	})
	if staleSession {
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "stale-session", "cwd": filepath.Join(fixture, "Projects", "旧项目")}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "stale-write", "input": "*** Begin Patch\n*** Update File: " + oldFile + "\n@@\n-old\n+older\n*** End Patch\n"}}, map[string]any{"type": "response_item", "timestamp": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "stale-write", "output": "Success. Updated the following files:\nM " + oldFile + "\n"}}}
		var data []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			data = append(data, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(fixture, "sessions", "stale.jsonl"), data, 0600); err != nil {
			t.Fatal(err)
		}
		wait("older session scanned", func() bool {
			data, e := os.ReadFile(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json"))
			var state struct {
				Grants []projectmemory.SourceGrant `json:"grants"`
			}
			if e != nil || json.Unmarshal(data, &state) != nil {
				return false
			}
			for _, grant := range state.Grants {
				for _, doc := range grant.Documents {
					if filepath.Base(doc.Path) == "stale.jsonl" && doc.Cursor.Line >= 3 {
						return true
					}
				}
			}
			return false
		})
		time.Sleep(32 * time.Second)
		policy, e := autoorganize.ReadPolicy(cfg)
		if e != nil {
			t.Fatal(e)
		}
		records, _, e := autoorganize.ReadSessionEvidence(cfg, policy)
		if e != nil || len(records) != 1 || records[0].Project != "品牌" {
			t.Fatal("older session replaced the latest project", records, e)
		}
		operations, e := autoorganize.List(cfg)
		if e != nil || len(operations) != 3 {
			t.Fatal("older session started another organization", e)
		}
		if data, e := os.ReadFile(oldFile); e != nil || string(data) != "updated" {
			t.Fatal("older session moved the restored file", e)
		}
	}
}
