package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
)

// Explicit opt-in local bridge. The model, files and credentials are all fixtures.
func TestProjectAnalysisBrowserAcceptance(t *testing.T) {
	dir := os.Getenv("WORKSHELF_ANALYSIS_BROWSER")
	if dir == "" {
		t.Skip("isolated browser fixture only")
	}
	token := os.Getenv("WORKSHELF_ACCEPTANCE_TOKEN")
	if !filepath.IsAbs(dir) || len(token) < 24 {
		t.Fatal("isolated directory and fixture token required")
	}
	large := os.Getenv("WORKSHELF_ANALYSIS_SCOPE_LARGE") == "1"
	batchMode := os.Getenv("WORKSHELF_ANALYSIS_BATCH_BROWSER") == "1"
	archiveMode := os.Getenv("WORKSHELF_ANALYSIS_ARCHIVE_BROWSER") == "1"
	placementMode := os.Getenv("WORKSHELF_PLACEMENT_BROWSER") == "1"
	var reply func(http.ResponseWriter, *http.Request, projectcontext.Packet)
	if large && !batchMode {
		reply = func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
			if len(p.Body.Files) != 40 {
				t.Errorf("expected selected 40 files, got %d", len(p.Body.Files))
			}
			for _, f := range p.Body.Files {
				if filepath.Dir(f.Position.Relative) != "docs" {
					t.Errorf("unexpected selected file: %s", f.Position.Relative)
				}
			}
			writeAnalysisFixture(w, p)
		}
	}
	s, project, calls := analysisServiceFixture(t, reply)
	defer s.closePlacements()
	key := "root:品牌项目"
	oldToken, oldID := "", ""
	if archiveMode {
		preview, rows := seedOldAnalysisReceipts(t, s, 100)
		oldToken, oldID = preview.Token, rows[0].ID
	}
	if large || batchMode {
		docs, assets := 120, 80
		if batchMode {
			docs, assets = 450, 0
		}
		for i := 0; i < docs; i++ {
			versionFixtureFile(t, project, fmt.Sprintf("docs/file-%03d.txt", i), "DO_NOT_SEND")
		}
		for i := 0; i < assets; i++ {
			versionFixtureFile(t, project, fmt.Sprintf("assets/image-%03d.png", i), "DO_NOT_SEND")
		}
		state, err := s.ProjectMemory(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ObserveProjectMemory(key, state.Revision, "browser-scope-scan", state.CurrentScopeSHA); err != nil {
			t.Fatal(err)
		}
	}
	allowed := map[string]bool{}
	for _, m := range []string{"Info", "ProjectAnalysisScopes", "PreviewProjectAnalysisGroup", "PreviewProjectAnalysis", "ConfirmProjectAnalysis", "ReleaseProjectAnalysis", "CancelProjectAnalysis", "ProjectAnalysisResult", "ProjectAnalysisHistory", "RecordProjectAnalysisNote", "ProjectMemory", "ProjectMemoryEvents"} {
		allowed[m] = true
	}
	for _, m := range []string{"PreviewProjectAnalysisBatch", "ReleaseProjectAnalysisBatch", "ConfirmProjectAnalysisBatch", "PauseProjectAnalysisBatch", "ResumeProjectAnalysisBatch", "StopProjectAnalysisBatch", "ProjectAnalysisBatchResult", "ProjectAnalysisBatchDetail", "ProjectAnalysisBatchHistory", "RecordProjectAnalysisBatchNote"} {
		allowed[m] = true
	}
	allowed["ProjectAnalysisHistoryPage"] = true
	allowed["ProjectAnalysisBatchHistoryPage"] = true
	if placementMode {
		for _, m := range []string{"PreviewProjectPlacement", "ReleaseProjectPlacementPreview", "ConfirmProjectPlacement", "ExecuteProjectPlacement", "ProjectPlacementOperation", "StopProjectPlacement", "PreviewProjectPlacementUndo", "ConfirmProjectPlacementUndo", "ExecuteProjectPlacementUndo", "ProjectMemoryGraph"} {
			allowed[m] = true
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9258")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-WorkShelf-Acceptance")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/call" || r.Header.Get("X-WorkShelf-Acceptance") != token {
			http.Error(w, "fixture only", 403)
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
		case "FixtureStats":
			h, e := s.ProjectAnalysisHistory(key, 0)
			callErr = e
			batches, batchErr := s.ProjectAnalysisBatchHistory(key, 0)
			if callErr == nil {
				callErr = batchErr
			}
			value = map[string]any{"calls": calls.Load(), "history": h, "batches": batches}
			if placementMode {
				source, sourceErr := os.ReadFile(filepath.Join(project, "说明.txt"))
				target, targetErr := os.ReadFile(filepath.Join(project, "参考资料", "说明.txt"))
				value = map[string]any{"calls": calls.Load(), "history": h, "sourceExists": sourceErr == nil, "targetExists": targetErr == nil, "sourceContent": string(source), "targetContent": string(target)}
			}
		case "FixtureDone":
			select {
			case done <- struct{}{}:
			default:
			}
			value = true
		default:
			if !allowed[req.Method] {
				http.Error(w, "method not allowed", 403)
				return
			}
			method := reflect.ValueOf(s).MethodByName(req.Method)
			if !method.IsValid() || method.Type().NumIn() != len(req.Args) {
				http.Error(w, "bad method", 400)
				return
			}
			args := make([]reflect.Value, len(req.Args))
			for i, raw := range req.Args {
				v := reflect.New(method.Type().In(i))
				if json.Unmarshal(raw, v.Interface()) != nil {
					http.Error(w, "bad argument", 400)
					return
				}
				args[i] = v.Elem()
			}
			values := method.Call(args)
			for _, v := range values {
				if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
					if !v.IsNil() {
						callErr = v.Interface().(error)
					}
				} else {
					value = v.Interface()
				}
			}
		}
		response := map[string]any{"value": value}
		if callErr != nil {
			response["error"] = callErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	ready, _ := json.Marshal(map[string]any{"port": listener.Addr().(*net.TCPAddr).Port, "token": token, "key": key, "oldToken": oldToken, "oldID": oldID})
	if err = os.WriteFile(filepath.Join(dir, "ready.json"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("browser acceptance timed out")
	}
	expectedCalls := int32(1)
	if batchMode {
		expectedCalls = 12
	}
	if calls.Load() != expectedCalls {
		t.Fatalf("expected %d actual fixture generations; got %d", expectedCalls, calls.Load())
	}
}
