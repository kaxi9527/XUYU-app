package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func acceptanceClaudeSession(session, cwd, file, call string) []any {
	return []any{
		map[string]any{"type": "assistant", "sessionId": session, "cwd": cwd, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": call, "name": "Write", "input": map[string]any{"file_path": file, "content": "fixture artwork"}}}}},
		map[string]any{"type": "user", "sessionId": session, "cwd": cwd, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": call, "is_error": false}}}},
	}
}

// Opt-in transport for the real browser fixture. This file is excluded from
// production builds; the finite method list cannot execute arbitrary methods.
func TestVersionBrowserAcceptance(t *testing.T) {
	fixture := os.Getenv("WORKSHELF_ACCEPTANCE_FIXTURE")
	if fixture == "" {
		t.Skip("isolated browser runner only")
	}
	if !filepath.IsAbs(fixture) || filepath.Clean(os.Getenv("WORKSHELF_CONFIG")) != filepath.Join(fixture, "config.json") {
		t.Fatal("explicit isolated config required")
	}
	token := os.Getenv("WORKSHELF_ACCEPTANCE_TOKEN")
	if len(token) < 24 {
		t.Fatal("fixture token required")
	}
	cfg := config.Default(fixture)
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	writeVersionConfig(t, filepath.Join(fixture, "config.json"), cfg)
	project := filepath.Join(cfg.Root, "品牌项目")
	first := versionFixtureFile(t, project, "首页-v1.txt", "first content")
	second := versionFixtureFile(t, project, "首页-v2.txt", "second content")
	versionFixtureFile(t, project, "素材/长名称-版式参考.txt", "reference")
	versionFixtureFile(t, project, ".hidden.txt", "ordinary hidden")
	versionFixtureFile(t, project, ".codex/config.toml", "protected")
	versionFixtureFile(t, project, ".workshelf-origin.jsonl", "{\"file\":\"首页-v1.txt\",\"agent\":\"codex\",\"time\":\"2026-11-08T12:00:00Z\"}\n")
	s := &ShelfService{}
	discoveredAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model-discover"
	customAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model-custom-profile"
	noCwdAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model-external-nocwd"
	autoProjectName := "自动项目"
	if noCwdAutoCase {
		autoProjectName = "团队/自动项目"
	}
	externalAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model-external" || discoveredAutoCase || noCwdAutoCase || customAutoCase
	modelExistingCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model-existing" || externalAutoCase
	modelAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-model" || modelExistingCase
	objectionModelCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-session-objection-model"
	autoProject := filepath.Join(cfg.Root, "自动项目")
	if externalAutoCase {
		autoProject = filepath.Join(fixture, "Projects", "自动项目")
	}
	if modelExistingCase {
		versionFixtureFile(t, autoProject, "原有文件.txt", "existing project content")
	}
	if customAutoCase {
		profile := worktype.Profile{Name: "自己的模板", Folders: []string{"资料", "设计成品"}, Roles: map[string]string{"source": "资料"}, Rules: []worktype.Rule{{Kind: "extension", Value: "png", Folder: "设计成品"}}}
		body, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(autoProject, worktype.ProfileFile), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if discoveredAutoCase {
		versionFixtureFile(t, autoProject, "project.psd", "existing design source")
	}
	if externalAutoCase && !discoveredAutoCase {
		reg := registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "browser-external", Name: autoProjectName, Path: autoProject}}}
		data, err := json.Marshal(reg)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(registry.Dir(cfg), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(registry.Dir(cfg), registry.FileName), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if modelAutoCase || objectionModelCase {
		modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) == 0 {
				w.WriteHeader(400)
				return
			}
			input := request.Messages[len(request.Messages)-1].Content
			answer := "OK"
			if input != "Reply with the word OK." {
				if objectionModelCase {
					var payload struct {
						Message string `json:"message"`
						Project string `json:"project"`
					}
					if json.Unmarshal([]byte(input), &payload) != nil || payload.Message != "这样不妥，请把刚刚挪走的文件放回原来的地方" || payload.Project != "自动项目" {
						w.WriteHeader(400)
						return
					}
					answer = `{"objection":true}`
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
					return
				}
				var payload struct {
					Groups   []autoorganize.ModelTask `json:"groups"`
					Projects []string                 `json:"existingProjects"`
				}
				if json.Unmarshal([]byte(input), &payload) != nil || len(payload.Groups) != 1 {
					w.WriteHeader(400)
					return
				}
				if noCwdAutoCase {
					found := false
					for _, name := range payload.Projects {
						if name == autoProjectName {
							found = true
						}
					}
					if !found {
						w.WriteHeader(400)
						return
					}
				}
				body, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": payload.Groups[0].ID, "project": autoProjectName, "workType": "设计·UI原型", "confident": true, "reason": "同一会话的设计成果"}}})
				answer = string(body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
		}))
		defer modelServer.Close()
		client := modelhttp.NewClient()
		store, err := models.NewStore(filepath.Join(fixture, "model-config"), nil, &accountModelAdapter{direct: client})
		if err != nil {
			t.Fatal(err)
		}
		s.connections.store, s.connections.client, s.connections.protocolReady = store, client, true
		defer s.closeModels()
		p, err := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "本机模拟", Protocol: models.Chat, BaseURL: modelServer.URL + "/v1", ModelID: "fixture", ModelName: "测试模型"})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		saved, err := store.Save(context.Background(), p, "save")
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
	}
	geminiAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-gemini"
	claudeAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-claude"
	observeAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-observe-only"
	objectionAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-session-objection" || objectionModelCase
	unassignedAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-unassigned" || modelAutoCase
	sessionAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-session" || unassignedAutoCase || geminiAutoCase || claudeAutoCase || observeAutoCase || objectionAutoCase
	unknownAutoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-unknown"
	autoCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "auto-organize" || sessionAutoCase || unknownAutoCase
	autoFile := filepath.Join(fixture, "Desktop", "自动设计.psd")
	if unknownAutoCase {
		autoFile = filepath.Join(fixture, "Desktop", "需求说明.md")
	}
	autoSecond := filepath.Join(fixture, "Desktop", "后续设计.psd")
	if geminiAutoCase {
		autoSecond = filepath.Join(fixture, "Downloads", "后续设计.psd")
	}
	if unassignedAutoCase {
		autoFile = filepath.Join(fixture, "Desktop", "图一.png")
		autoSecond = filepath.Join(fixture, "Desktop", "图二.png")
	}
	autoSource := filepath.Join(fixture, "sessions")
	if autoCase {
		if err := os.MkdirAll(filepath.Dir(autoFile), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(autoFile, []byte("fixture artwork"), 0600); err != nil {
			t.Fatal(err)
		}
		if sessionAutoCase {
			cwd := filepath.Join(fixture, "Projects", "自动项目")
			if unassignedAutoCase {
				cwd = fixture
				if externalAutoCase && !noCwdAutoCase {
					cwd = autoProject
				}
				if err := os.WriteFile(autoSecond, []byte("later artwork"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(cwd, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(autoSource, 0700); err != nil {
				t.Fatal(err)
			}
			patch := "*** Begin Patch\n*** Add File: " + autoFile + "\n+fixture artwork\n"
			if unassignedAutoCase {
				patch += "*** Add File: " + autoSecond + "\n+later artwork\n"
			}
			patch += "*** End Patch\n"
			lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "auto-session", "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + autoFile + "\n"}}}
			if geminiAutoCase {
				hash := sha256.Sum256([]byte(cwd))
				lines = []any{map[string]any{"sessionId": "auto-session", "projectHash": fmt.Sprintf("%x", hash)}, map[string]any{"id": "message-1", "type": "gemini", "timestamp": "2026-11-09T10:00:00Z", "toolCalls": []any{map[string]any{"id": "c1", "name": "write_file", "status": "success", "args": map[string]any{"file_path": autoFile, "content": "fixture artwork"}}}}}
			}
			if claudeAutoCase {
				lines = acceptanceClaudeSession("auto-session", cwd, autoFile, "c1")
			}
			var data []byte
			for _, line := range lines {
				b, _ := json.Marshal(line)
				data = append(data, append(b, '\n')...)
			}
			if err := os.WriteFile(filepath.Join(autoSource, "session.jsonl"), data, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			trade := "设计·UI原型"
			if unknownAutoCase {
				trade = ""
			}
			err := activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: autoFile, Project: "自动项目", WorkType: trade, Agent: "codex", Time: time.Now().Format(time.RFC3339)})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var graphFixture *graphAcceptanceFixture
	if os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "work-graph" {
		graphFixture = seedGraphAcceptance(t, s, first, second, versionCatalogPath(cfg))
	}
	allowed := map[string]bool{}
	if autoCase {
		for _, name := range []string{"Settings", "AutoOrganizeStatus", "SetAutoOrganize", "SetAutoObjections", "PreviewAutoObjectionModel", "SetAutoObjectionModel", "RunAutoOrganize", "RejectAutoOrganize", "ConfirmAutoProjectTrade", "ConfirmAutoSession", "ProjectMemory", "ProjectMemoryEvents", "ProjectMemoryGraph", "PreviewAutoModel", "SetAutoModel"} {
			allowed[name] = true
		}
	}
	for _, name := range []string{"Info", "Project", "InferWorkTypes", "ProjectWorks", "WorkVersions", "VersionDetail", "VersionFile", "OrganizeBrowse", "PreviewRegisterVersion", "CancelVersionPreview", "DiscardRegisterPreview", "ApplyVersionAction", "VersionOperation", "PreviewVersionRegroup"} {
		allowed[name] = true
	}
	if graphFixture != nil {
		allowed["Projects"] = true
		allowed["WorkGraph"] = true
	}
	directoryCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "project-source-directory"
	sourcesCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "project-sources" || directoryCase
	sourcePath := filepath.Join(project, ".workshelf-origin.jsonl")
	if directoryCase {
		sourcePath = filepath.Join(fixture, "agent-sessions")
		if err := os.MkdirAll(filepath.Join(sourcePath, "2026", "11"), 0700); err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(map[string]string{"path": first, "agent": "codex", "session": "directory-first"})
		if err := os.WriteFile(filepath.Join(sourcePath, "2026", "11", "first.jsonl"), append(line, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	memoryCase := os.Getenv("WORKSHELF_ACCEPTANCE_CASE") == "project-memory" || sourcesCase
	if memoryCase {
		for _, name := range []string{"Projects", "ProjectMemory", "ProjectMemoryEvents", "ProjectMemoryFiles", "ApplyProjectMemoryAction", "ProjectMemoryOperation", "ProjectMemoryGraph"} {
			allowed[name] = true
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9258")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-WorkShelf-Acceptance")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == "OPTIONS" {
			return
		}
		if r.Method != "POST" || r.Header.Get("X-WorkShelf-Acceptance") != token {
			http.Error(w, "unauthorized", 403)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		var value any
		var callErr error
		if req.Method == "ApplyProjectMemoryAction" && len(req.Args) == 1 {
			var action MemoryAction
			if json.Unmarshal(req.Args[0], &action) != nil {
				http.Error(w, "invalid action", 400)
				return
			}
			if strings.HasPrefix(action.Kind, "source_") && (!sourcesCase || action.SourcePath != sourcePath || action.SourceDirectory != directoryCase) {
				http.Error(w, "source outside fixture", 403)
				return
			}
		}
		if req.Method == "SetAutoOrganize" {
			var roots []string
			var sources []struct {
				Adapter string `json:"adapter"`
				Path    string `json:"path"`
			}
			adapter := "codex"
			if geminiAutoCase {
				adapter = "gemini"
			}
			if claudeAutoCase {
				adapter = "claude"
			}
			if !autoCase || len(req.Args) != 3 || json.Unmarshal(req.Args[1], &roots) != nil || len(roots) != 1 || roots[0] != fixture || json.Unmarshal(req.Args[2], &sources) != nil || (!sessionAutoCase && len(sources) != 0) || (sessionAutoCase && (len(sources) != 1 || sources[0].Adapter != adapter || sources[0].Path != autoSource)) {
				http.Error(w, "outside fixture", 403)
				return
			}
		}
		switch req.Method {
		case "FixtureAppendObjection":
			if !objectionAutoCase {
				http.Error(w, "wrong fixture", 403)
				return
			}
			text := "撤销这次整理"
			if objectionModelCase {
				text = "这样不妥，请把刚刚挪走的文件放回原来的地方"
			}
			line, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}})
			f, e := os.OpenFile(filepath.Join(autoSource, "session.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				callErr = e
				break
			}
			_, callErr = f.Write(append(line, '\n'))
			if e = f.Close(); callErr == nil {
				callErr = e
			}
			value = true
		case "FixtureAppendAutoSession":
			if !sessionAutoCase {
				http.Error(w, "wrong fixture", 403)
				return
			}
			if e := os.MkdirAll(filepath.Dir(autoSecond), 0700); e != nil {
				callErr = e
				break
			}
			if e := os.WriteFile(autoSecond, []byte("later artwork"), 0600); e != nil {
				callErr = e
				break
			}
			cwd := filepath.Join(fixture, "Projects", "自动项目")
			lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "auto-session-2", "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c2", "input": "*** Begin Patch\n*** Add File: " + autoSecond + "\n+later artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c2", "output": "Success. Updated the following files:\nA " + autoSecond + "\n"}}}
			if geminiAutoCase {
				hash := sha256.Sum256([]byte(cwd))
				lines = []any{map[string]any{"sessionId": "auto-session-2", "projectHash": fmt.Sprintf("%x", hash)}, map[string]any{"id": "message-2", "type": "gemini", "timestamp": "2026-11-09T10:01:00Z", "toolCalls": []any{map[string]any{"id": "c2", "name": "write_file", "status": "success", "args": map[string]any{"file_path": autoSecond, "content": "later artwork"}}}}}
			}
			if claudeAutoCase {
				lines = acceptanceClaudeSession("auto-session-2", cwd, autoSecond, "c2")
			}
			var data []byte
			for _, line := range lines {
				b, _ := json.Marshal(line)
				data = append(data, append(b, '\n')...)
			}
			callErr = os.WriteFile(filepath.Join(autoSource, "second.jsonl"), data, 0600)
			value = true
		case "FixtureAppendSource":
			if !sourcesCase {
				http.Error(w, "wrong fixture", 403)
				return
			}
			data, e := os.ReadFile(first)
			if e != nil {
				callErr = e
				break
			}
			sum := sha256.Sum256(data)
			line, _ := json.Marshal(map[string]any{"path": first, "file": filepath.Base(first), "agent": "claude", "task": "browser-source-task", "session": "browser-source-session", "sha256": hex.EncodeToString(sum[:])})
			if directoryCase {
				newDir := filepath.Join(sourcePath, "new-sessions")
				if e = os.MkdirAll(newDir, 0700); e != nil {
					callErr = e
					break
				}
				callErr = os.WriteFile(filepath.Join(newDir, fmt.Sprintf("new-%d.jsonl", time.Now().UnixNano())), append(line, '\n'), 0600)
				value = true
				break
			}
			file, e := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				callErr = e
				break
			}
			_, callErr = file.Write(append(line, '\n'))
			file.Close()
			value = true
		case "Reveal":
			// Never opens a user's native app during automation; the preceding
			// VersionFile call remains real and separately checked by the browser.
			value = true
		case "FixtureRewrite":
			callErr = os.WriteFile(first, []byte("intentionally edited first content"), 0600)
			value = true
		case "FixtureAddDrafts":
			list, e := s.ProjectWorks("root:品牌项目", "", 0)
			callErr = e
			if e == nil && len(list.Items) == 1 {
				for i := 0; i < 23; i++ {
					file := filepath.Join(project, fmt.Sprintf("草稿-%02d.txt", i))
					if callErr = os.WriteFile(file, []byte(fmt.Sprintf("draft %d", i)), 0600); callErr != nil {
						break
					}
					p, e := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", WorkID: list.Items[0].ID, Paths: []string{file}, Role: "draft"})
					if e != nil {
						callErr = e
						break
					}
					_, callErr = s.RegisterVersion(p.Token, fmt.Sprintf("fixture-draft-%d", i))
					if callErr != nil {
						break
					}
				}
			}
			value = true
		case "FixtureFinish":
			select {
			case done <- struct{}{}:
			default:
			}
			value = true
		default:
			if !allowed[req.Method] {
				http.Error(w, "method not allowed", 400)
				return
			}
			method := reflect.ValueOf(s).MethodByName(req.Method)
			if method.Type().NumIn() != len(req.Args) {
				http.Error(w, "wrong arguments", 400)
				return
			}
			args := make([]reflect.Value, len(req.Args))
			for i, raw := range req.Args {
				v := reflect.New(method.Type().In(i))
				if err := json.Unmarshal(raw, v.Interface()); err != nil {
					http.Error(w, "invalid arguments", 400)
					return
				}
				args[i] = v.Elem()
			}
			out := method.Call(args)
			if len(out) == 1 && method.Type().Out(0).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
				if !out[0].IsNil() {
					callErr = out[0].Interface().(error)
				}
			} else if len(out) > 0 {
				value = out[0].Interface()
			}
			if len(out) > 1 && !out[1].IsNil() {
				callErr = out[1].Interface().(error)
			}
		}
		result := map[string]any{"value": value}
		if callErr != nil {
			result["error"] = callErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	ready := map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "token": token, "key": "root:品牌项目", "project": project, "first": first, "second": second}
	if autoCase {
		ready["autoRoot"] = fixture
		ready["autoFile"] = autoFile
		ready["autoUnknown"] = unknownAutoCase
		ready["autoUnassigned"] = unassignedAutoCase
		ready["autoModel"] = modelAutoCase
		ready["customProfile"] = customAutoCase
		ready["autoProjectName"] = autoProjectName
		ready["existingProject"] = modelExistingCase
		if externalAutoCase && !discoveredAutoCase {
			ready["autoProjectKey"] = "place:browser-external"
		}
		ready["autoGemini"] = geminiAutoCase
		ready["autoClaude"] = claudeAutoCase
		ready["autoObserveOnly"] = observeAutoCase
		ready["autoObjection"] = objectionAutoCase
		ready["autoObjectionModel"] = objectionModelCase
		if sessionAutoCase {
			ready["autoSource"] = autoSource
		}
	}
	if sourcesCase {
		ready["sourcePath"] = sourcePath
		ready["sourceDirectory"] = directoryCase
	}
	if graphFixture != nil {
		ready["graph"] = graphFixture
	}
	b, _ := json.Marshal(ready)
	if err = os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_READY"), b, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(180 * time.Second):
		t.Fatal("browser acceptance timed out")
	}
	if autoCase {
		view, err := s.AutoOrganizeStatus()
		expected := 1
		if sessionAutoCase && !unassignedAutoCase {
			expected = 2
		}
		if observeAutoCase {
			expected = 0
			if view.SessionFileCount != 2 || view.Policy.Enabled {
				t.Fatal("observation status incorrect", view)
			}
		}
		if objectionAutoCase {
			expected = 1
		}
		if err != nil || len(view.Operations) != expected {
			t.Fatal(view, err)
		}
		for _, o := range view.Operations {
			if o.Status != "restored" {
				t.Fatal("operation was not restored", o)
			}
		}
		data, err := os.ReadFile(autoFile)
		if err != nil || string(data) != "fixture artwork" {
			t.Fatal("artifact not restored", err)
		}
		if sessionAutoCase && !objectionAutoCase {
			data, err = os.ReadFile(autoSecond)
			if err != nil || string(data) != "later artwork" {
				t.Fatal("later artifact not restored", err)
			}
		}
		if modelExistingCase {
			data, e := os.ReadFile(filepath.Join(autoProject, "原有文件.txt"))
			if e != nil || string(data) != "existing project content" {
				t.Fatal("existing project not preserved", e)
			}
		} else if _, err = os.Stat(filepath.Join(cfg.Root, "自动项目")); !os.IsNotExist(err) {
			t.Fatal("automatic project not restored", err)
		}
		if externalAutoCase {
			if _, err = os.Stat(filepath.Join(cfg.Root, "自动项目")); !os.IsNotExist(err) {
				t.Fatal("duplicate project created", err)
			}
		}
		if discoveredAutoCase {
			reg, e := registry.Load(cfg)
			if e != nil || reg.Owner(autoProject) == nil || !reg.Owner(autoProject).FromSession {
				t.Fatal("session project not registered", e)
			}
		}
		b, _ := json.MarshalIndent(map[string]any{"restored": !observeAutoCase, "observeOnly": observeAutoCase, "artifactUnchanged": true, "automaticProjectRemoved": !modelExistingCase && !observeAutoCase, "existingProjectPreserved": modelExistingCase}, "", "  ")
		if err = os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), b, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if graphFixture != nil {
		graphFixture.finish(t, s, second, versionCatalogPath(cfg))
		return
	}
	if sourcesCase {
		finishSourcesAcceptance(t, s, first, second, os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"))
		return
	}
	if memoryCase {
		state, err := s.ProjectMemory("root:品牌项目")
		if err != nil || state.Enabled || state.Revision != 4 || state.ObservationComplete {
			t.Fatal(state, err)
		}
		page, err := s.ProjectMemoryEvents("root:品牌项目", state.Revision, "")
		if err != nil {
			t.Fatal(err)
		}
		changed := false
		for _, event := range page.Items {
			if event.Kind == "content_changed" {
				changed = true
				if event.Actor != "unknown" {
					t.Fatal("invented actor", event)
				}
			}
		}
		if !changed {
			t.Fatal("missing real content change")
		}
		bytes, err := os.ReadFile(second)
		if err != nil || string(bytes) != "second content" {
			t.Fatal("unrelated file changed", err)
		}
		report := map[string]any{"revision": state.Revision, "paused": !state.Enabled, "coverageComplete": state.ObservationComplete, "realContentChange": changed, "unrelatedFileUnchanged": true, "projectID": state.ProjectID}
		b, _ = json.MarshalIndent(report, "", "  ")
		if err = os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), b, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	list, err := s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || list.Total != 1 || list.Items[0].Versions != 25 || list.Items[0].Usable || list.Items[0].UsableState != "changed" {
		t.Fatal(list, err)
	}
	data, err := s.VersionDetail("root:品牌项目", list.Items[0].CurrentUsableVersionID)
	if err != nil || !data.Item.RecordedUsable || data.Item.State != "changed" || len(data.Item.Provenance) != 2 {
		t.Fatal(data, err)
	}
	bytes, _ := os.ReadFile(second)
	if string(bytes) != "second content" {
		t.Fatal("unrelated content changed")
	}
	hash := sha256.Sum256(bytes)
	report := map[string]any{"works": list.Total, "versions": list.Items[0].Versions, "usableState": list.Items[0].UsableState, "provenance": len(data.Item.Provenance), "secondSHA256": hex.EncodeToString(hash[:]), "nativeReveal": "not executed; transport acknowledged after real VersionFile"}
	b, _ = json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
