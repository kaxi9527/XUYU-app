package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func analysisServiceFixture(t *testing.T, handler func(http.ResponseWriter, *http.Request, projectcontext.Packet)) (*ShelfService, string, *atomic.Int32) {
	return analysisServiceFixtureAt(t, handler, filepath.Join(t.TempDir(), "models"))
}
func analysisServiceFixtureAt(t *testing.T, handler func(http.ResponseWriter, *http.Request, projectcontext.Packet), modelDir string) (*ShelfService, string, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Messages) == 0 {
			t.Error("invalid protocol body")
			w.WriteHeader(400)
			return
		}
		last := in.Messages[len(in.Messages)-1].Content
		w.Header().Set("Content-Type", "application/json")
		if last == "Reply with the word OK." {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`))
			return
		}
		packet, err := projectcontext.Restore([]byte(last))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		if handler != nil {
			handler(w, r, packet)
			return
		}
		writeAnalysisFixture(w, packet)
	}))
	t.Cleanup(server.Close)
	s, _, project, _ := versionFixture(t)
	t.Cleanup(s.closeModels)
	profile := worktype.Profile{Name: "设计", Folders: []string{"参考资料", "源文件"}, Roles: map[string]string{"source": "源文件"}, Rules: []worktype.Rule{{Kind: "extension", Value: "txt", Folder: "参考资料"}}}
	b, _ := json.Marshal(profile)
	if err := os.WriteFile(filepath.Join(project, worktype.ProfileFile), b, 0600); err != nil {
		t.Fatal(err)
	}
	versionFixtureFile(t, project, "说明.txt", "DO_NOT_SEND_FILE_CONTENT")
	versionFixtureFile(t, project, ".env", "DO_NOT_SEND_SECRET")
	status, err := s.ProjectMemory("root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectMemoryTracking("root:品牌项目", true, 0, "enable", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveProjectMemory("root:品牌项目", 1, "scan", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	client := modelhttp.NewClient()
	store, err := models.NewStore(modelDir, nil, &accountModelAdapter{direct: client})
	if err != nil {
		t.Fatal(err)
	}
	s.connections.store, s.connections.client, s.connections.protocolReady = store, client, true
	p, err := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "本机夹具", Protocol: models.Chat, BaseURL: server.URL + "/v1", ModelID: "fixture", ModelName: "Fixture"})
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
	return s, project, calls
}
func writeAnalysisFixture(w http.ResponseWriter, packet projectcontext.Packet) {
	groups := map[string][]string{}
	for _, f := range packet.Body.Files {
		if f.Kind == "file" && f.Status == "present" {
			folder := f.RuleFolderID
			if folder == "" {
				folder = packet.Body.Folders[0].ID
			}
			groups[folder] = append(groups[folder], f.ID)
		}
	}
	a := projectcontext.Advice{ProjectID: packet.Body.ProjectID, ContextSHA: packet.SHA256, MemoryRevision: packet.Body.MemoryRevision, Groups: []projectcontext.Group{}, UncertainIDs: []string{}}
	for folder, ids := range groups {
		a.Groups = append(a.Groups, projectcontext.Group{FolderID: folder, FileIDs: ids, Reason: "依据工种模板归入参考资料"})
	}
	b, _ := json.Marshal(a)
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(b)}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 40, "total_tokens": 140}})
}
func waitAnalysis(t *testing.T, s *ShelfService, id string, note bool) projectmemory.AnalysisRequest {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.ProjectAnalysisResult("root:品牌项目", id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "running" && (!note || r.NoteSaved) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("analysis did not reach a recorded outcome")
	return projectmemory.AnalysisRequest{}
}

func TestProjectAnalysisHTTPConsentReplayRestartAndMemory(t *testing.T) {
	s, project, calls := analysisServiceFixture(t, nil)
	preview, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || preview.Budget.Reserved.Requests != 0 || strings.Contains(preview.Input, "DO_NOT_SEND") || strings.Contains(preview.Input, project) || strings.Contains(preview.Input, `.env`) {
		t.Fatal("preview sent or exposed excluded content")
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "analysis-one", false, false); err == nil {
		t.Fatal("missing explicit acceptance")
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "analysis-one", true, true); err == nil {
		t.Fatal("unsupported hard cost limit silently ignored")
	}
	if calls.Load() != 0 {
		t.Fatal("sent before consent")
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "analysis-one", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "analysis-one", true, false); err != nil {
		t.Fatal("repeat did not return receipt", err)
	}
	r := waitAnalysis(t, s, "analysis-one", true)
	if r.Status != "succeeded" || calls.Load() != 1 || r.Usage.TotalTokens == nil || *r.Usage.TotalTokens != 140 {
		t.Fatal("not exactly one successful request")
	}
	e, err := s.memoryEnvironment(context.Background(), "root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.State.Notes) != 1 || v.State.Notes[0].Kind != "agent_proposal" {
		t.Fatal("proposal missing or became user decision")
	}
	if err = s.RecordProjectAnalysisNote("root:品牌项目", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted := &ShelfService{}
	t.Cleanup(restarted.closeModels)
	if _, err = restarted.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "analysis-one", true, false); err != nil {
		t.Fatal("restart did not recover receipt", err)
	}
	history, err := restarted.ProjectAnalysisHistory("root:品牌项目", 0)
	if err != nil || history.Total != 1 || history.Budget.Reserved.Requests != 1 || calls.Load() != 1 {
		t.Fatal("restart lost budget or retried", err)
	}
	b, err := os.ReadFile(filepath.Join(project, "说明.txt"))
	if err != nil || string(b) != "DO_NOT_SEND_FILE_CONTENT" {
		t.Fatal("source file changed")
	}
	if _, err = os.Stat(filepath.Join(project, "参考资料")); !os.IsNotExist(err) {
		t.Fatal("model executed folder action")
	}
}

func TestProjectAnalysisPreviewRejectsChangesExpiryAndWriteFailure(t *testing.T) {
	for _, kind := range []string{"template", "mode", "expired", "released", "record_write", "wrong_project"} {
		t.Run(kind, func(t *testing.T) {
			s, project, calls := analysisServiceFixture(t, nil)
			if kind == "expired" {
				s.analyses.ttl = 10 * time.Millisecond
			}
			p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
			if err != nil {
				t.Fatal(err)
			}
			key := "root:品牌项目"
			switch kind {
			case "template":
				b, _ := json.Marshal(worktype.Profile{Name: "新规则", Folders: []string{"其他"}})
				if err = os.WriteFile(filepath.Join(project, worktype.ProfileFile), b, 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				state, _ := s.connections.store.Read(context.Background())
				if _, err = s.connections.store.SetMode(context.Background(), state.Revision, "stop", "tool", ""); err != nil {
					t.Fatal(err)
				}
			case "expired":
				time.Sleep(30 * time.Millisecond)
			case "released":
				s.ReleaseProjectAnalysis(p.Token)
			case "record_write":
				e, err := s.memoryEnvironment(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				env, _ := s.env()
				path := filepath.Join(env.Config.Root, env.Config.Names.Control, "项目记忆", e.project.ID, "analysis.json")
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "wrong_project":
				key = "root:another-project"
			}
			if _, err = s.ConfirmProjectAnalysis(key, p.Token, "blocked", true, false); err == nil {
				t.Fatal("unsafe confirmation accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("request sent despite preflight failure")
			}
		})
	}
}

func TestProjectAnalysisCancellationAndLateOutput(t *testing.T) {
	for _, kind := range []string{"cancel", "pause", "mode", "close"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			s, _, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
				close(entered)
				<-r.Context().Done()
				writeAnalysisFixture(w, p)
			})
			p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "stop-me", true, false); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("not sent")
			}
			switch kind {
			case "cancel":
				s.CancelProjectAnalysis("root:品牌项目", "stop-me")
			case "pause":
				state, _ := s.ProjectMemory("root:品牌项目")
				if _, err = s.SetProjectMemoryTracking("root:品牌项目", false, state.Revision, "pause", state.CurrentScopeSHA); err != nil {
					t.Fatal(err)
				}
			case "mode":
				state, _ := s.connections.store.Read(context.Background())
				if _, err = s.connections.store.SetMode(context.Background(), state.Revision, "stop", "tool", ""); err != nil {
					t.Fatal(err)
				}
			case "close":
				s.closeModels()
			}
			r := waitAnalysis(t, s, "stop-me", false)
			if r.Status == "succeeded" || len(r.Advice) != 0 || r.NoteSaved || calls.Load() != 1 {
				t.Fatal("cancelled or late output published")
			}
			history, err := s.ProjectAnalysisHistory("root:品牌项目", 0)
			if err != nil || history.Budget.Reserved.Requests != 1 {
				t.Fatal("cancel refunded possibly consumed quota", err)
			}
		})
	}
}

func TestProjectAnalysisRejectsUnreliableAdvice(t *testing.T) {
	s, _, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{"command":"delete files"}`}}}})
	})
	p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "invalid-advice", true, false); err != nil {
		t.Fatal(err)
	}
	r := waitAnalysis(t, s, "invalid-advice", false)
	if r.Status != "failed" || r.Problem != "unreliable_advice" || len(r.Advice) != 0 || calls.Load() != 1 {
		t.Fatal("unsafe advice retained")
	}
}
