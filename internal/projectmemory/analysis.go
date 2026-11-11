package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

var ErrAnalysisBudget = errors.New("这个项目的分析额度已用完，请稍后再试；原请求记录已保留")
var ErrAnalysisConsent = errors.New("请重新查看发送内容、接收方和费用说明后确认")
var ErrAnalysisCost = errors.New("当前连接无法核实严格金额上限，未发送项目资料")

// Quotas reserve every attempted call, including cancelled/failed/unknown ones.
// They are per-project rolling 24-hour limits, not claims about vendor billing.
type AnalysisQuota struct {
	Requests     int `json:"requests"`
	InputBytes   int `json:"inputBytes"`
	OutputTokens int `json:"outputTokens"`
}

var analysisLimits = AnalysisQuota{Requests: 10, InputBytes: 640 << 10, OutputTokens: 40960}

const AnalysisOutputLimit = 4096
const maxAnalysisRecords = 100

type AnalysisSpec struct {
	PreviewID           string                   `json:"previewID"`
	ProjectID           string                   `json:"projectID"`
	ContextSHA          string                   `json:"contextSHA"`
	MemoryRevision      uint64                   `json:"memoryRevision"`
	MemoryHead          string                   `json:"memoryHead"`
	Recipient           models.Recipient         `json:"recipient"`
	Request             models.GenerationRequest `json:"request"`
	UnknownCostAccepted bool                     `json:"unknownCostAccepted"`
	StrictCostLimit     bool                     `json:"strictCostLimit"`
}

func (a AnalysisSpec) Digest() string { return digestJSON(a) }
func (a AnalysisSpec) validate(project string) error {
	if a.StrictCostLimit {
		return ErrAnalysisCost
	}
	if !a.UnknownCostAccepted {
		return ErrAnalysisConsent
	}
	if !idOK(a.PreviewID) || a.ProjectID != project || !hashOK(a.ContextSHA) || !hashOK(a.MemoryHead) || a.MemoryRevision == 0 || a.MemoryRevision >= 1<<53 || !a.Recipient.Valid() || a.Request.Validate() != nil || a.Request.MaxOutputTokens > AnalysisOutputLimit {
		return ErrAnalysisConsent
	}
	var packet struct {
		SHA256 string `json:"sha256"`
		Body   struct {
			ProjectID      string `json:"projectID"`
			MemoryRevision uint64 `json:"memoryRevision"`
			MemoryHead     string `json:"memoryHead"`
		} `json:"body"`
	}
	if json.Unmarshal([]byte(a.Request.Input), &packet) != nil || packet.SHA256 != a.ContextSHA || packet.Body.ProjectID != project || packet.Body.MemoryRevision != a.MemoryRevision || packet.Body.MemoryHead != a.MemoryHead {
		return ErrAnalysisConsent
	}
	return nil
}

type AnalysisRequest struct {
	ID         string          `json:"id"`
	SpecSHA    string          `json:"specSHA"`
	Spec       AnalysisSpec    `json:"spec"`
	StartedAt  string          `json:"startedAt"`
	DeadlineAt string          `json:"deadlineAt"`
	FinishedAt string          `json:"finishedAt,omitempty"`
	Status     string          `json:"status"`
	Problem    string          `json:"problem,omitempty"`
	Advice     json.RawMessage `json:"advice,omitempty"`
	Usage      models.Usage    `json:"usage"`
	NoteSaved  bool            `json:"noteSaved"`
}

func (r AnalysisRequest) EffectiveStatus(now time.Time) string {
	deadline, _ := time.Parse(time.RFC3339Nano, r.DeadlineAt)
	if r.Status == "running" && !now.Before(deadline) {
		return "unknown"
	}
	return r.Status
}

type AnalysisLedger struct {
	Schema    int               `json:"schema"`
	ProjectID string            `json:"projectID"`
	Revision  uint64            `json:"revision"`
	Requests  []AnalysisRequest `json:"requests"`
	Quota     *AnalysisQuota    `json:"quota,omitempty"`
	Batches   []AnalysisBatch   `json:"batches,omitempty"`
	Archive   *AnalysisArchive  `json:"archive,omitempty"`
}
type AnalysisBudget struct {
	Limit            AnalysisQuota `json:"limit"`
	Reserved         AnalysisQuota `json:"reserved"`
	RecordsRemaining int           `json:"recordsRemaining"`
	CostStatus       string        `json:"costStatus"`
}

func (l AnalysisLedger) Budget(now time.Time) (AnalysisBudget, error) {
	out := AnalysisBudget{Limit: l.limit(), RecordsRemaining: maxAnalysisRecords - l.recordCount(), CostStatus: "unknown"}
	if !out.Limit.Valid() || out.RecordsRemaining < 0 || !l.Archive.valid() {
		return out, ErrCorrupt
	}
	if l.Archive != nil {
		at, _ := time.Parse(time.RFC3339Nano, l.Archive.LastAt)
		if now.Before(at) {
			return out, errors.New("系统时间早于最近归档，暂不能发起新分析")
		}
	}
	for _, r := range l.Requests {
		at, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
		if at.After(now) {
			return out, errors.New("系统时间早于已记录请求，暂不能发起新分析")
		}
		if at.After(now.Add(-24 * time.Hour)) {
			out.Reserved.Requests++
			out.Reserved.InputBytes += len(r.Spec.Request.System) + len(r.Spec.Request.Input)
			out.Reserved.OutputTokens += r.Spec.Request.MaxOutputTokens
		}
	}
	for _, batch := range l.Batches {
		for i, part := range batch.Items {
			stamp := batch.CreatedAt
			if part.StartedAt != "" {
				stamp = part.StartedAt
			}
			at, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || at.After(now) {
				return out, errors.New("系统时间早于已记录请求，暂不能发起新分析")
			}
			if at.After(now.Add(-24 * time.Hour)) {
				out.Reserved = out.Reserved.add(requestQuota(batch.Spec.Requests[i]))
			}
		}
	}
	return out, nil
}

func (s *Store) analysisRead(ctx context.Context) (AnalysisLedger, error) {
	l := AnalysisLedger{Schema: 1, ProjectID: s.project.ID, Requests: []AnalysisRequest{}}
	ready, err := s.ready(false)
	if err != nil || !ready {
		return l, err
	}
	b, err := safeRead(ctx, filepath.Join(s.directory, "analysis.json"), maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if decodeAnalysis(b, &l) != nil || l.Schema != 1 || l.ProjectID != s.project.ID || l.Revision >= 1<<53 || l.recordCount() > maxAnalysisRecords || len(l.Batches) > maxAnalysisRecords || !l.limit().Valid() || !l.Archive.valid() {
		return l, ErrCorrupt
	}
	ids := map[string]bool{}
	for _, r := range l.Requests {
		if ids[r.ID] || validateAnalysisRequest(l.ProjectID, r) != nil {
			return l, ErrCorrupt
		}
		ids[r.ID] = true
	}
	for _, batch := range l.Batches {
		if ids[batch.ID] || batch.validate(l.ProjectID) != nil {
			return l, ErrCorrupt
		}
		ids[batch.ID] = true
	}
	return l, nil
}

func validateAnalysisRequest(projectID string, r AnalysisRequest) error {
	start, e1 := time.Parse(time.RFC3339Nano, r.StartedAt)
	deadline, e2 := time.Parse(time.RFC3339Nano, r.DeadlineAt)
	if !idOK(r.ID) || len(r.ID) > 64 || r.Spec.validate(projectID) != nil || r.SpecSHA != r.Spec.Digest() || e1 != nil || e2 != nil || !deadline.Equal(start.Add(45*time.Second)) || !r.Usage.ValidFor(r.Spec.Recipient) || !contains([]string{"running", "succeeded", "failed", "cancelled", "stale", "unknown"}, r.Status) || !analysisProblem(r.Problem) {
		return ErrCorrupt
	}
	if r.Status == "running" && (r.FinishedAt != "" || len(r.Advice) != 0 || r.NoteSaved || r.Usage.OpenRouterDebit != nil) || r.Status != "running" && !timeOK(r.FinishedAt) || r.Status == "succeeded" && (len(r.Advice) == 0 || len(r.Advice) > models.MaxGeneratedBytes || !json.Valid(r.Advice)) || r.Status != "succeeded" && (len(r.Advice) != 0 || r.NoteSaved) {
		return ErrCorrupt
	}
	return nil
}
func (s *Store) Analysis(ctx context.Context) (AnalysisLedger, error) {
	// Serialise public reads with atomic replacements. safeRead deliberately rejects
	// inode changes; without this lock normal progress writes can look like damage.
	ready, err := s.ready(false)
	if err != nil || !ready || !writesSupported() {
		return s.analysisRead(ctx)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisLedger{}, err
	}
	defer release()
	return s.analysisRead(ctx)
}
func analysisProblem(code string) bool {
	return contains([]string{"", "cancelled", "connection_changed", "project_changed", "unreliable_advice", "request_failed", "unknown_result", "expired"}, code)
}
func (s *Store) analysisWrite(ctx context.Context, l AnalysisLedger) error {
	if l.recordCount() > maxAnalysisRecords || !l.limit().Valid() || !l.Archive.valid() {
		return ErrBudget
	}
	for _, batch := range l.Batches {
		if err := batch.validate(l.ProjectID); err != nil {
			return err
		}
	}
	l.Revision++
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if len(b) > maxStateBytes {
		return ErrBudget
	}
	if err = s.atomic(ctx, filepath.Join(s.directory, "analysis.json"), append(b, '\n'), false); err != nil {
		return err
	}
	if s.afterAnalysisCommit != nil {
		return s.afterAnalysisCommit()
	}
	return nil
}

// ReserveAnalysis commits the one-shot intent BEFORE any network call. False
// means this ID already exists; the caller must never send it again.
func (s *Store) ReserveAnalysis(ctx context.Context, id string, spec AnalysisSpec, now time.Time) (AnalysisRequest, bool, error) {
	if !idOK(id) || len(id) > 64 || now.IsZero() {
		return AnalysisRequest{}, false, ErrAnalysisConsent
	}
	if err := spec.validate(s.project.ID); err != nil {
		return AnalysisRequest{}, false, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisRequest{}, false, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisRequest{}, false, err
	}
	for _, r := range l.Requests {
		if r.ID == id {
			if r.SpecSHA != spec.Digest() {
				return r, false, ErrOperationReused
			}
			return r, false, nil
		}
	}
	for _, b := range l.Batches {
		if b.ID == id {
			return AnalysisRequest{}, false, ErrOperationReused
		}
	}
	archived, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return AnalysisRequest{}, false, err
	}
	if found {
		if archived.Request == nil || archived.Request.SpecSHA != spec.Digest() {
			return AnalysisRequest{}, false, ErrOperationReused
		}
		return *archived.Request, false, nil
	}
	if l.batchBusy(now) {
		return AnalysisRequest{}, false, ErrAnalysisBusy
	}
	v, err := s.read(ctx)
	if err != nil {
		return AnalysisRequest{}, false, err
	}
	if !v.State.Enabled || v.State.Revision != spec.MemoryRevision || v.HeadSHA != spec.MemoryHead {
		return AnalysisRequest{}, false, ErrConflict
	}
	b, err := l.Budget(now)
	if err != nil {
		return AnalysisRequest{}, false, err
	}
	if b.RecordsRemaining < 1 || b.Reserved.Requests+1 > b.Limit.Requests || b.Reserved.InputBytes+len(spec.Request.System)+len(spec.Request.Input) > b.Limit.InputBytes || b.Reserved.OutputTokens+spec.Request.MaxOutputTokens > b.Limit.OutputTokens {
		return AnalysisRequest{}, false, ErrAnalysisBudget
	}
	r := AnalysisRequest{ID: id, Spec: spec, SpecSHA: spec.Digest(), StartedAt: stamp(now), DeadlineAt: stamp(now.Add(45 * time.Second)), Status: "running"}
	l.Requests = append(l.Requests, r)
	if err = s.analysisWrite(ctx, l); err != nil {
		return AnalysisRequest{}, false, err
	}
	return r, true, nil
}

// Completion does not refund reservations. A stale, unverified or late result
// cannot become a successful suggestion. Output has already passed the trusted
// controller's projectcontext validator; this ledger never executes it.
func (s *Store) FinishAnalysis(ctx context.Context, id, specSHA, status, problem string, advice json.RawMessage, usage models.Usage, now time.Time) (AnalysisRequest, error) {
	return s.finishAnalysis(ctx, id, specSHA, status, problem, advice, usage, now, nil)
}

// FinishAnalysisChecked validates external project inputs after acquiring the
// project lock. The callback must be read-only and must not acquire this lock.
// The controller also holds its recipient publication lock around this call.
func (s *Store) FinishAnalysisChecked(ctx context.Context, id, specSHA, status, problem string, advice json.RawMessage, usage models.Usage, check func() error) (AnalysisRequest, error) {
	if check == nil {
		return AnalysisRequest{}, ErrAnalysisConsent
	}
	return s.finishAnalysis(ctx, id, specSHA, status, problem, advice, usage, time.Now(), check)
}

func (s *Store) finishAnalysis(ctx context.Context, id, specSHA, status, problem string, advice json.RawMessage, usage models.Usage, now time.Time, check func() error) (AnalysisRequest, error) {
	if !contains([]string{"succeeded", "failed", "cancelled", "stale", "unknown"}, status) || !analysisProblem(problem) || now.IsZero() || !usage.Valid() || status == "succeeded" && (len(advice) == 0 || len(advice) > models.MaxGeneratedBytes || !json.Valid(advice)) || status != "succeeded" && len(advice) != 0 {
		return AnalysisRequest{}, ErrAnalysisConsent
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisRequest{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisRequest{}, err
	}
	for i := range l.Requests {
		r := &l.Requests[i]
		if r.ID != id {
			continue
		}
		if r.SpecSHA != specSHA {
			return AnalysisRequest{}, ErrOperationReused
		}
		if r.Status != "running" {
			return *r, nil
		}
		if !usage.ValidFor(r.Spec.Recipient) {
			return AnalysisRequest{}, ErrAnalysisConsent
		}
		if status == "succeeded" && check != nil {
			if e := check(); e != nil {
				status, problem, advice = "stale", "project_changed", nil
				if errors.Is(e, context.Canceled) {
					status, problem = "cancelled", "cancelled"
				}
			}
			now = time.Now()
		}
		start, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
		if now.Before(start) {
			return AnalysisRequest{}, ErrConflict
		}
		if status == "succeeded" {
			v, e := s.read(ctx)
			if e != nil {
				return AnalysisRequest{}, e
			}
			if !v.State.Enabled || v.State.Revision != r.Spec.MemoryRevision || v.HeadSHA != r.Spec.MemoryHead {
				status, problem, advice = "stale", "project_changed", nil
			}
			if r.EffectiveStatus(now) == "unknown" {
				status, problem, advice = "unknown", "unknown_result", nil
			}
		}
		r.Status, r.Problem, r.Advice, r.Usage, r.FinishedAt = status, problem, advice, usage, stamp(now)
		if err = s.analysisWrite(ctx, l); err != nil {
			return AnalysisRequest{}, err
		}
		return *r, nil
	}
	return AnalysisRequest{}, errors.New("没有找到这个分析请求")
}

func (s *Store) MarkAnalysisNoted(ctx context.Context, id, specSHA string) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return err
	}
	for i := range l.Requests {
		r := &l.Requests[i]
		if r.ID != id {
			continue
		}
		if r.SpecSHA != specSHA || r.Status != "succeeded" {
			return ErrConflict
		}
		if r.NoteSaved {
			return nil
		}
		v, e := s.read(ctx)
		if e != nil {
			return e
		}
		found := false
		for _, op := range v.State.Operations {
			if op.ID != "analysis-note-"+id {
				continue
			}
			record, _, err := s.readRecord(ctx, op.Revision)
			if err != nil {
				return err
			}
			want := analysisNote(*r)
			for _, event := range record.Events {
				if n := event.Note; n != nil && n.Kind == want.Kind && n.Text == want.Text && n.Agent == want.Agent && n.Model == want.Model && n.Evidence == want.Evidence && event.Source == "agent_declaration" {
					found = true
				}
			}
		}
		if !found {
			return ErrConflict
		}
		r.NoteSaved = true
		return s.analysisWrite(ctx, l)
	}
	archived, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return err
	}
	if found && archived.Request != nil && archived.Request.SpecSHA == specSHA && archived.Request.NoteSaved {
		return nil
	}
	return ErrConflict
}

func analysisNote(r AnalysisRequest) Note {
	var advice struct {
		Groups       []json.RawMessage `json:"groups"`
		UncertainIDs []string          `json:"uncertainIDs"`
	}
	_ = json.Unmarshal(r.Advice, &advice)
	return Note{Kind: "agent_proposal", Agent: "workshelf", Model: r.Spec.Recipient.ModelID, Text: fmt.Sprintf("模型根据生成时的文件记录提出了 %d 组整理建议，%d 项仍不确定。文件没有移动；完整输入和建议保存在本项目的分析记录中。", len(advice.Groups), len(advice.UncertainIDs)), Evidence: "analysis:" + r.ID + "; context:" + r.Spec.ContextSHA}
}

// Retry only the local memory append, never the model call. Its stable operation
// ID lets a crash between event publication and NoteSaved be recovered safely.
func (s *Store) RecordAnalysisNote(ctx context.Context, id, specSHA string, now time.Time) error {
	return s.recordAnalysisNote(ctx, id, specSHA, now, false)
}
func (s *Store) RecordAnalysisNoteIfIdle(ctx context.Context, id, specSHA string, now time.Time) error {
	return s.recordAnalysisNote(ctx, id, specSHA, now, true)
}
func (s *Store) recordAnalysisNote(ctx context.Context, id, specSHA string, now time.Time, requireIdle bool) error {
	l, err := s.Analysis(ctx)
	if err != nil {
		return err
	}
	for _, r := range l.Requests {
		if r.ID != id {
			continue
		}
		if r.SpecSHA != specSHA || r.Status != "succeeded" {
			return ErrConflict
		}
		if r.NoteSaved {
			return nil
		}
		v, err := s.Read(ctx)
		if err != nil {
			return err
		}
		var check func() error
		if requireIdle {
			check = func() error { return s.analysisIdle(ctx, now) }
		}
		if _, err = s.addNoteChecked(ctx, v.State.Revision, "analysis-note-"+id, analysisNote(r), now, check); err != nil {
			return err
		}
		return s.MarkAnalysisNoted(ctx, id, specSHA)
	}
	archived, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return err
	}
	if found && archived.Request != nil && archived.Request.SpecSHA == specSHA && archived.Request.NoteSaved {
		return nil
	}
	return ErrConflict
}
