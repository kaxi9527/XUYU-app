package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/organize"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// Opt-in real browser/service acceptance. The runner owns and removes the whole
// fixture only AFTER this test process exits, including asynchronous refreshes.
// There is no HTTP transport in production builds.
func TestOrganizerBrowserAcceptance(t *testing.T) {
	fixture := os.Getenv("WORKSHELF_ACCEPTANCE_FIXTURE")
	if fixture == "" {
		t.Skip("run the isolated browser acceptance runner")
	}
	fixture, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(userHome, fixture); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatal("fixture must be inside the permitted user directory")
	}
	if filepath.Clean(os.Getenv("WORKSHELF_CONFIG")) != filepath.Join(fixture, "config.json") {
		t.Fatal("acceptance requires its own explicit config")
	}
	token := os.Getenv("WORKSHELF_ACCEPTANCE_TOKEN")
	if len(token) < 24 {
		t.Fatal("missing test authorization token")
	}
	cfg := config.Default(fixture)
	cfg.ScanPaths = []string{filepath.Join(fixture, "Desktop"), filepath.Join(fixture, "Downloads")}
	cfg.GitStatus = false
	cfg.TimeoutSeconds = 10
	cfg.MaxFiles = 200
	cfg.MaxDirs = 100
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project := "验收品牌改版"
	main := filepath.Join(fixture, "Desktop", project)
	inputs := map[string]string{
		filepath.Join(main, "需求说明.md"):                         "客户需求：统一首页。\n",
		filepath.Join(main, "首页.fig"):                          "fixture fig source\n",
		filepath.Join(main, "参考图.png"):                         "fixture image\n",
		filepath.Join(main, "README.md"):                       "# " + project + "\n设计项目说明。\n",
		filepath.Join(main, ".codex", "config.toml"):           "fixture_config=true\n",
		filepath.Join(fixture, "Downloads", project+"-定稿.png"): "fixture final bytes\n",
	}
	mergeAcceptance := os.Getenv("WORKSHELF_ACCEPTANCE_MERGE") == "1"
	if mergeAcceptance {
		other := filepath.Join(fixture, "Downloads", project)
		for path, content := range map[string]string{
			filepath.Join(main, "素材", "logo.png"):             "same logo",
			filepath.Join(other, "素材", "logo.png"):            "same logo",
			filepath.Join(main, "素材", "cover.png"):            "cover first",
			filepath.Join(other, "素材", "cover.png"):           "cover second",
			filepath.Join(main, "素材", "poster-v1.png"):        "poster version one",
			filepath.Join(other, "素材", "poster-v2.png"):       "poster version two",
			filepath.Join(other, "素材", "细节", "image.png"):     "nested image",
			filepath.Join(cfg.Root, project, "需求", "需求说明.md"): inputs[filepath.Join(main, "需求说明.md")],
		} {
			inputs[path] = content
		}
	}
	for p, s := range inputs {
		write(p, s)
	}
	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	write(filepath.Join(fixture, "config.json"), string(b))
	svc := &ShelfService{}
	if _, err := svc.RegistryRefresh(); err != nil {
		t.Fatal(err)
	}
	ctx, err := svc.OrganizeContext("")
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, choice := range ctx.Projects {
		if choice.Name == project {
			key = choice.Key
		}
	}
	if key == "" {
		t.Fatalf("scan did not discover project: %+v", ctx.Projects)
	}
	inside := func(path string) bool {
		rel, e := filepath.Rel(fixture, filepath.Clean(path))
		return filepath.IsAbs(path) && e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	decode := func(args []json.RawMessage, index int, out any) error {
		if index >= len(args) {
			return fmt.Errorf("missing argument %d", index)
		}
		return json.Unmarshal(args[index], out)
	}
	var mu sync.Mutex
	var calls []map[string]any
	var applied *home.Summary
	var undone *home.UndoResult
	finished := make(chan struct{}, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://127.0.0.1:9258" {
			http.Error(w, "origin refused", 403)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9258")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-WorkShelf-Acceptance")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("X-WorkShelf-Acceptance") != token {
			http.Error(w, "test authorization required", 403)
			return
		}
		if r.URL.Path == "/finish" {
			w.WriteHeader(204)
			select {
			case finished <- struct{}{}:
			default:
			}
			return
		}
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var value any
		var callErr error
		switch req.Method {
		case "Info":
			value, callErr = svc.Info()
		case "OrganizeContext":
			var k string
			callErr = decode(req.Args, 0, &k)
			if callErr == nil {
				value, callErr = svc.OrganizeContext(k)
			}
		case "RegistryRefresh":
			value, callErr = svc.RegistryRefresh()
		case "RegistryStatus":
			value, callErr = svc.RegistryStatus()
		case "OrganizeSuggest":
			var paths []string
			callErr = decode(req.Args, 0, &paths)
			for _, p := range paths {
				if !inside(p) {
					callErr = fmt.Errorf("source outside fixture")
				}
			}
			if callErr == nil {
				value, callErr = svc.OrganizeSuggest(paths)
			}
		case "OrganizeBrowse":
			var p string
			var hidden bool
			callErr = decode(req.Args, 0, &p)
			if callErr == nil {
				callErr = decode(req.Args, 1, &hidden)
			}
			if !inside(p) {
				callErr = fmt.Errorf("browse outside fixture")
			}
			if callErr == nil {
				value, callErr = svc.OrganizeBrowse(p, hidden)
			}
		case "OrganizePreview":
			var input organize.Request
			callErr = decode(req.Args, 0, &input)
			for _, p := range input.Sources {
				if !inside(p) {
					callErr = fmt.Errorf("source outside fixture")
				}
			}
			if callErr == nil {
				value, callErr = svc.OrganizePreview(input)
			}
		case "OrganizeSaveTemplate":
			var profile worktype.Profile
			callErr = decode(req.Args, 0, &profile)
			if callErr == nil {
				callErr = svc.OrganizeSaveTemplate(profile)
			}
		case "OrganizeApply":
			var planToken string
			var ids []string
			callErr = decode(req.Args, 0, &planToken)
			if callErr == nil {
				callErr = decode(req.Args, 1, &ids)
			}
			if callErr == nil {
				applied, callErr = svc.OrganizeApply(planToken, ids)
				value = applied
			}
		case "UndoHome":
			var path string
			callErr = decode(req.Args, 0, &path)
			if !inside(path) {
				callErr = fmt.Errorf("undo outside fixture")
			}
			if callErr == nil {
				u, e := svc.UndoHome(path)
				undone = &u
				value = u
				callErr = e
			}
		default:
			callErr = fmt.Errorf("method not enabled in acceptance: %s", req.Method)
		}
		receipt := map[string]any{"method": req.Method, "args": req.Args, "result": value}
		payload := map[string]any{"value": value}
		if callErr != nil {
			receipt["error"] = callErr.Error()
			payload["error"] = callErr.Error()
		}
		calls = append(calls, receipt)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	var inputPaths []string
	for path := range inputs {
		inputPaths = append(inputPaths, path)
	}
	ready := map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "token": token, "key": key, "project": project, "main": main, "fixture": fixture, "inputs": inputPaths}
	b, _ = json.Marshal(ready)
	write(filepath.Join(fixture, "ready.json"), string(b))
	select {
	case <-finished:
	case <-time.After(150 * time.Second):
		t.Error("browser did not complete")
	}
	mu.Lock()
	defer mu.Unlock()
	if applied == nil || applied.Moved < 4 || applied.Failed != 0 {
		t.Errorf("organization incomplete: %+v", applied)
	}
	if mergeAcceptance && (applied == nil || applied.Duplicates < 2) {
		t.Errorf("merge duplicates were not verified: %+v", applied)
	}
	if undone == nil || applied == nil || undone.Restored != applied.Moved || undone.Failed != 0 {
		t.Errorf("undo incomplete: %+v", undone)
	}
	hashes := map[string]string{}
	for path, original := range inputs {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != original {
			t.Errorf("original not restored: %s: %v", filepath.Base(path), err)
		}
		sum := sha256.Sum256(got)
		rel, _ := filepath.Rel(fixture, path)
		hashes[rel] = hex.EncodeToString(sum[:])
	}
	if applied != nil {
		for _, item := range applied.Items {
			if item.Action == "搬走" {
				if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
					t.Errorf("moved target still present after undo: %s", item.Target)
				}
			}
		}
	}
	report := map[string]any{"calls": calls, "restoredSHA256": hashes, "moved": applied, "undo": undone, "passed": !t.Failed()}
	b, _ = json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
