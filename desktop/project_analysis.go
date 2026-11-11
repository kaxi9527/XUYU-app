package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type analysisPreviewEntry struct {
	key       string
	packet    projectcontext.Packet
	spec      projectmemory.AnalysisSpec
	expires   time.Time
	timer     *time.Timer
	requestID string
}
type analysisRun struct {
	key, id string
	cancel  context.CancelFunc
}
type analysisConnection struct {
	mu            sync.Mutex
	closed        bool
	previews      map[string]*analysisPreviewEntry
	runs          map[string]*analysisRun
	batchPreviews map[string]*analysisBatchPreviewEntry
	batchRuns     map[string]*analysisRun
	active        sync.WaitGroup
	ttl           time.Duration
}
type AnalysisPreview struct {
	Token           string                       `json:"token"`
	ExpiresAt       string                       `json:"expiresAt"`
	Recipient       models.Recipient             `json:"recipient"`
	Packet          projectcontext.Packet        `json:"packet"`
	System          string                       `json:"system"`
	Input           string                       `json:"input"`
	MaxOutputTokens int                          `json:"maxOutputTokens"`
	Budget          projectmemory.AnalysisBudget `json:"budget"`
}
type AnalysisItem struct {
	ID             string           `json:"id"`
	Status         string           `json:"status"`
	StartedAt      string           `json:"startedAt"`
	FinishedAt     string           `json:"finishedAt"`
	Recipient      models.Recipient `json:"recipient"`
	ContextSHA     string           `json:"contextSHA"`
	MemoryRevision uint64           `json:"memoryRevision"`
	Problem        string           `json:"problem"`
	NoteSaved      bool             `json:"noteSaved"`
	Usage          models.Usage     `json:"usage"`
	ArchivedAt     string           `json:"archivedAt,omitempty"`
}
type AnalysisHistory struct {
	ProjectID     string                       `json:"projectID"`
	Budget        projectmemory.AnalysisBudget `json:"budget"`
	Items         []AnalysisItem               `json:"items"`
	Total         int                          `json:"total"`
	Archived      int                          `json:"archived"`
	Revision      uint64                       `json:"revision"`
	BudgetProblem string                       `json:"budgetProblem,omitempty"`
}

func analysisItem(r projectmemory.AnalysisRequest) AnalysisItem {
	return AnalysisItem{ID: r.ID, Status: r.EffectiveStatus(time.Now()), StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Recipient: r.Spec.Recipient, ContextSHA: r.Spec.ContextSHA, MemoryRevision: r.Spec.MemoryRevision, Problem: r.Problem, NoteSaved: r.NoteSaved, Usage: r.Usage}
}

// Read only the actual project's own template through a confined directory
// handle. Atomic replacement or links during the read require a fresh preview.
func analysisProfile(e memoryEnvironment) (worktype.Profile, error) {
	var p worktype.Profile
	path := filepath.Join(e.project.Path, worktype.ProfileFile)
	if err := e.scope.allowedBase(path); err != nil {
		return p, err
	}
	root, err := os.OpenRoot(e.project.Path)
	if err != nil {
		return p, err
	}
	defer root.Close()
	before, err := root.Lstat(worktype.ProfileFile)
	if os.IsNotExist(err) {
		return p, errors.New("请先为这个项目确认工种模板，再生成整理建议")
	}
	if err != nil || !before.Mode().IsRegular() || before.Size() > 128<<10 {
		return p, errors.New("项目模板无法安全读取，请检查文件记录")
	}
	f, err := root.Open(worktype.ProfileFile)
	if err != nil {
		return p, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return p, projectmemory.ErrConflict
	}
	b, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil {
		return p, err
	}
	after, err := root.Lstat(worktype.ProfileFile)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || len(b) > 128<<10 {
		return p, projectmemory.ErrConflict
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, errors.New("项目模板无法读取")
	}
	return p, p.Validate()
}
func (s *ShelfService) analysisContext(ctx context.Context, key string, selected []string) (memoryEnvironment, projectcontext.Packet, error) {
	e, v, p, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return e, projectcontext.Packet{}, err
	}
	packet, err := projectcontext.Build(v, p, selected, projectcontext.Limits{})
	return e, packet, err
}

func (s *ShelfService) analysisSnapshot(ctx context.Context, key string) (memoryEnvironment, projectmemory.View, worktype.Profile, error) {
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return e, projectmemory.View{}, worktype.Profile{}, err
	}
	if e.store == nil {
		return e, projectmemory.View{}, worktype.Profile{}, errors.New("请先开启这个项目的文件记录并检查一次文件")
	}
	v, err := e.store.Read(ctx)
	if err != nil {
		return e, v, worktype.Profile{}, err
	}
	if !v.State.Enabled || v.State.Scope.Digest() != e.tracking.Digest() {
		return e, v, worktype.Profile{}, errors.New("项目记录已暂停或范围变化，请先更新文件记录")
	}
	p, err := analysisProfile(e)
	return e, v, p, err
}
func (s *ShelfService) analysisModels() (*models.Store, error) {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getStore()
}
func analysisToken() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

func (s *ShelfService) PreviewProjectAnalysis(key string, selected []string) (AnalysisPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.prepareAnalysisHistory(ctx, key); err != nil {
		return AnalysisPreview{}, err
	}
	e, packet, err := s.analysisContext(ctx, key, selected)
	if err != nil {
		return AnalysisPreview{}, err
	}
	return s.previewAnalysisPacket(ctx, key, e, packet)
}

func (s *ShelfService) previewAnalysisPacket(ctx context.Context, key string, e memoryEnvironment, packet projectcontext.Packet) (AnalysisPreview, error) {
	count := 0
	for _, f := range packet.Body.Files {
		if f.Status == "present" && f.Kind == "file" {
			count++
		}
	}
	if count == 0 {
		return AnalysisPreview{}, errors.New("当前范围没有可分析的普通文件")
	}
	store, err := s.analysisModels()
	if err != nil {
		return AnalysisPreview{}, modelPublicError(err)
	}
	lease, err := store.Begin(ctx)
	if err != nil {
		return AnalysisPreview{}, modelPublicError(err)
	}
	defer lease.Close()
	recipient, err := lease.Recipient()
	if err != nil {
		return AnalysisPreview{}, modelPublicError(err)
	}
	if err = recipient.RequireLiveProof(); err != nil {
		return AnalysisPreview{}, modelPublicError(err)
	}
	input, err := packet.Bytes()
	if err != nil {
		return AnalysisPreview{}, err
	}
	token, err := analysisToken()
	if err != nil {
		return AnalysisPreview{}, err
	}
	request := models.GenerationRequest{System: projectcontext.Instructions, Input: string(input), MaxOutputTokens: projectmemory.AnalysisOutputLimit}
	if err = request.Validate(); err != nil {
		return AnalysisPreview{}, err
	}
	l, err := e.store.Analysis(ctx)
	if err != nil {
		return AnalysisPreview{}, err
	}
	budget, err := l.Budget(time.Now())
	if err != nil {
		return AnalysisPreview{}, err
	}
	if budget.RecordsRemaining < 1 || budget.Reserved.Requests >= budget.Limit.Requests || budget.Reserved.InputBytes+len(request.System)+len(request.Input) > budget.Limit.InputBytes || budget.Reserved.OutputTokens+request.MaxOutputTokens > budget.Limit.OutputTokens {
		return AnalysisPreview{}, projectmemory.ErrAnalysisBudget
	}
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AnalysisPreview{}, models.ErrStopped
	}
	if b.previews == nil {
		b.previews = map[string]*analysisPreviewEntry{}
	}
	if len(b.previews)+len(b.batchPreviews) >= 8 {
		return AnalysisPreview{}, errors.New("请先关闭其他分析预览")
	}
	ttl := b.ttl
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	entry := &analysisPreviewEntry{key: key, packet: packet, expires: time.Now().Add(ttl), spec: projectmemory.AnalysisSpec{PreviewID: token, ProjectID: packet.Body.ProjectID, ContextSHA: packet.SHA256, MemoryRevision: packet.Body.MemoryRevision, MemoryHead: packet.Body.MemoryHead, Recipient: recipient, Request: request}}
	entry.timer = time.AfterFunc(ttl, func() { b.mu.Lock(); delete(b.previews, token); b.mu.Unlock() })
	b.previews[token] = entry
	return AnalysisPreview{Token: token, ExpiresAt: entry.expires.UTC().Format(time.RFC3339Nano), Recipient: recipient, Packet: packet, System: request.System, Input: request.Input, MaxOutputTokens: request.MaxOutputTokens, Budget: budget}, nil
}
func (s *ShelfService) ReleaseProjectAnalysis(token string) {
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.previews[token]; p != nil {
		p.timer.Stop()
		delete(b.previews, token)
	}
}

func (s *ShelfService) ConfirmProjectAnalysis(key, token, id string, acceptUnknownCost, strictCostLimit bool) (AnalysisItem, error) {
	if strictCostLimit {
		return AnalysisItem{}, projectmemory.ErrAnalysisCost
	}
	if !acceptUnknownCost || !modelOperationID(id) || len(id) > 64 {
		return AnalysisItem{}, projectmemory.ErrAnalysisConsent
	}
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return AnalysisItem{}, models.ErrStopped
	}
	p := b.previews[token]
	if p == nil || p.requestID != "" {
		r, err := s.ProjectAnalysisResult(key, id)
		if err == nil && r.Spec.PreviewID == token {
			return analysisItem(r), nil
		}
		return AnalysisItem{}, projectmemory.ErrAnalysisConsent
	}
	if p.key != key || !time.Now().Before(p.expires) {
		return AnalysisItem{}, projectmemory.ErrAnalysisConsent
	}
	if len(b.runs)+len(b.batchRuns) >= 8 {
		return AnalysisItem{}, errors.New("其他项目正在分析，请稍后再试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	e, packet, err := s.analysisContext(ctx, key, p.packet.Body.SelectedIDs)
	if err != nil || packet.SHA256 != p.packet.SHA256 {
		cancel()
		return AnalysisItem{}, projectcontext.ErrContext
	}
	store, err := s.analysisModels()
	if err != nil {
		cancel()
		return AnalysisItem{}, modelPublicError(err)
	}
	lease, err := store.Begin(ctx)
	if err != nil {
		cancel()
		return AnalysisItem{}, modelPublicError(err)
	}
	recipient, err := lease.Recipient()
	if err != nil || recipient.Digest() != p.spec.Recipient.Digest() {
		lease.Close()
		cancel()
		return AnalysisItem{}, errors.New("接收模型或账号已变化，请重新预览")
	}
	if err = recipient.RequireLiveProof(); err != nil {
		lease.Close()
		cancel()
		return AnalysisItem{}, modelPublicError(err)
	}
	p.requestID = id
	spec := p.spec
	spec.UnknownCostAccepted = true
	r, fresh, err := e.store.ReserveAnalysis(ctx, id, spec, time.Now())
	if err != nil || !fresh {
		lease.Close()
		cancel()
		if err != nil {
			return AnalysisItem{}, err
		}
		return analysisItem(r), nil
	}
	p.timer.Stop()
	delete(b.previews, token)
	if b.runs == nil {
		b.runs = map[string]*analysisRun{}
	}
	runKey := spec.ProjectID + "/" + id
	b.runs[runKey] = &analysisRun{key: key, id: id, cancel: cancel}
	b.active.Add(1)
	go s.runProjectAnalysis(ctx, cancel, lease, e, key, packet, r, runKey)
	return analysisItem(r), nil
}

func (s *ShelfService) runProjectAnalysis(ctx context.Context, cancel context.CancelFunc, lease *models.Lease, e memoryEnvironment, key string, packet projectcontext.Packet, r projectmemory.AnalysisRequest, runKey string) {
	b := &s.analyses
	defer func() {
		lease.Close()
		cancel()
		b.mu.Lock()
		delete(b.runs, runKey)
		b.mu.Unlock()
		b.active.Done()
	}()
	result, status, problem, advice := generateAnalysisRequest(ctx, cancel, lease, packet, r.Spec.Request, r.Spec.Recipient, func(checkCtx context.Context) error {
		_, current, err := s.analysisContext(checkCtx, key, packet.Body.SelectedIDs)
		if err != nil {
			return err
		}
		if current.SHA256 != packet.SHA256 {
			return projectcontext.ErrContext
		}
		return nil
	})
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if status == "succeeded" {
		_, current, err := s.analysisContext(finishCtx, key, packet.Body.SelectedIDs)
		if err != nil || current.SHA256 != packet.SHA256 {
			status, problem, advice = "stale", "project_changed", nil
		}
	}
	var finished projectmemory.AnalysisRequest
	finish := func() error {
		var err error
		if status == "succeeded" {
			finished, err = e.store.FinishAnalysisChecked(finishCtx, r.ID, r.SpecSHA, status, problem, advice, result.Usage, func() error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				_, current, checkErr := s.analysisContext(finishCtx, key, packet.Body.SelectedIDs)
				if checkErr != nil {
					return checkErr
				}
				if current.SHA256 != packet.SHA256 {
					return projectmemory.ErrConflict
				}
				return nil
			})
		} else {
			finished, err = e.store.FinishAnalysis(finishCtx, r.ID, r.SpecSHA, status, problem, advice, result.Usage, time.Now())
		}
		return err
	}
	var err error
	if status == "succeeded" {
		err = lease.Publish(finishCtx, finish)
		if errors.Is(err, models.ErrStopped) || errors.Is(err, models.ErrAccountChanging) {
			status, problem, advice = "failed", "connection_changed", nil
			err = finish()
		}
	} else {
		err = finish()
	}
	if err == nil && finished.Status == "succeeded" {
		_ = e.store.RecordAnalysisNote(finishCtx, r.ID, r.SpecSHA, time.Now())
	}
}

func generateAnalysisRequest(ctx context.Context, cancel context.CancelFunc, lease *models.Lease, packet projectcontext.Packet, request models.GenerationRequest, recipient models.Recipient, check func(context.Context) error) (models.GenerationResult, string, string, json.RawMessage) {
	if err := check(ctx); err != nil {
		return models.GenerationResult{}, "stale", "project_changed", nil
	}
	var changed atomic.Bool
	stop := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if check(ctx) != nil {
					changed.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	result, callErr := lease.GenerateForRecipient(recipient, request)
	close(stop)
	<-joined
	status, problem := "succeeded", ""
	var advice json.RawMessage
	if changed.Load() {
		status, problem = "stale", "project_changed"
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status, problem = "unknown", "unknown_result"
	} else if ctx.Err() != nil {
		status, problem = "cancelled", "cancelled"
	} else if callErr != nil {
		status, problem = "failed", "request_failed"
	} else {
		a, err := packet.ValidateAdvice([]byte(result.Text))
		if err != nil {
			status, problem = "failed", "unreliable_advice"
		} else {
			advice, _ = json.Marshal(a)
		}
	}
	return result, status, problem, advice
}

func (s *ShelfService) CancelProjectAnalysis(key, id string) {
	b := &s.analyses
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.runs {
		if r.key == key && r.id == id {
			r.cancel()
		}
	}
}
func (s *ShelfService) ProjectAnalysisHistory(key string, offset int) (AnalysisHistory, error) {
	return s.ProjectAnalysisHistoryPage(key, offset, 0)
}
func (s *ShelfService) ProjectAnalysisResult(key, id string) (projectmemory.AnalysisRequest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return projectmemory.AnalysisRequest{}, err
	}
	if e.store == nil {
		return projectmemory.AnalysisRequest{}, errors.New("没有找到这个项目的分析记录")
	}
	r, _, _, err := e.store.AnalysisReceipt(ctx, id)
	if err != nil {
		return projectmemory.AnalysisRequest{}, err
	}
	if r == nil {
		return projectmemory.AnalysisRequest{}, errors.New("没有找到这个分析请求")
	}
	packet, err := projectcontext.Restore([]byte(r.Spec.Request.Input))
	if err != nil {
		return projectmemory.AnalysisRequest{}, err
	}
	if r.Status == "succeeded" {
		if _, err = packet.ValidateAdvice(r.Advice); err != nil {
			return projectmemory.AnalysisRequest{}, err
		}
	}
	return *r, nil
}
func (s *ShelfService) RecordProjectAnalysisNote(key, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := s.ProjectAnalysisResult(key, id)
	if err != nil {
		return err
	}
	if r.NoteSaved {
		return nil
	}
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return err
	}
	if e.store == nil {
		return projectmemory.ErrConflict
	}
	return e.store.RecordAnalysisNote(ctx, id, r.SpecSHA, time.Now())
}
func (s *ShelfService) closeAnalyses() {
	b := &s.analyses
	b.mu.Lock()
	b.closed = true
	for token, p := range b.previews {
		p.timer.Stop()
		delete(b.previews, token)
	}
	for _, r := range b.runs {
		r.cancel()
	}
	for token, p := range b.batchPreviews {
		p.timer.Stop()
		delete(b.batchPreviews, token)
	}
	for _, r := range b.batchRuns {
		r.cancel()
	}
	b.mu.Unlock()
	b.active.Wait()
}
