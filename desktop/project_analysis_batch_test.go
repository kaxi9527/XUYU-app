package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func addBatchFiles(t *testing.T, s *ShelfService, project string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		versionFixtureFile(t, project, fmt.Sprintf("docs/file-%04d.txt", i), "DO_NOT_SEND_FILE_BODY")
	}
	state, err := s.ProjectMemory("root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveProjectMemory("root:品牌项目", state.Revision, "batch-scan", state.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
}
func previewWholeBatch(t *testing.T, s *ShelfService) AnalysisBatchPreview {
	t.Helper()
	scope, err := s.ProjectAnalysisScopes("root:品牌项目", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewProjectAnalysisBatch("root:品牌项目", scope.SHA, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}
func waitBatch(t *testing.T, s *ShelfService, id string, accept func(AnalysisBatchItem) bool) AnalysisBatchItem {
	t.Helper()
	until := time.Now().Add(30 * time.Second)
	var last AnalysisBatchItem
	var err error
	for time.Now().Before(until) {
		last, err = s.ProjectAnalysisBatchResult("root:品牌项目", id)
		if err == nil && accept(last) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("batch did not reach expected state: %+v error=%v", last, err)
	return last
}

func TestProjectAnalysisBatchWholeDirectoryQuotaAndAggregate(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		mu.Lock()
		for _, f := range p.Body.Files {
			if seen[f.ID] {
				t.Error("duplicate outgoing file", f.ID)
			}
			seen[f.ID] = true
		}
		mu.Unlock()
		data, _ := p.Bytes()
		if strings.Contains(string(data), "DO_NOT_SEND") || strings.Contains(string(data), ".env") {
			t.Error("private file content leaked")
		}
		writeAnalysisFixture(w, p)
	})
	addBatchFiles(t, s, project, 450)
	preview := previewWholeBatch(t, s)
	if preview.TotalFiles != 451 || preview.Required.Requests != 12 || preview.Spec.Quota.Requests != 12 || preview.Budget.Limit.Requests != 10 || calls.Load() != 0 {
		t.Fatal("preview totals", preview.TotalFiles, preview.Required, preview.Budget)
	}
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "whole", true, false, false); err == nil || calls.Load() != 0 {
		t.Fatal("quota silently expanded", err)
	}
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "whole", true, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "whole", true, true, false); err != nil {
		t.Fatal("replay", err)
	}
	item := waitBatch(t, s, "whole", func(b AnalysisBatchItem) bool { return b.Status == "completed" && b.NoteSaved })
	if item.Completed != 12 || item.TotalFiles != 451 || item.AnalyzedFiles != 451 || calls.Load() != 12 {
		t.Fatal("incomplete batch", item, calls.Load())
	}
	detail, err := s.ProjectAnalysisBatchDetail("root:品牌项目", "whole")
	if err != nil || len(detail.Summary.UnanalyzedIDs) != 0 || detail.Summary.AnalyzedFiles != 451 {
		t.Fatal("summary", err)
	}
	if err = s.RecordProjectAnalysisBatchNote("root:品牌项目", "whole"); err != nil {
		t.Fatal(err)
	}
	e, err := s.memoryEnvironment(context.Background(), "root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.Read(context.Background())
	if err != nil || len(v.State.Notes) != 1 || v.State.Notes[0].Kind != "agent_proposal" {
		t.Fatal("aggregate memory", err)
	}
	l, err := e.store.Analysis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	budget, err := l.Budget(time.Now())
	if err != nil || budget.Reserved.Requests != 12 || budget.Limit.Requests != 12 {
		t.Fatal("shared quota", budget, err)
	}
	reopened := &ShelfService{}
	t.Cleanup(reopened.closeModels)
	if _, err = reopened.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "whole", true, true, false); err != nil {
		t.Fatal("restart receipt", err)
	}
	if calls.Load() != 12 {
		t.Fatal("restart sent model request")
	}
}

func TestProjectAnalysisBatchPauseRestartResumeOnlyQueued(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var first atomic.Bool
	modelDir := filepath.Join(t.TempDir(), "models")
	s, project, calls := analysisServiceFixtureAt(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		if first.CompareAndSwap(false, true) {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeAnalysisFixture(w, p)
	}, modelDir)
	addBatchFiles(t, s, project, 80)
	preview := previewWholeBatch(t, s)
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "pause", true, false, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not dispatched")
	}
	if _, err := s.PauseProjectAnalysisBatch("root:品牌项目", "pause"); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	paused := waitBatch(t, s, "pause", func(b AnalysisBatchItem) bool { return b.Status == "paused" && b.Completed == 1 && b.CanResume })
	if calls.Load() != 1 {
		t.Fatal("pause sent later group")
	}
	e, _ := s.memoryEnvironment(context.Background(), "root:品牌项目")
	v, err := e.store.Read(context.Background())
	if err != nil || len(v.State.Notes) != 0 {
		t.Fatal("partial note invalidated remaining context", err)
	}
	s.closeModels()
	restarted := &ShelfService{}
	t.Cleanup(restarted.closeModels)
	client := modelhttp.NewClient()
	store, err := models.NewStore(modelDir, nil, &accountModelAdapter{direct: client})
	if err != nil {
		t.Fatal(err)
	}
	restarted.connections.store, restarted.connections.client, restarted.connections.protocolReady = store, client, true
	if _, err = store.Begin(context.Background()); err == nil {
		t.Fatal("restart armed normal requests")
	}
	if _, err = restarted.ProjectAnalysisBatchResult("root:品牌项目", "pause"); err != nil || calls.Load() != 1 {
		t.Fatal("readback resumed model", err)
	}
	if _, err = restarted.ResumeProjectAnalysisBatch("root:品牌项目", "pause", paused.SpecSHA, false); err == nil {
		t.Fatal("resume without explicit consent")
	}
	if _, err = restarted.ResumeProjectAnalysisBatch("root:品牌项目", "pause", paused.SpecSHA, true); err != nil {
		t.Fatal("resume", err)
	}
	finished := waitBatch(t, restarted, "pause", func(b AnalysisBatchItem) bool { return b.Status == "completed" && b.NoteSaved })
	if calls.Load() != 3 || finished.AnalyzedFiles != 81 {
		t.Fatal("resent completed group", calls.Load(), finished)
	}
	if _, err = store.Begin(context.Background()); err == nil {
		t.Fatal("batch resume armed unrelated model use")
	}
}

func TestProjectAnalysisBatchStopsAfterUnreliablePart(t *testing.T) {
	var seen atomic.Int32
	s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		if seen.Add(1) == 2 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"not a valid project suggestion"}}]}`))
			return
		}
		writeAnalysisFixture(w, p)
	})
	addBatchFiles(t, s, project, 80)
	preview := previewWholeBatch(t, s)
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "partial", true, false, false); err != nil {
		t.Fatal(err)
	}
	item := waitBatch(t, s, "partial", func(b AnalysisBatchItem) bool { return b.Status == "stopped" && b.NoteSaved })
	if calls.Load() != 2 || item.Completed != 1 || item.AnalyzedFiles != 40 {
		t.Fatal("continued after invalid response", item, calls.Load())
	}
	detail, err := s.ProjectAnalysisBatchDetail("root:品牌项目", "partial")
	if err != nil || len(detail.Summary.UnanalyzedIDs) != 41 || len(detail.Summary.UncertainIDs) != 0 {
		t.Fatal("unprocessed files misrepresented", err)
	}
	if _, err = s.ResumeProjectAnalysisBatch("root:品牌项目", "partial", item.SpecSHA, true); err == nil || calls.Load() != 2 {
		t.Fatal("resumed failed request")
	}
}

func TestProjectAnalysisBatchPausedStopRecordsOneAggregate(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeAnalysisFixture(w, p)
	})
	addBatchFiles(t, s, project, 80)
	p := previewWholeBatch(t, s)
	if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", p.Token, "paused-stop", true, false, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not dispatched")
	}
	if _, err := s.PauseProjectAnalysisBatch("root:品牌项目", "paused-stop"); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	waitBatch(t, s, "paused-stop", func(b AnalysisBatchItem) bool { return b.Status == "paused" && b.Completed == 1 && b.CanResume })
	for i := 0; i < 2; i++ {
		b, err := s.StopProjectAnalysisBatch("root:品牌项目", "paused-stop")
		if err != nil || b.Status != "stopped" || !b.NoteSaved {
			t.Fatal("terminal aggregate not recorded", b, err)
		}
		if _, err = s.ProjectAnalysisBatchDetail("root:品牌项目", b.ID); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := s.memoryEnvironment(context.Background(), "root:品牌项目")
	v, err := e.store.Read(context.Background())
	if err != nil || len(v.State.Notes) != 1 || calls.Load() != 1 {
		t.Fatal("duplicate note or model dispatch", err, calls.Load())
	}
}

func TestProjectAnalysisBatchPausedResumeRejectsChangedInputs(t *testing.T) {
	for _, kind := range []string{"model", "template"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				writeAnalysisFixture(w, p)
			})
			addBatchFiles(t, s, project, 80)
			preview := previewWholeBatch(t, s)
			if _, err := s.ConfirmProjectAnalysisBatch("root:品牌项目", preview.Token, "changed", true, false, false); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("no first request")
			}
			if _, err := s.PauseProjectAnalysisBatch("root:品牌项目", "changed"); err != nil {
				t.Fatal(err)
			}
			once.Do(func() { close(release) })
			item := waitBatch(t, s, "changed", func(b AnalysisBatchItem) bool { return b.Status == "paused" && b.CanResume })
			if kind == "model" {
				state, err := s.connections.store.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.connections.store.SetMode(context.Background(), state.Revision, "changed-mode", "tool", ""); err != nil {
					t.Fatal(err)
				}
			} else {
				path := filepath.Join(project, worktype.ProfileFile)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.Replace(string(data), "设计", "更新设计", 1))
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ResumeProjectAnalysisBatch("root:品牌项目", "changed", item.SpecSHA, true); err == nil || calls.Load() != 1 {
				t.Fatal("resumed changed inputs", kind, err)
			}
		})
	}
}
