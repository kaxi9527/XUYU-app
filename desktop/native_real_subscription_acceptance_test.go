//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

// Opt-in: the installed process and account are real; all work data is synthetic.
func TestInstalledRealSubscriptionAgentFlow(t *testing.T) {
	binary := os.Getenv("WORKSHELF_NATIVE_REAL_BINARY")
	fixture := os.Getenv("WORKSHELF_NATIVE_REAL_FIXTURE")
	claudeClient := os.Getenv("WORKSHELF_NATIVE_REAL_CLAUDE_CLIENT")
	launchd := os.Getenv("WORKSHELF_NATIVE_REAL_LAUNCHD") == "1"
	if binary == "" || fixture == "" {
		t.Skip("requires explicitly authorized real-subscription acceptance")
	}
	if !filepath.IsAbs(binary) || !filepath.IsAbs(fixture) {
		t.Fatal("absolute application and fixture paths required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Dir(config.DefaultPath(home))
	file, err := os.CreateTemp(configDir, ".workshelf-agent-acceptance-*.json")
	if err != nil {
		t.Fatal(err)
	}
	configPath := file.Name()
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(configPath) })
	t.Setenv("WORKSHELF_CONFIG", configPath)
	t.Setenv("WORKSHELF_DISABLE_NOTIFICATIONS", "1")
	cfg := config.Default(fixture)
	cfg.Root = filepath.Join(fixture, "Projects")
	if volume := os.Getenv("WORKSHELF_NATIVE_REAL_TARGET_VOLUME"); volume != "" {
		if !filepath.IsAbs(volume) {
			t.Fatal("cross-device test volume must be an absolute path")
		}
		cfg.Root = filepath.Join(volume, "real-subscription-projects")
	}
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	writeVersionConfig(t, configPath, cfg)
	modelCatalog := filepath.Join(configDir, "models", "connections.json")
	before, err := os.ReadFile(modelCatalog)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := sha256.Sum256(before)
	sessions := filepath.Join(fixture, "sessions")
	adapter := "codex"
	if claudeClient != "" {
		sessions = filepath.Join(fixture, "claude", "projects")
		adapter = "claude"
	}
	workdir := filepath.Join(fixture, "品牌项目UI界面设计")
	files := []string{filepath.Join(fixture, "Desktop", "UI界面设计", "首页.txt"), filepath.Join(fixture, "Downloads", "UI界面设计", "封面.txt")}
	for _, dir := range []string{cfg.Root, sessions, workdir, filepath.Dir(files[0]), filepath.Dir(files[1])} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s := &ShelfService{}
	defer s.closeModels()
	s.connections.accounts.binary = filepath.Join(filepath.Dir(binary), "..", "Resources", "workshelf-model-core")
	if _, err = s.SetAutoOrganize(true, []string{fixture}, []autoorganize.SessionSource{{Adapter: adapter, Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil || !preview.UncappedOutput || !strings.Contains(preview.Model, "gpt-6.1-terra") {
		t.Fatal("selected Codex model could not be previewed", err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	objection, err := s.PreviewAutoObjectionModel()
	if err != nil || objection.RecipientSHA != preview.RecipientSHA {
		t.Fatal("objection recipient changed", err)
	}
	if _, err = s.SetAutoObjectionModel(true, objection.RecipientSHA, objection.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	s.closeModels()
	log, err := os.Create(filepath.Join(fixture, "native-process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	var app *exec.Cmd
	var done chan struct{}
	var processErr error
	label := fmt.Sprintf("%s.acceptance.%d", backgroundLabel, os.Getpid())
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	job := domain + "/" + label
	plist := filepath.Join(fixture, "acceptance-launchd.plist")
	active := false
	if launchd {
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(binary)))
		f := backgroundFiles{home: fixture, config: configPath, app: bundle}
		doc := strings.Replace(string(f.loginDocument()), backgroundLabel, label, 1)
		doc = strings.Replace(doc, "<key>EnvironmentVariables</key><dict>", "<key>EnvironmentVariables</key><dict><key>WORKSHELF_DISABLE_NOTIFICATIONS</key><string>1</string>", 1)
		if err = os.WriteFile(plist, []byte(doc), 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := func() {
		t.Helper()
		if launchd {
			if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
				t.Fatalf("temporary launchd startup failed: %s: %v", output, err)
			}
			active = true
			return
		}
		app = exec.Command(binary, "--background")
		app.Stdout, app.Stderr = log, log
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan struct{})
		go func() { processErr = app.Wait(); close(done) }()
	}
	stop := func() {
		t.Helper()
		if launchd {
			if active {
				if output, err := exec.Command("/bin/launchctl", "bootout", job).CombinedOutput(); err != nil {
					t.Errorf("temporary launchd shutdown failed: %s: %v", output, err)
				}
				active = false
			}
			return
		}
		select {
		case <-done:
		default:
			if err := app.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			<-done
		}
	}
	start()
	defer stop()
	wait := func(label string, check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
			if !launchd {
				select {
				case <-done:
					t.Fatalf("installed process exited during %s: %v", label, processErr)
				default:
				}
			}
			if check() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", label)
	}
	wait("initial session scan", func() bool {
		_, err := os.Stat(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json"))
		return err == nil
	})
	session := filepath.Join(sessions, "synthetic.jsonl")
	if claudeClient != "" {
		command := exec.Command("python3", claudeClient, "write")
		command.Env = append(os.Environ(), "WORKSHELF_TEST_FIXTURE="+fixture, "WORKSHELF_TEST_DESIGN_PATHS=1", "WORKSHELF_TEST_WORKDIR="+workdir)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated Claude client failed: %s: %v", output, err)
		}
	} else {
		for _, path := range files {
			if err = os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		patch := "*** Begin Patch\n*** Add File: " + files[0] + "\n+synthetic\n*** Add File: " + files[1] + "\n+synthetic\n*** End Patch\n"
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "synthetic-session", "cwd": workdir}}, map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write", "output": "Success. Updated the following files:\nA " + files[0] + "\nA " + files[1] + "\n"}}}
		var data []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			data = append(data, append(b, '\n')...)
		}
		if err = os.WriteFile(session, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("installed application is classifying synthetic files")
	var op autoorganize.Operation
	wait("automatic organization", func() bool {
		list, err := autoorganize.List(cfg)
		if err != nil || len(list) != 1 || list[0].Status != "organized" || !list[0].MemoryInitialized {
			return false
		}
		op = list[0]
		return true
	})
	if op.Summary == nil || op.Summary.Moved != 2 || len(op.ModelDecisions) != 1 || op.ModelDecisions[0].Model != preview.Model {
		t.Fatal("installed application did not record the expected model decision")
	}
	for _, item := range op.Summary.Items {
		if _, err := os.Stat(item.Source); !os.IsNotExist(err) {
			t.Fatal("source remains after automatic move", err)
		}
		if os.Getenv("WORKSHELF_NATIVE_REAL_TARGET_VOLUME") != "" && !item.CrossDevice {
			t.Fatal("automatic move did not cross devices")
		}
	}
	if launchd {
		if output, err := exec.Command("/bin/launchctl", "kickstart", "-k", job).CombinedOutput(); err != nil {
			t.Fatalf("temporary launchd restart failed: %s: %v", output, err)
		}
	} else {
		stop()
		start()
	}
	t.Log("installed application restarted; adding synthetic objection")
	if claudeClient != "" {
		command := exec.Command("python3", claudeClient, "objection")
		command.Env = append(os.Environ(), "WORKSHELF_TEST_FIXTURE="+fixture, "WORKSHELF_TEST_DESIGN_PATHS=1", "WORKSHELF_TEST_WORKDIR="+workdir)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated Claude objection failed: %s: %v", output, err)
		}
	} else {
		message := map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "这样不妥，请把刚刚挪走的文件放回原来的地方"}}}}
		b, _ := json.Marshal(message)
		f, err := os.OpenFile(session, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(append(b, '\n')); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	wait("objection and restoration", func() bool {
		list, err := autoorganize.List(cfg)
		return err == nil && len(list) == 1 && list[0].Status == "restored"
	})
	for _, path := range files {
		want := "synthetic"
		if claudeClient != "" {
			want = "fixture artwork"
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != want {
			t.Fatal("synthetic file not restored", err)
		}
	}
	reader := &ShelfService{}
	wait("project memory", func() bool {
		memory, err := reader.ProjectMemory("root:" + op.Project)
		if err != nil {
			return false
		}
		page, err := reader.ProjectMemoryEvents("root:"+op.Project, memory.Revision, "")
		if err != nil {
			return false
		}
		for _, event := range page.Items {
			if event.Kind == "automatic_project_restored" && event.Auto != nil && event.Auto.Objection != nil && event.Auto.Objection.Model == preview.Model {
				return true
			}
		}
		return false
	})
	memory, err := reader.ProjectMemory("root:" + op.Project)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := reader.ProjectMemoryGraph("root:"+op.Project, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range graph.Nodes {
		if node.Name == preview.Provider+" / "+preview.Model {
			found = true
		}
	}
	if !found {
		t.Fatal("model missing from relationship graph")
	}
	stop()
	after, err := os.ReadFile(modelCatalog)
	if err != nil || sha256.Sum256(after) != beforeHash {
		t.Fatal("user model configuration changed", err)
	}
}
