package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

// Historical failures are seeded through the real ledger API, across expired
// daily windows. This does not pretend that fixture setup made model requests.
func seedOldAnalysisReceipts(t *testing.T, s *ShelfService, n int) (AnalysisPreview, []projectmemory.AnalysisRequest) {
	t.Helper()
	p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.analyses.mu.Lock()
	spec := s.analyses.previews[p.Token].spec
	s.analyses.mu.Unlock()
	spec.UnknownCostAccepted = true
	e, err := s.memoryEnvironment(context.Background(), "root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	rows := []projectmemory.AnalysisRequest{}
	now := time.Now()
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(12-i/10) * 24 * time.Hour)
		r, fresh, err := e.store.ReserveAnalysis(context.Background(), fmt.Sprintf("historical-%03d", i), spec, at)
		if err != nil || !fresh {
			t.Fatal(i, err)
		}
		r, err = e.store.FinishAnalysis(context.Background(), r.ID, r.SpecSHA, "failed", "request_failed", nil, models.Usage{}, at.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	s.ReleaseProjectAnalysis(p.Token)
	return p, rows
}

func TestProjectAnalysisArchiveCapacityAndReadOnlyReplay(t *testing.T) {
	s, _, calls := analysisServiceFixture(t, nil)
	oldPreview, old := seedOldAnalysisReceipts(t, s, 100)
	h, err := s.ProjectAnalysisHistoryPage("root:品牌项目", 0, 0)
	if err != nil || h.Total != 100 || h.Archived != 0 || h.Budget.RecordsRemaining != 0 {
		t.Fatal(h.Total, h.Archived, err)
	}
	p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil || p.Budget.RecordsRemaining != 100 || calls.Load() != 0 {
		t.Fatal("preview failed to free capacity without network", err, calls.Load())
	}
	h, err = s.ProjectAnalysisHistoryPage("root:品牌项目", 0, 0)
	if err != nil || h.Archived != 100 {
		t.Fatal("archives not visible", err)
	}
	if _, err = s.ProjectAnalysisHistoryPage("root:品牌项目", 20, h.Revision); err != nil {
		t.Fatal("archive page", err)
	}
	r, err := s.ProjectAnalysisResult("root:品牌项目", old[0].ID)
	if err != nil || !reflect.DeepEqual(r, old[0]) {
		t.Fatal("old result changed", err)
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", oldPreview.Token, old[0].ID, true, false); err != nil || calls.Load() != 0 {
		t.Fatal("archived confirmation resent", err, calls.Load())
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "after-archive", true, false); err != nil {
		t.Fatal(err)
	}
	waitAnalysis(t, s, "after-archive", true)
	if calls.Load() != 1 {
		t.Fatal("new request not exactly once", calls.Load())
	}
	h, err = s.ProjectAnalysisHistoryPage("root:品牌项目", 0, 0)
	if err != nil || h.Total != 101 || h.Archived != 100 || h.Budget.Reserved.Requests != 1 {
		t.Fatal("archive changed current budget", h, err)
	}
	seen := map[string]bool{}
	for offset := 0; offset < h.Total; offset += 20 {
		p, err := s.ProjectAnalysisHistoryPage("root:品牌项目", offset, h.Revision)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range p.Items {
			if seen[row.ID] {
				t.Fatal("duplicate history")
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 101 {
		t.Fatal("missing archive records")
	}
	restarted := &ShelfService{}
	defer restarted.closeModels()
	if _, err = restarted.ConfirmProjectAnalysis("root:品牌项目", oldPreview.Token, old[0].ID, true, false); err != nil || calls.Load() != 1 {
		t.Fatal("reopened old confirmation invoked model", err)
	}
}

func TestProjectAnalysisArchiveBatchReadPaths(t *testing.T) {
	s, _, calls := analysisServiceFixture(t, nil)
	p := previewWholeBatch(t, s)
	s.analyses.mu.Lock()
	spec := s.analyses.batchPreviews[p.Token].spec
	s.analyses.mu.Unlock()
	for i := range spec.Requests {
		spec.Requests[i].UnknownCostAccepted = true
	}
	e, err := s.memoryEnvironment(context.Background(), "root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().Add(-48 * time.Hour)
	b, fresh, err := e.store.ReserveAnalysisBatch(ctx, "old-batch", spec, at)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	b, _, _, err = e.store.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, b.RunEpoch, "old-claim", at)
	if err != nil {
		t.Fatal(err)
	}
	b, err = e.store.FinishAnalysisBatchPart(ctx, b.ID, b.SpecSHA, 0, b.RunEpoch, "old-claim", "failed", "request_failed", nil, models.Usage{}, at.Add(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.ReleaseProjectAnalysisBatch(p.Token)
	// Preparing a new scope records the old terminal note before taking its new snapshot.
	if _, err = s.ProjectAnalysisScopes("root:品牌项目", "", "", 0); err != nil {
		t.Fatal(err)
	}
	h, err := s.ProjectAnalysisBatchHistoryPage("root:品牌项目", 0, 0)
	if err != nil || h.Total != 1 || h.Archived != 1 {
		t.Fatal(h, err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.ProjectAnalysisBatchResult("root:品牌项目", b.ID); err != nil {
			t.Fatal(err)
		}
		d, err := s.ProjectAnalysisBatchDetail("root:品牌项目", b.ID)
		if err != nil || d.Item.CanResume || d.Item.TotalFiles != 1 || len(d.Summary.UnanalyzedIDs) != 1 {
			t.Fatal("archived detail changed", d.Item, err)
		}
		if _, err = s.StopProjectAnalysisBatch("root:品牌项目", b.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.RecordProjectAnalysisBatchNote("root:品牌项目", b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ConfirmProjectAnalysisBatch("root:品牌项目", p.Token, b.ID, true, false, false); err != nil {
			t.Fatal("old batch receipt not recovered", err)
		}
	}
	if _, err = s.ResumeProjectAnalysisBatch("root:品牌项目", b.ID, b.SpecSHA, true); !errors.Is(err, projectmemory.ErrAnalysisBatch) {
		t.Fatal("archived batch became resumable", err)
	}
	if _, err = s.PauseProjectAnalysisBatch("root:品牌项目", b.ID); err == nil {
		t.Fatal("archived batch writable")
	}
	v, err := e.store.Read(ctx)
	if err != nil || len(v.State.Notes) != 1 || calls.Load() != 0 {
		t.Fatal("read path duplicated note or model call", err, calls.Load())
	}
}
