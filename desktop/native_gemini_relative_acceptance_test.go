package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

func TestNativeGeminiRelativeAcceptance(t *testing.T) {
	t.Setenv("WORKSHELF_DISABLE_NOTIFICATIONS", "1")
	fixture, binary, cli := os.Getenv("WORKSHELF_NATIVE_AGENT_FIXTURE"), os.Getenv("WORKSHELF_NATIVE_AGENT_BINARY"), os.Getenv("WORKSHELF_NATIVE_AGENT_CLI")
	if fixture == "" {
		t.Skip("isolated native acceptance only")
	}
	if !filepath.IsAbs(fixture) || !filepath.IsAbs(binary) || !filepath.IsAbs(cli) {
		t.Fatal("isolated fixture and executables required")
	}
	project, desktop, sessions := filepath.Join(fixture, "Projects", "品牌"), filepath.Join(fixture, "Desktop"), filepath.Join(fixture, "sessions")
	for _, path := range []string{project, desktop, sessions} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(fixture, "config.json")
	t.Setenv("WORKSHELF_CONFIG", configPath)
	cfg := config.Default(fixture)
	cfg.Root = filepath.Join(fixture, "organized")
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	writeVersionConfig(t, configPath, cfg)
	if err := os.WriteFile(filepath.Join(fixture, "background.json"), []byte(`{"version":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &ShelfService{}
	if _, err := s.SetAutoOrganize(true, []string{fixture}, []autoorganize.SessionSource{{Adapter: "gemini", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
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
	progress := filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json")
	wait("initial scan", func() bool { _, e := os.Stat(progress); return e == nil })
	file := filepath.Join(desktop, "design.psd")
	if err = os.WriteFile(file, []byte("fixture artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte(project))}, map[string]any{"id": "message", "type": "gemini", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "content": "PRIVATE_CONVERSATION", "toolCalls": []any{map[string]any{"id": "write", "name": "write_file", "status": "success", "args": map[string]any{"file_path": "../../Desktop/design.psd", "content": "PRIVATE_FILE_CONTENT"}}}}}
	var data []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		data = append(data, append(b, '\n')...)
	}
	if err = os.WriteFile(filepath.Join(sessions, "new.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var op autoorganize.Operation
	wait("Gemini relative output organized", func() bool {
		operations, e := autoorganize.List(cfg)
		if e == nil && len(operations) == 1 && operations[0].Status == "organized" && operations[0].MemoryInitialized {
			op = operations[0]
			return op.Project == "品牌" && len(op.Signals) == 1 && op.Signals[0].Agent == "gemini"
		}
		return false
	})
	data, err = os.ReadFile(progress)
	if err != nil || bytes.Contains(data, []byte("PRIVATE_CONVERSATION")) || bytes.Contains(data, []byte("PRIVATE_FILE_CONTENT")) {
		t.Fatal("session contents appeared in progress", err)
	}
	if output, e := exec.Command(cli, "auto", "reject", op.ID, "--agent", "gemini", "--config", configPath).CombinedOutput(); e != nil {
		t.Fatalf("restore failed: %s: %v", output, e)
	}
	wait("restored file and memory", func() bool {
		data, e := os.ReadFile(file)
		pending, pe := autoorganize.PendingObjections(cfg)
		return e == nil && string(data) == "fixture artwork" && pe == nil && len(pending) == 0
	})
	memory, err := s.ProjectMemory("root:品牌")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.ProjectMemoryGraph("root:品牌", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Kind == "memory_source" && node.Name == "gemini" {
			return
		}
	}
	t.Fatal("Gemini origin not linked to project graph")
}

func TestNativeNotificationServiceStartup(t *testing.T) {
	binary := os.Getenv("WORKSHELF_NATIVE_AGENT_BINARY")
	if binary == "" {
		t.Skip("isolated signed application acceptance only")
	}
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "projects")
	writeVersionConfig(t, configPath, cfg)
	if err := os.WriteFile(filepath.Join(home, "background.json"), []byte(`{"version":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSHELF_CONFIG", configPath)
	t.Setenv("WORKSHELF_DISABLE_NOTIFICATIONS", "")
	app := exec.Command(binary, "--background")
	var output bytes.Buffer
	app.Stdout, app.Stderr = &output, &output
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- app.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("notification service stopped the application: %s: %v", output.String(), err)
	case <-time.After(3 * time.Second):
	}
	_ = app.Process.Signal(syscall.SIGTERM)
	<-done
}
