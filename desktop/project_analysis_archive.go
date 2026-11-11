package main

import (
	"context"
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

// Local maintenance precedes a new context snapshot. It never invokes a model.
// Idle-only notes recheck the ledger inside the project mutation lock, so another
// window cannot start a request between the check and the memory append.
func (s *ShelfService) prepareAnalysisHistory(ctx context.Context, key string) error {
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return err
	}
	if e.store == nil {
		return nil
	}
	v, err := e.store.Read(ctx)
	if err != nil {
		return err
	}
	if !v.State.Enabled || v.State.Scope.Digest() != e.tracking.Digest() {
		return nil
	}
	l, err := e.store.Analysis(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	if _, err = l.Budget(now); err != nil {
		return err
	}
	for _, r := range l.Requests {
		at, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
		if at.After(now.Add(-24 * time.Hour)) {
			continue
		}
		if r.Status == "running" {
			r, err = e.store.SettleAnalysis(ctx, r.ID, r.SpecSHA, now)
			if err != nil {
				return err
			}
		}
		if r.Status == "succeeded" && !r.NoteSaved {
			if _, err = s.ProjectAnalysisResult(key, r.ID); err != nil {
				return err
			}
			err = e.store.RecordAnalysisNoteIfIdle(ctx, r.ID, r.SpecSHA, now)
			if err != nil && !errors.Is(err, projectmemory.ErrAnalysisBusy) {
				return err
			}
		}
	}
	for _, b := range l.Batches {
		at, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
		if at.After(now.Add(-24 * time.Hour)) {
			continue
		}
		b, err = e.store.SettleAnalysisBatch(ctx, b.ID, b.SpecSHA, now)
		if err != nil {
			return err
		}
		if !b.NoteSaved && (b.Status == "completed" || b.Status == "stopped") {
			err = e.store.RecordAnalysisBatchNoteIfIdle(ctx, b.ID, b.SpecSHA, now)
			if err != nil && !errors.Is(err, projectmemory.ErrAnalysisBusy) {
				return err
			}
		}
	}
	_, err = e.store.ArchiveAnalyses(ctx, now)
	return err
}

func singleAnalysisSummary(v projectmemory.AnalysisSummary) AnalysisItem {
	return AnalysisItem{ID: v.ID, Status: v.Status, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, Recipient: v.Recipient, ContextSHA: v.ContextSHA, MemoryRevision: v.MemoryRevision, Problem: v.Problem, NoteSaved: v.NoteSaved, Usage: v.Usage, ArchivedAt: v.ArchivedAt}
}
func batchAnalysisSummary(v projectmemory.AnalysisSummary) AnalysisBatchItem {
	expires, _ := time.Parse(time.RFC3339Nano, v.ExpiresAt)
	return AnalysisBatchItem{ID: v.ID, SpecSHA: v.SpecSHA, ScopeName: v.ScopeName, Status: v.Status, Problem: v.Problem, CreatedAt: v.StartedAt, ExpiresAt: v.ExpiresAt, Total: v.Total, Completed: v.Completed, Running: v.Running, TotalFiles: v.TotalFiles, AnalyzedFiles: v.AnalyzedFiles, NoteSaved: v.NoteSaved, Recipient: v.Recipient, ArchivedAt: v.ArchivedAt, CanResume: (v.Status == "active" || v.Status == "paused") && v.Running == 0 && time.Now().Before(expires)}
}

func (s *ShelfService) ProjectAnalysisHistoryPage(key string, offset int, revision uint64) (AnalysisHistory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	out := AnalysisHistory{Items: []AnalysisItem{}}
	if err != nil {
		return out, err
	}
	if e.store == nil {
		return out, nil
	}
	p, err := e.store.AnalysisHistory(ctx, "single", offset, revision)
	if err != nil {
		return out, err
	}
	out.ProjectID = p.ProjectID
	out.Total = p.Total
	out.Archived = p.Archived
	out.Revision = p.Revision
	out.Budget = p.Budget
	out.BudgetProblem = p.BudgetProblem
	for _, v := range p.Items {
		item := singleAnalysisSummary(v)
		if item.Status == "running" {
			at, _ := time.Parse(time.RFC3339Nano, item.StartedAt)
			if !time.Now().Before(at.Add(45 * time.Second)) {
				item.Status = "unknown"
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
func (s *ShelfService) ProjectAnalysisBatchHistoryPage(key string, offset int, revision uint64) (AnalysisBatchHistory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	out := AnalysisBatchHistory{Items: []AnalysisBatchItem{}}
	if err != nil {
		return out, err
	}
	if e.store == nil {
		return out, nil
	}
	p, err := e.store.AnalysisHistory(ctx, "batch", offset, revision)
	if err != nil {
		return out, err
	}
	out.Total = p.Total
	out.Archived = p.Archived
	out.Revision = p.Revision
	for _, v := range p.Items {
		item := batchAnalysisSummary(v)
		// A local worker can still be exiting after the last completion. The
		// authoritative resume endpoint also fences by persisted run epoch.
		s.analyses.mu.Lock()
		for _, run := range s.analyses.batchRuns {
			if run.key == key && run.id == item.ID {
				item.CanResume = false
			}
		}
		s.analyses.mu.Unlock()
		out.Items = append(out.Items, item)
	}
	return out, nil
}
