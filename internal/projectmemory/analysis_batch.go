package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

const AnalysisBatchTTL = 2 * time.Hour

var ErrAnalysisBusy = errors.New("这个项目仍有分析任务，请先暂停或结束当前任务")
var ErrAnalysisBatch = errors.New("批次已暂停、过期或发生变化，请读取当前记录；不会自动重新发送")

func (q AnalysisQuota) Valid() bool {
	return q.Requests > 0 && q.Requests <= 100 && q.InputBytes > 0 && q.InputBytes <= 8<<20 && q.OutputTokens > 0 && q.OutputTokens <= 409600
}
func (q AnalysisQuota) allows(used AnalysisQuota) bool {
	return used.Requests <= q.Requests && used.InputBytes <= q.InputBytes && used.OutputTokens <= q.OutputTokens
}
func (q AnalysisQuota) add(b AnalysisQuota) AnalysisQuota {
	return AnalysisQuota{q.Requests + b.Requests, q.InputBytes + b.InputBytes, q.OutputTokens + b.OutputTokens}
}
func requestQuota(s AnalysisSpec) AnalysisQuota {
	return AnalysisQuota{1, len(s.Request.System) + len(s.Request.Input), s.Request.MaxOutputTokens}
}
func (l AnalysisLedger) limit() AnalysisQuota {
	if l.Quota != nil {
		return *l.Quota
	}
	return analysisLimits
}
func (l AnalysisLedger) recordCount() int {
	n := len(l.Requests)
	for _, b := range l.Batches {
		n += len(b.Items)
	}
	return n
}

type AnalysisBatchSpec struct {
	PreviewID           string         `json:"previewID"`
	PlanSHA             string         `json:"planSHA"`
	ScopeSHA            string         `json:"scopeSHA"`
	ScopeID             string         `json:"scopeID"`
	ScopeName           string         `json:"scopeName"`
	LedgerRevision      uint64         `json:"ledgerRevision"`
	PreviousQuota       AnalysisQuota  `json:"previousQuota"`
	Quota               AnalysisQuota  `json:"quota"`
	QuotaChangeAccepted bool           `json:"quotaChangeAccepted"`
	Requests            []AnalysisSpec `json:"requests"`
}

func (s AnalysisBatchSpec) Digest() string { return digestJSON(s) }
func (s AnalysisBatchSpec) Required() AnalysisQuota {
	out := AnalysisQuota{}
	for _, r := range s.Requests {
		out = out.add(requestQuota(r))
	}
	return out
}
func (s AnalysisBatchSpec) validate(project string) error {
	if !idOK(s.PreviewID) || !hashOK(s.PlanSHA) || !hashOK(s.ScopeSHA) || !idOK(s.ScopeID) || !textOK(s.ScopeName, 2048) || s.LedgerRevision >= 1<<53 || !s.PreviousQuota.Valid() || !s.Quota.Valid() || len(s.Requests) == 0 || len(s.Requests) > 100 || !s.Quota.allows(s.Required()) || s.PreviousQuota != s.Quota && !s.QuotaChangeAccepted {
		return ErrAnalysisConsent
	}
	first := s.Requests[0]
	files := map[string]bool{}
	for _, r := range s.Requests {
		if err := r.validate(project); err != nil {
			return err
		}
		if r.PreviewID != s.PreviewID || r.Recipient.RequireLiveProof() != nil || r.MemoryRevision != first.MemoryRevision || r.MemoryHead != first.MemoryHead || r.Recipient.Digest() != first.Recipient.Digest() {
			return ErrAnalysisConsent
		}
		var packet struct {
			Body struct {
				Files []struct{ ID, Kind, Status string }
			}
		}
		if json.Unmarshal([]byte(r.Request.Input), &packet) != nil || len(packet.Body.Files) == 0 || len(packet.Body.Files) > 40 {
			return ErrAnalysisConsent
		}
		for _, file := range packet.Body.Files {
			if !idOK(file.ID) || files[file.ID] || file.Kind != "file" || file.Status != "present" {
				return ErrAnalysisConsent
			}
			files[file.ID] = true
		}
	}
	return nil
}

type AnalysisBatchPart struct {
	Status     string          `json:"status"`
	ClaimID    string          `json:"claimID,omitempty"`
	RunEpoch   uint64          `json:"runEpoch,omitempty"`
	StartedAt  string          `json:"startedAt,omitempty"`
	DeadlineAt string          `json:"deadlineAt,omitempty"`
	FinishedAt string          `json:"finishedAt,omitempty"`
	Problem    string          `json:"problem,omitempty"`
	Advice     json.RawMessage `json:"advice,omitempty"`
	Usage      models.Usage    `json:"usage"`
}

func (p AnalysisBatchPart) EffectiveStatus(now time.Time) string {
	deadline, _ := time.Parse(time.RFC3339Nano, p.DeadlineAt)
	if p.Status == "running" && !now.Before(deadline) {
		return "unknown"
	}
	return p.Status
}

type AnalysisBatch struct {
	ID         string              `json:"id"`
	SpecSHA    string              `json:"specSHA"`
	Spec       AnalysisBatchSpec   `json:"spec"`
	CreatedAt  string              `json:"createdAt"`
	ExpiresAt  string              `json:"expiresAt"`
	FinishedAt string              `json:"finishedAt,omitempty"`
	Status     string              `json:"status"`
	Problem    string              `json:"problem,omitempty"`
	RunEpoch   uint64              `json:"runEpoch"`
	Items      []AnalysisBatchPart `json:"items"`
	NoteSaved  bool                `json:"noteSaved"`
}

func (b AnalysisBatch) alive(now time.Time) bool {
	created, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
	expires, _ := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	return !now.Before(created) && now.Before(expires) && (b.Status == "active" || b.Status == "paused")
}
func (l AnalysisLedger) batchBusy(now time.Time) bool {
	for _, b := range l.Batches {
		if b.alive(now) {
			return true
		}
	}
	return false
}
func (b AnalysisBatch) validate(project string) error {
	created, e1 := time.Parse(time.RFC3339Nano, b.CreatedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	if !idOK(b.ID) || len(b.ID) > 64 || b.Spec.validate(project) != nil || b.SpecSHA != b.Spec.Digest() || e1 != nil || e2 != nil || !expires.Equal(created.Add(AnalysisBatchTTL)) || b.RunEpoch == 0 || b.RunEpoch >= 1<<53 || len(b.Items) != len(b.Spec.Requests) || !contains([]string{"active", "paused", "completed", "stopped"}, b.Status) || !analysisProblem(b.Problem) {
		return ErrCorrupt
	}
	terminal := b.Status == "completed" || b.Status == "stopped"
	if terminal && !timeOK(b.FinishedAt) || !terminal && (b.FinishedAt != "" || b.NoteSaved || b.Problem != "") || b.Status == "stopped" && b.Problem == "" {
		return ErrCorrupt
	}
	claims := map[string]bool{}
	running := 0
	queued := false
	blocked := false
	for i, p := range b.Items {
		if !p.Usage.ValidFor(b.Spec.Requests[i].Recipient) || !analysisProblem(p.Problem) {
			return ErrCorrupt
		}
		if p.Status == "queued" {
			queued = true
			if p.ClaimID != "" || p.RunEpoch != 0 || p.StartedAt != "" || p.DeadlineAt != "" || p.FinishedAt != "" || len(p.Advice) != 0 || p.Problem != "" || p.Usage != (models.Usage{}) {
				return ErrCorrupt
			}
			continue
		}
		start, e1 := time.Parse(time.RFC3339Nano, p.StartedAt)
		deadline, e2 := time.Parse(time.RFC3339Nano, p.DeadlineAt)
		if queued || blocked || !idOK(p.ClaimID) || len(p.ClaimID) > 64 || claims[p.ClaimID] || p.RunEpoch == 0 || p.RunEpoch > b.RunEpoch || e1 != nil || e2 != nil || start.Before(created) || !start.Before(expires) || !deadline.Equal(start.Add(45*time.Second)) || !contains([]string{"running", "succeeded", "failed", "cancelled", "stale", "unknown"}, p.Status) {
			return ErrCorrupt
		}
		claims[p.ClaimID] = true
		blocked = p.Status != "succeeded"
		if p.Status == "running" {
			running++
			if p.FinishedAt != "" || len(p.Advice) != 0 || p.Usage.OpenRouterDebit != nil {
				return ErrCorrupt
			}
		} else if !timeOK(p.FinishedAt) {
			return ErrCorrupt
		}
		if p.Status == "succeeded" {
			if len(p.Advice) == 0 || len(p.Advice) > models.MaxGeneratedBytes || !json.Valid(p.Advice) {
				return ErrCorrupt
			}
		} else if len(p.Advice) != 0 {
			return ErrCorrupt
		}
		if b.Status == "completed" && p.Status != "succeeded" {
			return ErrCorrupt
		}
	}
	if running > 1 || b.Status == "completed" && queued {
		return ErrCorrupt
	}
	return nil
}

// All attempts are reserved in the same file/transaction as the new project
// quota. No network call is allowed until this durable write succeeds.
func (s *Store) ReserveAnalysisBatch(ctx context.Context, id string, spec AnalysisBatchSpec, now time.Time) (AnalysisBatch, bool, error) {
	if !idOK(id) || len(id) > 64 || now.IsZero() {
		return AnalysisBatch{}, false, ErrAnalysisConsent
	}
	if err := spec.validate(s.project.ID); err != nil {
		return AnalysisBatch{}, false, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, false, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, false, err
	}
	for _, b := range l.Batches {
		if b.ID == id {
			if b.SpecSHA != spec.Digest() {
				return b, false, ErrOperationReused
			}
			return b, false, nil
		}
	}
	for _, r := range l.Requests {
		if r.ID == id {
			return AnalysisBatch{}, false, ErrOperationReused
		}
	}
	archived, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return AnalysisBatch{}, false, err
	}
	if found {
		if archived.Batch == nil || archived.Batch.SpecSHA != spec.Digest() {
			return AnalysisBatch{}, false, ErrOperationReused
		}
		return *archived.Batch, false, nil
	}
	if l.Revision != spec.LedgerRevision || l.limit() != spec.PreviousQuota {
		return AnalysisBatch{}, false, ErrConflict
	}
	if l.batchBusy(now) {
		return AnalysisBatch{}, false, ErrAnalysisBusy
	}
	for _, r := range l.Requests {
		if r.EffectiveStatus(now) == "running" {
			return AnalysisBatch{}, false, ErrAnalysisBusy
		}
	}
	v, err := s.read(ctx)
	if err != nil {
		return AnalysisBatch{}, false, err
	}
	first := spec.Requests[0]
	if !v.State.Enabled || v.State.Revision != first.MemoryRevision || v.HeadSHA != first.MemoryHead {
		return AnalysisBatch{}, false, ErrConflict
	}
	budget, err := l.Budget(now)
	if err != nil {
		return AnalysisBatch{}, false, err
	}
	if budget.RecordsRemaining < len(spec.Requests) || !spec.Quota.allows(budget.Reserved.add(spec.Required())) {
		return AnalysisBatch{}, false, ErrAnalysisBudget
	}
	b := AnalysisBatch{ID: id, Spec: spec, SpecSHA: spec.Digest(), CreatedAt: stamp(now), ExpiresAt: stamp(now.Add(AnalysisBatchTTL)), Status: "active", RunEpoch: 1, Items: make([]AnalysisBatchPart, len(spec.Requests))}
	for i := range b.Items {
		b.Items[i].Status = "queued"
	}
	quota := spec.Quota
	l.Quota = &quota
	l.Batches = append(l.Batches, b)
	if err = s.analysisWrite(ctx, l); err != nil {
		return AnalysisBatch{}, false, err
	}
	return b, true, nil
}

func batchAt(l *AnalysisLedger, id, sha string) (*AnalysisBatch, error) {
	for i := range l.Batches {
		b := &l.Batches[i]
		if b.ID == id {
			if b.SpecSHA != sha {
				return nil, ErrOperationReused
			}
			return b, nil
		}
	}
	return nil, ErrAnalysisBatch
}
func (s *Store) batchContext(ctx context.Context, b *AnalysisBatch) error {
	v, err := s.read(ctx)
	if err != nil {
		return err
	}
	first := b.Spec.Requests[0]
	if !v.State.Enabled || v.State.Revision != first.MemoryRevision || v.HeadSHA != first.MemoryHead {
		return ErrConflict
	}
	return nil
}
func stopBatch(b *AnalysisBatch, problem string, now time.Time) {
	b.Status = "stopped"
	b.Problem = problem
	b.FinishedAt = stamp(now)
}

// The durable claim and unique running slot fence competing processes. A
// claimed request can never return to queued, even if the caller crashes.
func (s *Store) ClaimAnalysisBatch(ctx context.Context, id, sha string, epoch uint64, claimID string, now time.Time) (AnalysisBatch, int, bool, error) {
	if !idOK(claimID) || len(claimID) > 64 {
		return AnalysisBatch{}, -1, false, ErrAnalysisConsent
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, -1, false, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, -1, false, err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		return AnalysisBatch{}, -1, false, err
	}
	if b.RunEpoch != epoch || b.Status != "active" || !b.alive(now) {
		return *b, -1, false, ErrAnalysisBatch
	}
	for i := range b.Items {
		p := &b.Items[i]
		if p.ClaimID == claimID {
			return *b, -1, false, ErrOperationReused
		}
		if p.Status == "running" {
			if p.EffectiveStatus(now) == "unknown" {
				p.Status = "unknown"
				p.Problem = "unknown_result"
				p.FinishedAt = stamp(now)
				stopBatch(b, "unknown_result", now)
				err = s.analysisWrite(ctx, l)
				return *b, -1, false, err
			}
			return *b, -1, false, ErrAnalysisBusy
		}
		if p.Status != "queued" && p.Status != "succeeded" {
			return *b, -1, false, ErrAnalysisBatch
		}
	}
	if err = s.batchContext(ctx, b); err != nil {
		stopBatch(b, "project_changed", now)
		if writeErr := s.analysisWrite(ctx, l); writeErr != nil {
			return *b, -1, false, writeErr
		}
		return *b, -1, false, err
	}
	if _, err = l.Budget(now); err != nil {
		return *b, -1, false, err
	}
	for i := range b.Items {
		if b.Items[i].Status == "queued" {
			b.Items[i] = AnalysisBatchPart{Status: "running", ClaimID: claimID, RunEpoch: epoch, StartedAt: stamp(now), DeadlineAt: stamp(now.Add(45 * time.Second))}
			if err = s.analysisWrite(ctx, l); err != nil {
				return *b, -1, false, err
			}
			return *b, i, true, nil
		}
	}
	return *b, -1, false, ErrAnalysisBatch
}

// Pause leaves the in-flight request intact. Resume fences the previous runner
// with a new epoch, and never claims or retries a request itself.
func (s *Store) SetAnalysisBatchPaused(ctx context.Context, id, sha string, pause bool, now time.Time) (AnalysisBatch, error) {
	return s.setAnalysisBatchPaused(ctx, id, sha, pause, 0, now)
}

// PauseAnalysisBatchRun fences a worker's automatic shutdown against a later resume.
func (s *Store) PauseAnalysisBatchRun(ctx context.Context, id, sha string, epoch uint64, now time.Time) (AnalysisBatch, error) {
	if epoch == 0 {
		return AnalysisBatch{}, ErrAnalysisBatch
	}
	return s.setAnalysisBatchPaused(ctx, id, sha, true, epoch, now)
}

func (s *Store) setAnalysisBatchPaused(ctx context.Context, id, sha string, pause bool, epoch uint64, now time.Time) (AnalysisBatch, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		return AnalysisBatch{}, err
	}
	if epoch != 0 && b.RunEpoch != epoch {
		return *b, ErrAnalysisBatch
	}
	if !b.alive(now) {
		return *b, ErrAnalysisBatch
	}
	if pause {
		if b.Status == "paused" {
			return *b, nil
		}
		b.Status = "paused"
	} else {
		for i := range b.Items {
			p := &b.Items[i]
			if p.Status == "running" {
				if p.EffectiveStatus(now) == "unknown" {
					p.Status = "unknown"
					p.Problem = "unknown_result"
					p.FinishedAt = stamp(now)
					stopBatch(b, "unknown_result", now)
					err = s.analysisWrite(ctx, l)
					if err != nil {
						return *b, err
					}
					return *b, ErrAnalysisBatch
				}
				return *b, ErrAnalysisBusy
			}
		}
		if err = s.batchContext(ctx, b); err != nil {
			return *b, err
		}
		if b.RunEpoch >= 1<<53-1 {
			return *b, ErrAnalysisBatch
		}
		b.Status = "active"
		b.RunEpoch++
	}
	err = s.analysisWrite(ctx, l)
	return *b, err
}

func (s *Store) StopAnalysisBatch(ctx context.Context, id, sha, problem string, now time.Time) (AnalysisBatch, error) {
	return s.stopAnalysisBatch(ctx, id, sha, problem, 0, now)
}

// StopAnalysisBatchRun permits only the currently authorized worker to stop a batch.
func (s *Store) StopAnalysisBatchRun(ctx context.Context, id, sha, problem string, epoch uint64, now time.Time) (AnalysisBatch, error) {
	if epoch == 0 {
		return AnalysisBatch{}, ErrAnalysisBatch
	}
	return s.stopAnalysisBatch(ctx, id, sha, problem, epoch, now)
}

func (s *Store) stopAnalysisBatch(ctx context.Context, id, sha, problem string, epoch uint64, now time.Time) (AnalysisBatch, error) {
	if !analysisProblem(problem) || problem == "" {
		return AnalysisBatch{}, ErrAnalysisBatch
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		return AnalysisBatch{}, err
	}
	if epoch != 0 && b.RunEpoch != epoch {
		return *b, ErrAnalysisBatch
	}
	if b.Status == "completed" || b.Status == "stopped" {
		return *b, nil
	}
	created, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
	if now.Before(created) {
		return *b, ErrConflict
	}
	stopBatch(b, problem, now)
	err = s.analysisWrite(ctx, l)
	return *b, err
}

func (s *Store) FinishAnalysisBatchPart(ctx context.Context, id, sha string, index int, epoch uint64, claimID, status, problem string, advice json.RawMessage, usage models.Usage, now time.Time, check func() error) (AnalysisBatch, error) {
	if !contains([]string{"succeeded", "failed", "cancelled", "stale", "unknown"}, status) || !analysisProblem(problem) || !usage.Valid() || status != "succeeded" && problem == "" || status == "succeeded" && (problem != "" || check == nil || len(advice) == 0 || len(advice) > models.MaxGeneratedBytes || !json.Valid(advice)) {
		return AnalysisBatch{}, ErrAnalysisBatch
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		return AnalysisBatch{}, err
	}
	if index < 0 || index >= len(b.Items) {
		return *b, ErrAnalysisBatch
	}
	p := &b.Items[index]
	if p.ClaimID != claimID || p.RunEpoch != epoch {
		return *b, ErrOperationReused
	}
	if p.Status != "running" {
		return *b, nil
	}
	if !usage.ValidFor(b.Spec.Requests[index].Recipient) {
		return *b, ErrAnalysisConsent
	}
	if status == "succeeded" {
		if err = check(); err != nil {
			status, problem = "stale", "project_changed"
			if errors.Is(err, context.Canceled) {
				status, problem = "cancelled", "cancelled"
			}
		}
		now = time.Now()
	}
	started, _ := time.Parse(time.RFC3339Nano, p.StartedAt)
	if now.Before(started) {
		return *b, ErrConflict
	}
	if status == "succeeded" {
		if b.Status == "stopped" {
			status, problem = "cancelled", b.Problem
		} else if p.EffectiveStatus(now) == "unknown" {
			status, problem = "unknown", "unknown_result"
		} else if !b.alive(now) || b.RunEpoch != epoch {
			status, problem = "stale", "project_changed"
		} else if err = s.batchContext(ctx, b); err != nil {
			status, problem = "stale", "project_changed"
		}
	}
	if status != "succeeded" {
		advice = nil
		stopBatch(b, problem, now)
	}
	p.Status, p.Problem, p.Advice, p.Usage, p.FinishedAt = status, problem, advice, usage, stamp(now)
	complete := true
	for _, part := range b.Items {
		if part.Status != "succeeded" {
			complete = false
			break
		}
	}
	if complete {
		b.Status = "completed"
		b.Problem = ""
		b.FinishedAt = stamp(now)
	}
	err = s.analysisWrite(ctx, l)
	return *b, err
}

func batchNote(b AnalysisBatch) Note {
	success := 0
	for _, p := range b.Items {
		if p.Status == "succeeded" {
			success++
		}
	}
	return Note{Kind: "agent_proposal", Agent: "workshelf", Model: b.Spec.Requests[0].Recipient.ModelID, Text: fmt.Sprintf("本次目录分析完成 %d / %d 组；其余未完成。建议仅覆盖已验证组，文件没有移动；完整输入、调用和结果保存在本项目的批次记录中。", success, len(b.Items)), Evidence: "analysis-batch:" + b.ID + "; plan:" + b.Spec.PlanSHA}
}

// Settle records an expired/unknown attempt but never requeues it. It is safe
// after restart and does not create a model lease or dispatch network work.
func (s *Store) SettleAnalysisBatch(ctx context.Context, id, sha string, now time.Time) (AnalysisBatch, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisBatch{}, err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		archived, found, readErr := s.archivedAnalysis(ctx, l, id)
		if readErr != nil {
			return AnalysisBatch{}, readErr
		}
		if found && archived.Batch != nil && archived.Batch.SpecSHA == sha {
			return *archived.Batch, nil
		}
		return AnalysisBatch{}, err
	}
	created, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
	expires, _ := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	if now.Before(created) {
		return *b, ErrConflict
	}
	changed := false
	for i := range b.Items {
		p := &b.Items[i]
		if p.Status == "running" && p.EffectiveStatus(now) == "unknown" {
			p.Status = "unknown"
			p.Problem = "unknown_result"
			p.FinishedAt = stamp(now)
			stopBatch(b, "unknown_result", now)
			changed = true
		}
	}
	if (b.Status == "active" || b.Status == "paused") && !now.Before(expires) {
		stopBatch(b, "expired", now)
		changed = true
	}
	if changed {
		err = s.analysisWrite(ctx, l)
	}
	return *b, err
}

func (s *Store) RecordAnalysisBatchNote(ctx context.Context, id, sha string, now time.Time) error {
	return s.recordAnalysisBatchNote(ctx, id, sha, 0, now, false)
}

func (s *Store) RecordAnalysisBatchNoteIfIdle(ctx context.Context, id, sha string, now time.Time) error {
	return s.recordAnalysisBatchNote(ctx, id, sha, 0, now, true)
}

func (s *Store) RecordAnalysisBatchRunNote(ctx context.Context, id, sha string, epoch uint64, now time.Time) error {
	if epoch == 0 {
		return ErrAnalysisBatch
	}
	return s.recordAnalysisBatchNote(ctx, id, sha, epoch, now, false)
}

func (s *Store) recordAnalysisBatchNote(ctx context.Context, id, sha string, epoch uint64, now time.Time, requireIdle bool) error {
	l, err := s.Analysis(ctx)
	if err != nil {
		return err
	}
	b, err := batchAt(&l, id, sha)
	if err != nil {
		archived, found, readErr := s.archivedAnalysis(ctx, l, id)
		if readErr != nil {
			return readErr
		}
		if found && archived.Batch != nil && archived.Batch.SpecSHA == sha && archived.Batch.NoteSaved && (epoch == 0 || archived.Batch.RunEpoch == epoch) {
			return nil
		}
		return err
	}
	// Terminal batches cannot resume, so the checked epoch remains stable during AddNote.
	if epoch != 0 && b.RunEpoch != epoch {
		return ErrAnalysisBatch
	}
	if b.Status != "completed" && b.Status != "stopped" {
		return ErrAnalysisBatch
	}
	if b.NoteSaved {
		return nil
	}
	for _, p := range b.Items {
		if p.Status == "running" {
			return ErrAnalysisBusy
		}
	}
	v, err := s.Read(ctx)
	if err != nil {
		return err
	}
	note := batchNote(*b)
	op := "analysis-batch-note-" + id
	var check func() error
	if requireIdle {
		check = func() error { return s.analysisIdle(ctx, now) }
	}
	if _, err = s.addNoteChecked(ctx, v.State.Revision, op, note, now, check); err != nil {
		return err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	l, err = s.analysisRead(ctx)
	if err != nil {
		return err
	}
	b, err = batchAt(&l, id, sha)
	if err != nil {
		archived, found, readErr := s.archivedAnalysis(ctx, l, id)
		if readErr != nil {
			return readErr
		}
		if found && archived.Batch != nil && archived.Batch.SpecSHA == sha && archived.Batch.NoteSaved && (epoch == 0 || archived.Batch.RunEpoch == epoch) {
			return nil
		}
		return err
	}
	v, err = s.read(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, operation := range v.State.Operations {
		if operation.ID == op {
			record, _, readErr := s.readRecord(ctx, operation.Revision)
			if readErr != nil {
				return readErr
			}
			for _, event := range record.Events {
				if n := event.Note; n != nil && n.Kind == note.Kind && n.Text == note.Text && n.Agent == note.Agent && n.Model == note.Model && n.Evidence == note.Evidence && event.Source == "agent_declaration" {
					found = true
				}
			}
		}
	}
	if !found {
		return ErrConflict
	}
	b.NoteSaved = true
	return s.analysisWrite(ctx, l)
}
