//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

// Opt-in acceptance of the desktop process with an isolated configuration and local model.
func TestNativeBackgroundAgentAcceptance(t *testing.T) {
	modelObjection := os.Getenv("WORKSHELF_NATIVE_MODEL_OBJECTION") == "1"
	restartBeforeObjection := os.Getenv("WORKSHELF_NATIVE_RESTART_BEFORE_OBJECTION") == "1"
	t.Setenv("WORKSHELF_DISABLE_NOTIFICATIONS", "1")
	namedObjection := os.Getenv("WORKSHELF_NATIVE_NAMED_OBJECTION") == "1"
	realClient := os.Getenv("WORKSHELF_NATIVE_CLAUDE_CLIENT")
	claudeShell := os.Getenv("WORKSHELF_NATIVE_CLAUDE_SHELL") == "1"
	codexShell := os.Getenv("WORKSHELF_NATIVE_CODEX_SHELL_CAT") == "1"
	rootSubdirectory := os.Getenv("WORKSHELF_NATIVE_ROOT_SUBDIRECTORY") == "1"
	outsideContext := os.Getenv("WORKSHELF_NATIVE_CONTEXT_OUTSIDE") == "1"
	switchUnregistered := os.Getenv("WORKSHELF_NATIVE_SWITCH_UNREGISTERED") == "1" || outsideContext
	switchProject := os.Getenv("WORKSHELF_NATIVE_SWITCH_PROJECT") == "1" || switchUnregistered || rootSubdirectory
	var realInput struct {
		Project  string `json:"project"`
		WorkType string `json:"workType"`
		Files    []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Backup string `json:"backup"`
		} `json:"files"`
	}
	if path := os.Getenv("WORKSHELF_NATIVE_REAL_INPUT"); path != "" {
		data, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(data, &realInput) != nil || len(realInput.Files) != 2 || realClient != "" || codexShell {
			t.Fatal("two explicitly selected real files and synthetic session are required")
		}
		for _, f := range realInput.Files {
			data, e := os.ReadFile(f.Path)
			backup, backupErr := os.ReadFile(f.Backup)
			if e != nil || backupErr != nil || sourceevidence.Hash(data) != f.SHA256 || sourceevidence.Hash(backup) != f.SHA256 {
				t.Fatal("real input or its backup changed before acceptance")
			}
		}
	}
	projectName, workType := "后台品牌", "设计·UI原型"
	if realInput.Project != "" {
		projectName = realInput.Project
	}
	if realInput.WorkType != "" {
		workType = realInput.WorkType
	}
	fixture, binary, cli := os.Getenv("WORKSHELF_NATIVE_AGENT_FIXTURE"), os.Getenv("WORKSHELF_NATIVE_AGENT_BINARY"), os.Getenv("WORKSHELF_NATIVE_AGENT_CLI")
	if fixture == "" {
		t.Skip("isolated native acceptance only")
	}
	if !filepath.IsAbs(fixture) || !filepath.IsAbs(binary) || !filepath.IsAbs(cli) {
		t.Fatal("isolated fixture and executables required")
	}
	if err := os.MkdirAll(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	newWorkDir := filepath.Join(fixture, "Contexts", "新工作区")
	if outsideContext {
		newWorkDir = filepath.Join(fixture, "Contexts", "品牌工程", "packages", "web")
	}
	if switchUnregistered {
		if err := os.MkdirAll(newWorkDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(fixture, "config.json")
	t.Setenv("WORKSHELF_CONFIG", configPath)
	cfg := config.Default(fixture)
	cfg.Root = filepath.Join(fixture, "Projects")
	if volume := os.Getenv("WORKSHELF_NATIVE_TARGET_VOLUME"); volume != "" {
		if !filepath.IsAbs(volume) {
			t.Fatal("cross-device test volume must be an absolute path")
		}
		cfg.Root = filepath.Join(volume, "mock-model-projects")
	}
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	cfg.TimeoutSeconds = 10
	writeVersionConfig(t, configPath, cfg)
	if err := os.WriteFile(filepath.Join(fixture, "background.json"), []byte(`{"version":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(fixture, "sessions")
	adapter := "codex"
	if realClient != "" {
		sessions = filepath.Join(fixture, "claude", "projects")
		adapter = "claude"
	}
	for _, dir := range []string{cfg.Root, sessions, filepath.Join(fixture, "Desktop"), filepath.Join(fixture, "Downloads")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) == 0 {
			w.WriteHeader(400)
			return
		}
		input := req.Messages[len(req.Messages)-1].Content
		answer := "OK"
		if input != "Reply with the word OK." {
			calls.Add(1)
			if modelObjection {
				var message struct {
					Message string `json:"message"`
					Project string `json:"project"`
				}
				if json.Unmarshal([]byte(input), &message) == nil && message.Message != "" {
					if message.Message != "这样不妥，请把刚刚挪走的文件放回原来的地方" || message.Project != projectName {
						w.WriteHeader(400)
						return
					}
					answer = `{"objection":true}`
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
					return
				}
			}
			var body struct {
				Groups []autoorganize.ModelTask `json:"groups"`
			}
			if json.Unmarshal([]byte(input), &body) != nil || len(body.Groups) != 1 || strings.Contains(input, "PRIVATE_CONVERSATION") || strings.Contains(input, fixture) || outsideContext && strings.Contains(input, "Contexts") {
				w.WriteHeader(400)
				return
			}
			decisionProject := projectName
			if switchUnregistered && strings.Contains(input, "new.psd") {
				decisionProject = "新项目"
			}
			result, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": decisionProject, "workType": workType, "confident": true, "reason": "同一项目成果"}}})
			answer = string(result)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
	}))
	defer server.Close()
	client := modelhttp.NewClient()
	store, err := models.NewStore(filepath.Join(fixture, "models"), nil, &accountModelAdapter{direct: client})
	if err != nil {
		t.Fatal(err)
	}
	s := &ShelfService{}
	s.connections.store, s.connections.client, s.connections.protocolReady = store, client, true
	prepared, err := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "本机模拟", Protocol: models.Chat, BaseURL: server.URL + "/v1", ModelID: "fixture", ModelName: "测试模型"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(context.Background(), prepared, "save")
	prepared.Close()
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyModel(context.Background(), saved.Revision, "verify", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetMode(context.Background(), verified.Receipt.Revision, "use", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	roots := []string{fixture}
	if rootSubdirectory {
		project := filepath.Join(cfg.Root, projectName)
		if err = os.MkdirAll(project, 0700); err != nil {
			t.Fatal(err)
		}
		roots = []string{project, filepath.Join(fixture, "Desktop"), filepath.Join(fixture, "Downloads")}
	} else if outsideContext {
		roots = []string{filepath.Join(fixture, "Desktop"), filepath.Join(fixture, "Downloads")}
	} else if switchUnregistered {
		roots = append(roots, newWorkDir)
	}
	for _, f := range realInput.Files {
		root := filepath.Dir(f.Path)
		if roots[len(roots)-1] != root {
			roots = append(roots, root)
		}
	}
	if _, err = s.SetAutoOrganize(true, roots, []autoorganize.SessionSource{{Adapter: adapter, Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	if modelObjection {
		if _, err = s.SetAutoObjections(true); err != nil {
			t.Fatal(err)
		}
		preview, err := s.PreviewAutoObjectionModel()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetAutoObjectionModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	s.closeModels()
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
	var processErr error
	go func() { processErr = app.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = app.Process.Signal(syscall.SIGTERM)
			<-done
		}
	}()
	if len(realInput.Files) > 0 {
		defer func() {
			select {
			case <-done:
			default:
				_ = app.Process.Signal(syscall.SIGTERM)
				<-done
			}
			operations, e := autoorganize.List(cfg)
			if e != nil {
				t.Errorf("cannot inspect automatic restore receipts: %v", e)
			}
			for _, operation := range operations {
				if operation.Status != "restored" {
					if _, e = autoorganize.Restore(cfg, operation.ID); e != nil {
						t.Errorf("automatic recovery failed: %v", e)
					}
				}
			}
			for _, f := range realInput.Files {
				data, e := os.ReadFile(f.Path)
				if e != nil || sourceevidence.Hash(data) != f.SHA256 {
					t.Errorf("real source was not restored; verified backup: %s", f.Backup)
				}
			}
		}()
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(55 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-done:
				t.Fatalf("native process exited during %s: %v", label, processErr)
			default:
			}
			if check() {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", label)
	}
	wait("initial background scan", func() bool {
		_, e := os.Stat(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json"))
		return e == nil
	})
	t.Log("native background process is running; adding a new session")
	files := []string{filepath.Join(fixture, "Desktop", "首页.png"), filepath.Join(fixture, "Downloads", "封面.png")}
	if len(realInput.Files) > 0 {
		files = []string{realInput.Files[0].Path, realInput.Files[1].Path}
	}
	if codexShell {
		files = []string{filepath.Join(fixture, "Desktop", "a.txt"), filepath.Join(fixture, "Downloads", "b.txt")}
	}
	if realClient != "" {
		files = []string{filepath.Join(fixture, "Desktop", "首页.txt"), filepath.Join(fixture, "Downloads", "封面.txt")}
		command := exec.Command("python3", realClient, "write")
		command.Env = append(os.Environ(), "WORKSHELF_TEST_FIXTURE="+fixture)
		if output, e := command.CombinedOutput(); e != nil {
			t.Fatalf("isolated Claude client failed: %s: %v", output, e)
		}
	} else {
		patch := "*** Begin Patch\n"
		for _, file := range files {
			if len(realInput.Files) > 0 {
				patch += "*** Update File: " + file + "\n@@\n"
				continue
			}
			content := "fixture artwork"
			if codexShell {
				content += "\n"
			}
			if err = os.WriteFile(file, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			patch += "*** Add File: " + file + "\n+fixture artwork\n"
		}
		patch += "*** End Patch\n"
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "native-background-session", "cwd": fixture}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "text": "PRIVATE_CONVERSATION"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA output\n"}}}
		if mode := os.Getenv("WORKSHELF_NATIVE_CODEX_EXEC_PATCH"); mode == "1" || mode == "inline" {
			literal, e := json.Marshal(patch)
			if e != nil {
				t.Fatal(e)
			}
			program := "const patch = " + string(literal) + ";\ntext(await tools.apply_patch(patch));"
			if mode == "inline" {
				program = "text(await tools.apply_patch(" + string(literal) + "));"
			}
			rows[2] = map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "write-1", "input": program}}
			rows[3] = map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": []any{map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.5 seconds\nOutput:\n"}, map[string]any{"type": "input_text", "text": "{}"}}}}
		}
		if os.Getenv("WORKSHELF_NATIVE_SHELL_PATCH") == "1" {
			args, err := json.Marshal(map[string]any{"cmd": "apply_patch <<'PATCH'\n" + patch + "PATCH\n", "workdir": fixture})
			if err != nil {
				t.Fatal(err)
			}
			rows[2] = map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "write-1", "arguments": string(args)}}
			rows[3] = map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "write-1", "output": "Process exited with code 0\nFinal output:\nSuccess. Updated the following files:\nA output\n"}}
		}
		if codexShell {
			rows = rows[:2]
			for i, path := range []string{"Desktop/a.txt", "Downloads/b.txt"} {
				id := []string{"one", "two"}[i]
				args, e := json.Marshal(map[string]string{"cmd": "cat > " + path + " <<'EOF'\nfixture artwork\nEOF", "workdir": fixture})
				if e != nil {
					t.Fatal(e)
				}
				rows = append(rows, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": id, "arguments": string(args)}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": id, "output": "Process exited with code 0\nFinal output:\n"}})
			}
		}
		var lines []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			lines = append(lines, append(b, '\n')...)
		}
		if err = os.WriteFile(filepath.Join(sessions, "new.jsonl"), lines, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var op autoorganize.Operation
	wait("automatic classification and organization", func() bool {
		list, e := autoorganize.List(cfg)
		if e != nil || len(list) != 1 || list[0].Status != "organized" || !list[0].MemoryInitialized {
			return false
		}
		op = list[0]
		return true
	})
	if calls.Load() != 1 || op.Summary == nil || op.Summary.Moved != 2 || len(op.ModelDecisions) != 1 {
		t.Fatal("unexpected organization", op.Status, calls.Load())
	}
	for _, item := range op.Summary.Items {
		if _, err := os.Stat(item.Source); !os.IsNotExist(err) {
			t.Fatal("source remains after automatic move", err)
		}
		if os.Getenv("WORKSHELF_NATIVE_TARGET_VOLUME") != "" && !item.CrossDevice {
			t.Fatal("automatic move did not cross devices")
		}
	}
	if realClient != "" {
		policy, e := autoorganize.ReadPolicy(cfg)
		if e != nil {
			t.Fatal(e)
		}
		_, issues, e := autoorganize.ReadSessionEvidence(cfg, policy)
		if e != nil || len(issues) != 0 {
			t.Fatal("read-only Claude calls reported as unrecognized output", issues, e)
		}
	}
	listing := exec.Command(cli, "auto", "list", "--project", projectName, "--config", configPath, "--json")
	output, err := listing.Output()
	if err != nil {
		t.Fatalf("worker could not list organization: %v", err)
	}
	var listed []autoorganize.Operation
	if err = json.Unmarshal(output, &listed); err != nil || len(listed) != 1 || listed[0].ID != op.ID {
		t.Fatalf("worker could not identify organization: %s %v", output, err)
	}
	var secondID, secondCode, secondFile string
	if switchProject {
		project := filepath.Join(fixture, "Existing", "新工程")
		if switchUnregistered {
			project = newWorkDir
		} else if rootSubdirectory {
			project = filepath.Join(cfg.Root, projectName, "packages", "web")
		}
		code, newFile := filepath.Join(project, "main.go"), filepath.Join(fixture, "Desktop", "new.psd")
		if !switchUnregistered && !rootSubdirectory {
			if err = os.MkdirAll(filepath.Join(project, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
		} else if rootSubdirectory {
			if err = os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err = os.WriteFile(code, []byte("package main"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(newFile, []byte("new work\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if !switchUnregistered && !rootSubdirectory {
			reg, e := registry.Load(cfg)
			if os.IsNotExist(e) {
				reg = &registry.Registry{Version: registry.Version}
			} else if e != nil {
				t.Fatal(e)
			}
			reg.Projects = append(reg.Projects, registry.Project{ID: "switch-project", Name: "新工程", Path: project})
			if _, e = registry.Save(cfg, reg, fixture); e != nil {
				t.Fatal(e)
			}
		}
		rows := []any{map[string]any{"type": "turn_context", "payload": map[string]any{"cwd": project, "turn_id": "second-project"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-2", "input": "*** Begin Patch\n*** Add File: " + newFile + "\n+new work\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-2", "output": "Success. Updated the following files:\nA " + newFile + "\n"}}}
		f, e := os.OpenFile(filepath.Join(sessions, "new.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			b, _ := json.Marshal(row)
			if _, e = f.Write(append(b, '\n')); e != nil {
				t.Fatal(e)
			}
		}
		if e = f.Close(); e != nil {
			t.Fatal(e)
		}
		var second autoorganize.Operation
		wait("same session switched to the registered project", func() bool {
			operations, e := autoorganize.List(cfg)
			if e != nil {
				return false
			}
			for _, item := range operations {
				matches := item.ProjectKey == "place:switch-project"
				if switchUnregistered {
					matches = item.Project == "新项目" && item.CreatedProject
				} else if rootSubdirectory {
					matches = item.Project == projectName && item.ID != op.ID
				}
				if matches && item.Status == "organized" && item.MemoryInitialized {
					second = item
					return true
				}
			}
			return false
		})
		target, decisions := project, 0
		if switchUnregistered {
			target, decisions = filepath.Join(cfg.Root, "新项目"), 1
		} else if rootSubdirectory {
			target = filepath.Join(cfg.Root, projectName)
		}
		if second.Target != target || second.Summary == nil || second.Summary.Moved != 1 || len(second.ModelDecisions) != decisions {
			t.Fatal("new work did not reach the registered project", second.Status)
		}
		if switchUnregistered && second.ModelDecisions[0].TaskID == op.ModelDecisions[0].TaskID {
			t.Fatal("new project reused the previous context's model judgment")
		}
		if namedObjection {
			secondID, secondCode, secondFile = second.ID, code, newFile
		} else {
			command := exec.Command(cli, "auto", "reject", second.ID, "--agent", "claude", "--config", configPath, "--json")
			if output, e := command.CombinedOutput(); e != nil {
				t.Fatalf("second project restore failed: %s: %v", output, e)
			}
			for path, want := range map[string]string{code: "package main", newFile: "new work\n"} {
				if data, e := os.ReadFile(path); e != nil || string(data) != want {
					t.Fatal("new project or restored file changed", e)
				}
			}
		}
	}
	sessionObjection := os.Getenv("WORKSHELF_NATIVE_SESSION_OBJECTION") == "1" || modelObjection || namedObjection
	requestedBy := "claude"
	if sessionObjection {
		requestedBy = "用户"
		if _, err = (&ShelfService{}).SetAutoObjections(true); err != nil {
			t.Fatal(err)
		}
		if restartBeforeObjection {
			if err = app.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			<-done
			app = exec.Command(binary, "--background")
			app.Stdout, app.Stderr = log, log
			if err = app.Start(); err != nil {
				t.Fatal(err)
			}
			done = make(chan struct{})
			go func() { processErr = app.Wait(); close(done) }()
		}
		text := "撤销这次整理"
		if modelObjection {
			text = "这样不妥，请把刚刚挪走的文件放回原来的地方"
		}
		if namedObjection {
			text = "撤销「" + projectName + "」的整理"
		}
		if realClient != "" {
			command := exec.Command("python3", realClient, "objection")
			command.Env = append(os.Environ(), "WORKSHELF_TEST_FIXTURE="+fixture)
			if output, e := command.CombinedOutput(); e != nil {
				t.Fatalf("isolated Claude objection failed: %s: %v", output, e)
			}
		} else {
			row := map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}
			b, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(filepath.Join(sessions, "new.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(append(b, '\n')); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		t.Log("native background process organized two files; adding session objection")
		wait("session objection restore", func() bool {
			for _, path := range files {
				if _, err := os.Stat(path); err != nil {
					return false
				}
			}
			return true
		})
		if rootSubdirectory {
			time.Sleep(32 * time.Second)
			for _, path := range append(files, filepath.Join(fixture, "Desktop", "new.psd")) {
				if _, e := os.Stat(path); e != nil {
					t.Fatal("restored file was collected again", e)
				}
			}
		}
		if namedObjection {
			if _, e := os.Stat(secondFile); !os.IsNotExist(e) {
				t.Fatal("unrelated project was restored", e)
			}
			command := exec.Command(cli, "auto", "reject", secondID, "--agent", "claude", "--config", configPath, "--json")
			if output, e := command.CombinedOutput(); e != nil {
				t.Fatalf("second project restore failed: %s: %v", output, e)
			}
			for path, want := range map[string]string{secondCode: "package main", secondFile: "new work\n"} {
				if data, e := os.ReadFile(path); e != nil || string(data) != want {
					t.Fatal("new project or restored file changed", e)
				}
			}
		}
	} else {
		t.Log("native background process organized two files; submitting worker objection")
		command := exec.Command(cli, "auto", "reject", op.ID, "--agent", "claude", "--config", configPath, "--json")
		if output, e := command.CombinedOutput(); e != nil {
			t.Fatalf("worker objection failed: %s: %v", output, e)
		}
	}
	for i, file := range files {
		if len(realInput.Files) > 0 {
			data, e := os.ReadFile(file)
			if e != nil || sourceevidence.Hash(data) != realInput.Files[i].SHA256 {
				t.Fatal("real file not restored byte for byte", e)
			}
			continue
		}
		content := "fixture artwork"
		if claudeShell || codexShell {
			content += "\n"
		}
		if data, e := os.ReadFile(file); e != nil || string(data) != content {
			t.Fatal("file not restored", e)
		}
	}
	reader := &ShelfService{}
	wait("worker objection in project memory", func() bool {
		state, e := reader.ProjectMemory("root:" + projectName)
		if e != nil {
			return false
		}
		page, e := reader.ProjectMemoryEvents("root:"+projectName, state.Revision, "")
		if e != nil {
			return false
		}
		for _, event := range page.Items {
			if event.Kind == "automatic_project_restored" && event.Auto != nil && event.Auto.RequestedBy == requestedBy {
				return !modelObjection || namedObjection || event.Auto.Objection != nil && event.Auto.Objection.Provider == "本机模拟" && event.Auto.Objection.Model == "测试模型"
			}
		}
		return false
	})
	state, err := reader.ProjectMemory("root:" + projectName)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := reader.ProjectMemoryGraph("root:"+projectName, state.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	modelLinked, workerLinked := false, sessionObjection
	for _, node := range graph.Nodes {
		if node.Name == "本机模拟 / 测试模型" {
			modelLinked = true
		}
		if node.Name == "claude" {
			workerLinked = true
		}
	}
	if !modelLinked || !workerLinked {
		t.Fatal("model or worker missing from project graph")
	}
	expectedCalls := int32(2)
	if namedObjection {
		expectedCalls = 1
	}
	if switchUnregistered {
		expectedCalls = 3
	}
	if modelObjection && calls.Load() != expectedCalls {
		t.Fatal("model did not judge the objection", calls.Load())
	}
	if err = app.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-done
	result, err := json.Marshal(map[string]any{"backgroundProcess": true, "newSession": true, "realFiles": len(realInput.Files), "realClaudeClient": realClient != "", "claudeShellWrite": claudeShell, "codexShellWrite": codexShell, "switchedProject": switchProject, "unregisteredProject": switchUnregistered, "outsideContext": outsideContext, "shellPatch": os.Getenv("WORKSHELF_NATIVE_SHELL_PATCH") == "1", "modelCalls": calls.Load(), "filesRestored": 2, "objectionBy": requestedBy, "sessionObjection": sessionObjection, "modelObjection": modelObjection, "restartedBeforeObjection": restartBeforeObjection && sessionObjection, "objectionRecorded": true, "graphLinked": true, "ownedProcessStopped": true})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(fixture, "native-result.json"), result, 0600); err != nil {
		t.Fatal(err)
	}
}
