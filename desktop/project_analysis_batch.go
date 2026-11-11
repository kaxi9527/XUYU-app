package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

type analysisBatchPreviewEntry struct {
	key       string
	spec      projectmemory.AnalysisBatchSpec
	expires   time.Time
	timer     *time.Timer
	requestID string
}
type AnalysisBatchPreview struct {
	Token      string                          `json:"token"`
	ExpiresAt  string                          `json:"expiresAt"`
	Spec       projectmemory.AnalysisBatchSpec `json:"spec"`
	TotalFiles int                             `json:"totalFiles"`
	Budget     projectmemory.AnalysisBudget    `json:"budget"`
	Required   projectmemory.AnalysisQuota     `json:"required"`
}
type AnalysisBatchItem struct {
	ID            string           `json:"id"`
	SpecSHA       string           `json:"specSHA"`
	ScopeName     string           `json:"scopeName"`
	Status        string           `json:"status"`
	Problem       string           `json:"problem"`
	CreatedAt     string           `json:"createdAt"`
	ExpiresAt     string           `json:"expiresAt"`
	Completed     int              `json:"completed"`
	Total         int              `json:"total"`
	Running       int              `json:"running"`
	TotalFiles    int              `json:"totalFiles"`
	AnalyzedFiles int              `json:"analyzedFiles"`
	NoteSaved     bool             `json:"noteSaved"`
	CanResume     bool             `json:"canResume"`
	Recipient     models.Recipient `json:"recipient"`
	ArchivedAt    string           `json:"archivedAt,omitempty"`
}
type AnalysisBatchDetail struct {
	Batch   projectmemory.AnalysisBatch `json:"batch"`
	Item    AnalysisBatchItem           `json:"item"`
	Summary projectcontext.BatchSummary `json:"summary"`
}
type AnalysisBatchHistory struct {
	Items    []AnalysisBatchItem `json:"items"`
	Total    int                 `json:"total"`
	Archived int                 `json:"archived"`
	Revision uint64              `json:"revision"`
}

func (s *ShelfService) checkBatchContext(ctx context.Context, key string, spec projectmemory.AnalysisBatchSpec) error {
	_, view, profile, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return err
	}
	index, err := projectcontext.NewScopeIndex(view, profile)
	if err != nil {
		return err
	}
	if index.SHA != spec.ScopeSHA {
		return projectcontext.ErrScopeChanged
	}
	return nil
}
func (s *ShelfService) PreviewProjectAnalysisBatch(key, scopeSHA, scopeID string) (AnalysisBatchPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.prepareAnalysisHistory(ctx, key); err != nil {
		return AnalysisBatchPreview{}, err
	}
	e, view, profile, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	index, err := projectcontext.NewScopeIndex(view, profile)
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	plan, err := index.PlanBatch(scopeSHA, scopeID)
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	modelStore, err := s.analysisModels()
	if err != nil {
		return AnalysisBatchPreview{}, modelPublicError(err)
	}
	lease, err := modelStore.Begin(ctx)
	if err != nil {
		return AnalysisBatchPreview{}, modelPublicError(err)
	}
	defer lease.Close()
	recipient, err := lease.Recipient()
	if err != nil {
		return AnalysisBatchPreview{}, modelPublicError(err)
	}
	if err = recipient.RequireLiveProof(); err != nil {
		return AnalysisBatchPreview{}, modelPublicError(err)
	}
	l, err := e.store.Analysis(ctx)
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	budget, err := l.Budget(time.Now())
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	token, err := analysisToken()
	if err != nil {
		return AnalysisBatchPreview{}, err
	}
	spec := projectmemory.AnalysisBatchSpec{PreviewID: token, PlanSHA: plan.SHA, ScopeSHA: scopeSHA, ScopeID: scopeID, ScopeName: plan.ScopeName, LedgerRevision: l.Revision, PreviousQuota: budget.Limit, Quota: budget.Limit, Requests: []projectmemory.AnalysisSpec{}}
	for _, packet := range plan.Packets {
		data, e := packet.Bytes()
		if e != nil {
			return AnalysisBatchPreview{}, e
		}
		spec.Requests = append(spec.Requests, projectmemory.AnalysisSpec{PreviewID: token, ProjectID: packet.Body.ProjectID, ContextSHA: packet.SHA256, MemoryRevision: packet.Body.MemoryRevision, MemoryHead: packet.Body.MemoryHead, Recipient: recipient, Request: models.GenerationRequest{System: projectcontext.Instructions, Input: string(data), MaxOutputTokens: projectmemory.AnalysisOutputLimit}})
	}
	required := spec.Required()
	spec.Quota.Requests = max(spec.Quota.Requests, budget.Reserved.Requests+required.Requests)
	spec.Quota.InputBytes = max(spec.Quota.InputBytes, budget.Reserved.InputBytes+required.InputBytes)
	spec.Quota.OutputTokens = max(spec.Quota.OutputTokens, budget.Reserved.OutputTokens+required.OutputTokens)
	if !spec.Quota.Valid() || budget.RecordsRemaining < required.Requests {
		return AnalysisBatchPreview{}, projectmemory.ErrAnalysisBudget
	}
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AnalysisBatchPreview{}, models.ErrStopped
	}
	if len(b.batchPreviews)+len(b.previews) >= 8 {
		return AnalysisBatchPreview{}, errors.New("请先关闭其他分析预览")
	}
	if b.batchPreviews == nil {
		b.batchPreviews = map[string]*analysisBatchPreviewEntry{}
	}
	ttl := b.ttl
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	p := &analysisBatchPreviewEntry{key: key, spec: spec, expires: time.Now().Add(ttl)}
	p.timer = time.AfterFunc(ttl, func() { b.mu.Lock(); delete(b.batchPreviews, token); b.mu.Unlock() })
	b.batchPreviews[token] = p
	return AnalysisBatchPreview{Token: token, ExpiresAt: p.expires.UTC().Format(time.RFC3339Nano), Spec: spec, TotalFiles: plan.TotalFiles, Budget: budget, Required: required}, nil
}
func (s *ShelfService) ReleaseProjectAnalysisBatch(token string) {
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.batchPreviews[token]; p != nil {
		p.timer.Stop()
		delete(b.batchPreviews, token)
	}
}

func (s *ShelfService) readAnalysisBatch(ctx context.Context, key, id string) (memoryEnvironment, projectmemory.AnalysisBatch, error) {
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return e, projectmemory.AnalysisBatch{}, err
	}
	if e.store == nil {
		return e, projectmemory.AnalysisBatch{}, projectmemory.ErrAnalysisBatch
	}
	_, batch, _, err := e.store.AnalysisReceipt(ctx, id)
	if err != nil {
		return e, projectmemory.AnalysisBatch{}, err
	}
	if batch == nil {
		return e, projectmemory.AnalysisBatch{}, projectmemory.ErrAnalysisBatch
	}
	return e, *batch, nil
}
func batchItem(b projectmemory.AnalysisBatch) AnalysisBatchItem {
	out := AnalysisBatchItem{ID: b.ID, SpecSHA: b.SpecSHA, ScopeName: b.Spec.ScopeName, Status: b.Status, Problem: b.Problem, CreatedAt: b.CreatedAt, ExpiresAt: b.ExpiresAt, Total: len(b.Items), NoteSaved: b.NoteSaved, Recipient: b.Spec.Requests[0].Recipient}
	for i, p := range b.Items {
		var packet struct {
			Body struct{ Files []json.RawMessage }
		}
		_ = json.Unmarshal([]byte(b.Spec.Requests[i].Request.Input), &packet)
		n := len(packet.Body.Files)
		out.TotalFiles += n
		if p.Status == "succeeded" {
			out.Completed++
			out.AnalyzedFiles += n
		}
		if p.Status == "running" {
			out.Running++
		}
	}
	expires, _ := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	out.CanResume = (b.Status == "active" || b.Status == "paused") && out.Running == 0 && time.Now().Before(expires)
	return out
}
func batchRunKey(b projectmemory.AnalysisBatch) string {
	return fmt.Sprintf("%s/batch/%s/%d", b.Spec.Requests[0].ProjectID, b.ID, b.RunEpoch)
}
func (s *ShelfService) batchItemLocal(b projectmemory.AnalysisBatch) AnalysisBatchItem {
	out := batchItem(b)
	s.analyses.mu.Lock()
	if s.analyses.batchRuns[batchRunKey(b)] != nil {
		out.CanResume = false
	}
	s.analyses.mu.Unlock()
	return out
}

func (s *ShelfService) ConfirmProjectAnalysisBatch(key, token, id string, acceptUnknownCost, acceptQuotaChange, strictCostLimit bool) (AnalysisBatchItem, error) {
	if strictCostLimit {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisCost
	}
	if !acceptUnknownCost || !modelOperationID(id) || len(id) > 64 {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AnalysisBatchItem{}, models.ErrStopped
	}
	p := b.batchPreviews[token]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if p == nil || p.requestID != "" {
		_, old, err := s.readAnalysisBatch(ctx, key, id)
		if err == nil && old.Spec.PreviewID == token {
			out := batchItem(old)
			if b.batchRuns[batchRunKey(old)] != nil {
				out.CanResume = false
			}
			return out, nil
		}
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	if p.key != key || !time.Now().Before(p.expires) {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	if len(b.runs)+len(b.batchRuns) >= 8 {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisBusy
	}
	if err := s.checkBatchContext(ctx, key, p.spec); err != nil {
		return AnalysisBatchItem{}, err
	}
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil || e.store == nil {
		return AnalysisBatchItem{}, projectmemory.ErrConflict
	}
	store, err := s.analysisModels()
	if err != nil {
		return AnalysisBatchItem{}, modelPublicError(err)
	}
	lease, err := store.Begin(ctx)
	if err != nil {
		return AnalysisBatchItem{}, modelPublicError(err)
	}
	defer lease.Close()
	recipient, err := lease.Recipient()
	if err != nil || recipient.Digest() != p.spec.Requests[0].Recipient.Digest() {
		return AnalysisBatchItem{}, errors.New("接收模型或账号已变化，请重新预览")
	}
	if err = recipient.RequireLiveProof(); err != nil {
		return AnalysisBatchItem{}, modelPublicError(err)
	}
	spec := p.spec
	spec.Requests = append([]projectmemory.AnalysisSpec(nil), p.spec.Requests...)
	spec.QuotaChangeAccepted = acceptQuotaChange
	for i := range spec.Requests {
		spec.Requests[i].UnknownCostAccepted = true
	}
	if spec.Quota != spec.PreviousQuota && !acceptQuotaChange {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	p.requestID = id
	var saved projectmemory.AnalysisBatch
	var fresh bool
	err = lease.Publish(ctx, func() error {
		var reserveErr error
		saved, fresh, reserveErr = e.store.ReserveAnalysisBatch(ctx, id, spec, time.Now())
		return reserveErr
	})
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	if !fresh {
		return batchItem(saved), nil
	}
	p.timer.Stop()
	delete(b.batchPreviews, token)
	s.startAnalysisBatchLocked(key, e, saved)
	out := batchItem(saved)
	out.CanResume = false
	return out, nil
}

func (s *ShelfService) startAnalysisBatchLocked(key string, e memoryEnvironment, batch projectmemory.AnalysisBatch) {
	b := &s.analyses
	if b.batchRuns == nil {
		b.batchRuns = map[string]*analysisRun{}
	}
	expires, _ := time.Parse(time.RFC3339Nano, batch.ExpiresAt)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	runKey := batchRunKey(batch)
	b.batchRuns[runKey] = &analysisRun{key: key, id: batch.ID, cancel: cancel}
	b.active.Add(1)
	go s.runAnalysisBatch(ctx, cancel, e, key, batch, runKey)
}
func (s *ShelfService) runAnalysisBatch(ctx context.Context, cancel context.CancelFunc, e memoryEnvironment, key string, batch projectmemory.AnalysisBatch, runKey string) {
	b := &s.analyses
	defer func() { cancel(); b.mu.Lock(); delete(b.batchRuns, runKey); b.mu.Unlock(); b.active.Done() }()
	finishRecord := func() {
		noteCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = e.store.RecordAnalysisBatchRunNote(noteCtx, batch.ID, batch.SpecSHA, batch.RunEpoch, time.Now())
	}
	stop := func(problem string) {
		stopCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_, _ = e.store.StopAnalysisBatchRun(stopCtx, batch.ID, batch.SpecSHA, problem, batch.RunEpoch, time.Now())
		finishRecord()
	}
	for {
		if ctx.Err() != nil {
			pauseCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = e.store.PauseAnalysisBatchRun(pauseCtx, batch.ID, batch.SpecSHA, batch.RunEpoch, time.Now())
			c()
			return
		}
		if err := s.checkBatchContext(ctx, key, batch.Spec); err != nil {
			stop("project_changed")
			return
		}
		store, err := s.analysisModels()
		if err != nil {
			stop("connection_changed")
			return
		}
		partCtx, partCancel := context.WithTimeout(ctx, 40*time.Second)
		lease, err := store.BeginForConfirmedRecipient(partCtx, batch.Spec.Requests[0].Recipient)
		if err != nil {
			partCancel()
			stop("connection_changed")
			return
		}
		claimID, err := analysisToken()
		if err != nil {
			lease.Close()
			partCancel()
			stop("request_failed")
			return
		}
		claimed, index, fresh, err := e.store.ClaimAnalysisBatch(partCtx, batch.ID, batch.SpecSHA, batch.RunEpoch, claimID, time.Now())
		if err != nil || !fresh {
			lease.Close()
			partCancel()
			finishRecord()
			return
		}
		packet, err := projectcontext.Restore([]byte(claimed.Spec.Requests[index].Request.Input))
		if err != nil {
			lease.Close()
			partCancel()
			stop("project_changed")
			return
		}
		result, status, problem, advice := generateAnalysisRequest(partCtx, partCancel, lease, packet, claimed.Spec.Requests[index].Request, claimed.Spec.Requests[index].Recipient, func(c context.Context) error { return s.checkBatchContext(c, key, batch.Spec) })
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		var finished projectmemory.AnalysisBatch
		finish := func() error {
			var e2 error
			var check func() error
			if status == "succeeded" {
				check = func() error {
					if partCtx.Err() != nil {
						return partCtx.Err()
					}
					return s.checkBatchContext(finishCtx, key, batch.Spec)
				}
			}
			finished, e2 = e.store.FinishAnalysisBatchPart(finishCtx, batch.ID, batch.SpecSHA, index, claimed.RunEpoch, claimID, status, problem, advice, result.Usage, time.Now(), check)
			return e2
		}
		if status == "succeeded" {
			err = lease.Publish(finishCtx, finish)
			if errors.Is(err, models.ErrStopped) || errors.Is(err, models.ErrAccountChanging) {
				status, problem, advice = "failed", "connection_changed", nil
				err = finish()
			}
		} else {
			err = finish()
		}
		finishCancel()
		lease.Close()
		partCancel()
		if err != nil {
			return
		}
		if finished.Status == "completed" || finished.Status == "stopped" {
			finishRecord()
			return
		}
		if finished.Status == "paused" {
			return
		}
	}
}

func (s *ShelfService) PauseProjectAnalysisBatch(key, id string) (AnalysisBatchItem, error) {
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	e, b, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	b, err = e.store.SetAnalysisBatchPaused(ctx, id, b.SpecSHA, true, time.Now())
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	return batchItem(b), err
}
func (s *ShelfService) ResumeProjectAnalysisBatch(key, id, sha string, confirmed bool) (AnalysisBatchItem, error) {
	if !confirmed {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	ctx, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	e, batch, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	if batch.SpecSHA != sha {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisConsent
	}
	expires, _ := time.Parse(time.RFC3339Nano, batch.ExpiresAt)
	if (batch.Status != "active" && batch.Status != "paused") || !time.Now().Before(expires) {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisBatch
	}
	if err = s.checkBatchContext(ctx, key, batch.Spec); err != nil {
		return AnalysisBatchItem{}, err
	}
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AnalysisBatchItem{}, models.ErrStopped
	}
	if len(b.runs)+len(b.batchRuns) >= 8 {
		return AnalysisBatchItem{}, projectmemory.ErrAnalysisBusy
	}
	store, err := s.analysisModels()
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	lease, err := store.BeginForConfirmedRecipient(ctx, batch.Spec.Requests[0].Recipient)
	if err != nil {
		return AnalysisBatchItem{}, modelPublicError(err)
	}
	defer lease.Close()
	err = lease.Publish(ctx, func() error {
		var e2 error
		batch, e2 = e.store.SetAnalysisBatchPaused(ctx, id, sha, false, time.Now())
		return e2
	})
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	s.startAnalysisBatchLocked(key, e, batch)
	out := batchItem(batch)
	out.CanResume = false
	return out, nil
}
func (s *ShelfService) StopProjectAnalysisBatch(key, id string) (AnalysisBatchItem, error) {
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	e, batch, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	if batch.NoteSaved && (batch.Status == "completed" || batch.Status == "stopped") {
		return batchItem(batch), nil
	}
	batch, err = e.store.StopAnalysisBatch(ctx, id, batch.SpecSHA, "cancelled", time.Now())
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	s.analyses.mu.Lock()
	for _, run := range s.analyses.batchRuns {
		if run.key == key && run.id == id {
			run.cancel()
		}
	}
	s.analyses.mu.Unlock()
	batch = completeBatchNote(ctx, e, batch)
	return batchItem(batch), nil
}

// Reconcile terminal receipts after stop, expiry or restart. Failed note writes
// remain visible as NoteSaved=false and can be retried without another model call.
func completeBatchNote(ctx context.Context, e memoryEnvironment, batch projectmemory.AnalysisBatch) projectmemory.AnalysisBatch {
	if batch.NoteSaved || batch.Status != "completed" && batch.Status != "stopped" {
		return batch
	}
	if err := e.store.RecordAnalysisBatchNote(ctx, batch.ID, batch.SpecSHA, time.Now()); err != nil {
		return batch
	}
	batch.NoteSaved = true
	return batch
}

func (s *ShelfService) ProjectAnalysisBatchResult(key, id string) (AnalysisBatchItem, error) {
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	e, batch, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return AnalysisBatchItem{}, err
	}
	if !batch.NoteSaved {
		batch, err = e.store.SettleAnalysisBatch(ctx, id, batch.SpecSHA, time.Now())
		if err != nil {
			return AnalysisBatchItem{}, err
		}
	}
	batch = completeBatchNote(ctx, e, batch)
	return s.batchItemLocal(batch), nil
}
func (s *ShelfService) ProjectAnalysisBatchDetail(key, id string) (AnalysisBatchDetail, error) {
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	e, batch, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return AnalysisBatchDetail{}, err
	}
	if !batch.NoteSaved {
		batch, err = e.store.SettleAnalysisBatch(ctx, id, batch.SpecSHA, time.Now())
		if err != nil {
			return AnalysisBatchDetail{}, err
		}
	}
	batch = completeBatchNote(ctx, e, batch)
	plan := projectcontext.BatchPlan{Packets: []projectcontext.Packet{}}
	responses := map[int]json.RawMessage{}
	for i, spec := range batch.Spec.Requests {
		packet, e := projectcontext.Restore([]byte(spec.Request.Input))
		if e != nil {
			return AnalysisBatchDetail{}, e
		}
		plan.Packets = append(plan.Packets, packet)
		plan.TotalFiles += len(packet.Body.Files)
		if batch.Items[i].Status == "succeeded" {
			responses[i] = batch.Items[i].Advice
		}
	}
	summary, err := plan.Summary(responses)
	if err != nil {
		return AnalysisBatchDetail{}, err
	}
	return AnalysisBatchDetail{Batch: batch, Item: s.batchItemLocal(batch), Summary: summary}, nil
}
func (s *ShelfService) ProjectAnalysisBatchHistory(key string, offset int) (AnalysisBatchHistory, error) {
	return s.ProjectAnalysisBatchHistoryPage(key, offset, 0)
}
func (s *ShelfService) RecordProjectAnalysisBatchNote(key, id string) error {
	ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	e, batch, err := s.readAnalysisBatch(ctx, key, id)
	if err != nil {
		return err
	}
	if batch.NoteSaved {
		return nil
	}
	return e.store.RecordAnalysisBatchNote(ctx, id, batch.SpecSHA, time.Now())
}
