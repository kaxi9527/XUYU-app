package main

import (
	"encoding/json"
	"fmt"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in, finite real-service browser transport. State and secrets are synthetic.
func TestModelCenterBrowserAcceptance(t *testing.T) {
	fixture := os.Getenv("WORKSHELF_MODEL_UI_FIXTURE")
	if fixture == "" {
		t.Skip("isolated browser fixture only")
	}
	if !filepath.IsAbs(fixture) || os.Getenv("WORKSHELF_CONFIG") != filepath.Join(fixture, "config.json") {
		t.Fatal("isolated config required")
	}
	token := os.Getenv("WORKSHELF_ACCEPTANCE_TOKEN")
	if len(token) < 24 {
		t.Fatal("fixture token required")
	}
	cfg := config.Default(fixture)
	cfg.ScanPaths = []string{fixture}
	cfg.GitStatus = false
	writeVersionConfig(t, filepath.Join(fixture, "config.json"), cfg)
	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer fixture-only-secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"fixture-probe"},{"id":"fixture-second"}]}`)
		case "/v1/chat/completions":
			probes.Add(1)
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	client := modelhttp.NewClient()
	defer client.Close()
	s, vault, _ := modelTestService(t, client)
	s.connections.accounts.binary = os.Getenv("WORKSHELF_TEST_MODEL_BINARY")
	s.connections.accounts.directory = filepath.Join(fixture, "accounts")
	defer s.connections.accounts.close()
	allowed := map[string]bool{}
	for _, m := range []string{"CreateModelDraft", "DiscardModelDraft", "SelectModelDraft", "ListModelDraft", "VerifyModelDraft", "SaveModelDraft", "CancelModelDraftRequest"} {
		allowed[m] = true
	}
	for _, m := range []string{"Info", "OrganizeContext", "CreateProject", "Project", "InferWorkTypes", "ModelConnections", "ModelConnectionPresets", "ModelAccounts", "PreviewModelConnection", "SaveModelConnection", "DiscardModelConnection", "ModelOperation", "VerifyModelConnection", "ListConnectionModels", "SetWorkShelfMode", "DeleteModelProvider"} {
		allowed[m] = true
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9258")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-WorkShelf-Acceptance")
		if r.Method == "OPTIONS" {
			return
		}
		if r.Method != "POST" || r.Header.Get("X-WorkShelf-Acceptance") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var value any
		var callErr error
		switch req.Method {
		case "FixtureDraftCount":
			s.connections.mu.Lock()
			value = len(s.connections.drafts)
			s.connections.mu.Unlock()
		case "FixtureDraftStats":
			state, err := s.ModelConnections()
			callErr = err
			vault.mu.Lock()
			value = map[string]any{"revision": state.State.Revision, "providers": len(state.State.Providers), "mode": state.State.Settings.Mode, "credentialCreates": vault.creates}
			vault.mu.Unlock()
		case "FixtureFinish":
			select {
			case done <- struct{}{}:
			default:
			}
			value = true
		case "InstallStatus":
			value = map[string]any{"needsInstall": false, "inApplications": true}
		default:
			if !allowed[req.Method] {
				callErr = fmt.Errorf("unsupported fixture method: %s", req.Method)
				break
			}
			method := reflect.ValueOf(s).MethodByName(req.Method)
			typ := method.Type()
			if typ.NumIn() != len(req.Args) {
				callErr = fmt.Errorf("argument count")
				break
			}
			args := []reflect.Value{}
			for i, a := range req.Args {
				arg := reflect.New(typ.In(i))
				if e := json.Unmarshal(a, arg.Interface()); e != nil {
					callErr = e
					break
				}
				args = append(args, arg.Elem())
			}
			if callErr != nil {
				break
			}
			out := method.Call(args)
			for _, v := range out {
				if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
					if !v.IsNil() {
						callErr = v.Interface().(error)
					}
				} else {
					value = v.Interface()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if callErr != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": callErr.Error()})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		}
	}), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	ready := map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "token": token, "upstream": upstream.URL + "/v1"}
	data, _ := json.Marshal(ready)
	if err := os.WriteFile(filepath.Join(fixture, "ready.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		t.Fatal("browser timed out")
	}
	project := filepath.Join(cfg.Root, "真实新项目")
	profile, err := worktype.ReadProfile(filepath.Join(project, worktype.ProfileFile))
	if err != nil || len(profile.Folders) != 3 {
		t.Fatal("real project profile", err)
	}
	for _, name := range profile.Folders {
		fi, err := os.Stat(filepath.Join(project, name))
		if err != nil || !fi.IsDir() {
			t.Fatal("project folder missing", name)
		}
	}
	state, err := s.ModelConnections()
	if err != nil || state.State.Revision != 1 || state.State.Settings.Mode != "agent" || len(state.State.Models) != 1 || state.State.Models[0].Verification == nil || probes.Load() != 1 {
		t.Fatal("real model state or probe count", err, probes.Load())
	}
	report := map[string]any{"createdProject": true, "folders": profile.Folders, "apiSaved": true, "apiVerified": true, "activeMode": state.State.Settings.Mode, "probeRequests": probes.Load(), "credentials": "synthetic in-memory vault; no real keychain access", "subscription": "empty owned runtime read; real authorization not attempted"}
	data, _ = json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
