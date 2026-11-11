package projectplacement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

type RecoveryObject struct {
	Path      string              `json:"path"`
	Fact      *projectmemory.Fact `json:"fact,omitempty"`
	Mode      uint32              `json:"mode,omitempty"`
	ChangedAt string              `json:"changedAt,omitempty"`
}

type RecoverySnapshot struct {
	OperationID    string             `json:"operationID"`
	IntentSHA      string             `json:"intentSHA"`
	JournalHead    string             `json:"journalHead"`
	FileID         string             `json:"fileID"`
	Claim          string             `json:"claim"`
	Stage          string             `json:"stage"`
	ClaimedAt      string             `json:"claimedAt"`
	ObservedAt     string             `json:"observedAt"`
	ExpiresAt      string             `json:"expiresAt"`
	MemoryRevision uint64             `json:"memoryRevision"`
	MemoryHead     string             `json:"memoryHead"`
	ExpectedFile   projectmemory.File `json:"expectedFile"`
	Source         RecoveryObject     `json:"source"`
	Target         RecoveryObject     `json:"target"`
	Temporary      *RecoveryObject    `json:"temporary,omitempty"`
	CopyObject     string             `json:"copyObject,omitempty"`
	Directories    []Directory        `json:"directories"`
	Outcome        string             `json:"outcome"`
	Reason         string             `json:"reason"`
}

// Only the trusted host holds this value. A future RPC keeps it in its own
// token store, accepting only the displayed review ID/SHA from the browser.
type RecoveryPreview struct {
	snapshot RecoverySnapshot
	sha      string
}

func (p RecoveryPreview) SHA256() string { return p.sha }
func (p RecoveryPreview) Snapshot() RecoverySnapshot {
	data, _ := json.Marshal(p.snapshot)
	var out RecoverySnapshot
	_ = json.Unmarshal(data, &out)
	return out
}

type RecoveryDecision struct {
	Review      RecoverySnapshot `json:"review"`
	ReviewSHA   string           `json:"reviewSHA"`
	ConfirmedAt string           `json:"confirmedAt"`
}

func (e *Executor) ReviewRecovery(ctx context.Context, id, intentSHA string) (RecoveryPreview, error) {
	return e.reviewRecovery(ctx, id, intentSHA)
}
func (e *Executor) ConfirmRecovery(ctx context.Context, preview RecoveryPreview) (JournalView, error) {
	return e.confirmRecovery(ctx, preview)
}

func recoveryOutcome(row Row, source, target RecoveryObject, temporary *RecoveryObject, copyObject string) (string, string) {
	match := func(f *projectmemory.Fact, object string) bool {
		return f != nil && f.Path != "" && f.ObjectKey == object && f.Kind == "file" && f.HashState == "verified" && f.SHA256 == row.Before.SHA256 && f.Size == row.Before.Size && f.ModifiedAt == row.Before.ModifiedAt
	}
	tempPresent := temporary != nil && temporary.Fact != nil
	if row.Action == "move" {
		if match(source.Fact, row.Object) && target.Fact == nil {
			return "at_source", "原文件仍在原处，目标没有文件；确认核对后停止本次计划"
		}
		if source.Fact == nil && match(target.Fact, row.Object) {
			return "at_target", "原文件对象位于目标，源位置为空；原动作操作者与具体发生时间未知"
		}
	} else if row.Action == "copy" {
		if match(source.Fact, row.Object) && target.Fact == nil {
			reason := "目标尚未发布，原文件仍在；确认核对不会再次复制"
			if tempPresent {
				reason += "；暂存位置存在对象，保留现场，不据名字认定归属或删除"
			}
			return "copy_unpublished", reason
		}
		if match(source.Fact, row.Object) && copyObject != "" && match(target.Fact, copyObject) && !tempPresent {
			return "copy_published", "原件和与持久暂存对象证据相符的副本均在；原动作操作者未知"
		}
	}
	return "conflict", "文件对象、内容或位置不能唯一核对；保留现场，请先处理冲突后重新核对"
}

func validateRecovery(v JournalView, d RecoveryDecision) error {
	p := d.Review
	index := itemIndex(v, p.FileID)
	row, ok := selectedRow(v, p.FileID)
	if !ok || index < 0 || !goodSHA(d.ReviewSHA) || digestJSON(p) != d.ReviewSHA || p.OperationID != v.Intent.ID || p.IntentSHA != v.IntentSHA || p.JournalHead != v.HeadSHA || p.Claim != v.Items[index].Claim || p.Stage != v.Items[index].Stage || p.Stage != "claimed" && p.Stage != "uncertain" || p.ClaimedAt != v.Items[index].ClaimedAt || p.MemoryRevision == 0 || p.MemoryRevision >= 1<<53 || !goodSHA(p.MemoryHead) || p.ExpectedFile.ID != p.FileID || p.Source.Path != row.Source || p.Target.Path != row.Target || !validTime(p.ObservedAt) || !validTime(p.ExpiresAt) || !validTime(d.ConfirmedAt) || len(p.Directories) == 0 || len(p.Directories) > 512 {
		return ErrConsent
	}
	observed, _ := time.Parse(time.RFC3339Nano, p.ObservedAt)
	expires, _ := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	confirmed, _ := time.Parse(time.RFC3339Nano, d.ConfirmedAt)
	claimed, _ := time.Parse(time.RFC3339Nano, p.ClaimedAt)
	if observed.Before(claimed) || expires != observed.Add(10*time.Minute) || confirmed.Before(observed) || !confirmed.Before(expires) {
		return ErrConsent
	}
	copyObject := ""
	if v.Items[index].Copy != nil {
		copyObject = v.Items[index].Copy.Object
	}
	if p.CopyObject != copyObject || row.Action == "move" && p.Temporary != nil || row.Action == "copy" && (p.Temporary == nil || p.Temporary.Path != copyTemporary(row, p.Claim)) {
		return ErrConsent
	}
	objects := []RecoveryObject{p.Source, p.Target}
	if p.Temporary != nil {
		objects = append(objects, *p.Temporary)
	}
	for _, o := range objects {
		if o.Fact != nil && (o.Fact.Path != o.Path || o.Fact.Kind != "file" || o.Fact.ObjectKey == "" || o.Fact.HashState != "verified" || !goodSHA(o.Fact.SHA256) || o.Fact.Size < 0 || !validTime(o.Fact.ModifiedAt) || o.ChangedAt == "") {
			return ErrConsent
		}
	}
	outcome, reason := recoveryOutcome(row, p.Source, p.Target, p.Temporary, p.CopyObject)
	if outcome == "conflict" || p.Outcome != outcome || p.Reason != reason {
		return ErrReconcile
	}
	return nil
}

func (e *Executor) recoveryAuthority(ctx context.Context, v JournalView) (projectmemory.View, error) {
	data, _ := json.Marshal(v.Intent)
	var intent Intent
	if json.Unmarshal(data, &intent) != nil {
		return projectmemory.View{}, ErrJournal
	}
	current, err := e.current(ctx, intent)
	if err != nil {
		return projectmemory.View{}, err
	}
	if !reflect.DeepEqual(current.Authority, v.Intent.Preview.Authority) {
		return projectmemory.View{}, ErrConsent
	}
	memory, err := e.memory.Read(ctx)
	if err != nil {
		return projectmemory.View{}, err
	}
	if memory.State.Project != current.Authority.Project || !memory.State.Enabled || memory.State.Scope.Digest() != current.Authority.Scope.Digest() {
		return projectmemory.View{}, ErrConsent
	}
	return memory, nil
}

func recoveryEvidence(v JournalView, item ItemState) (projectmemory.ReconciliationEvidence, error) {
	if item.Recovery == nil || item.Stage != "reconciliation_pending" {
		return projectmemory.ReconciliationEvidence{}, ErrJournal
	}
	d := item.Recovery
	p := d.Review
	row, ok := selectedRow(v, item.FileID)
	if !ok {
		return projectmemory.ReconciliationEvidence{}, ErrJournal
	}
	e := projectmemory.ReconciliationEvidence{Project: v.Intent.Preview.Authority.Project, OperationID: v.Intent.ID, Claim: item.Claim, IntentSHA: v.IntentSHA, ReviewSHA: d.ReviewSHA, JournalHead: p.JournalHead, MemoryRevision: p.MemoryRevision, MemoryHead: p.MemoryHead, Scope: v.Intent.Preview.Authority.Scope, ExpectedFile: p.ExpectedFile, Before: row.Before, TargetPath: row.Target, CopyObject: p.CopyObject, Source: p.Source.Fact, Target: p.Target.Fact, Outcome: p.Outcome, ClaimedAt: p.ClaimedAt, ObservedAt: p.ObservedAt, ConfirmedAt: d.ConfirmedAt, ConfirmedBy: "local_user", Performer: "unknown"}
	if p.Temporary != nil {
		e.Temporary = p.Temporary.Fact
	}
	return e, nil
}

func (e *Executor) saveRecoveryMemory(s *journalSession, item ItemState) error {
	evidence, err := recoveryEvidence(s.view, item)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = e.fail("before_recovery_memory"); err != nil {
		return err
	}
	var saved projectmemory.Result
	for attempt := 0; attempt < 4; attempt++ {
		current, readErr := e.memory.Read(ctx)
		if readErr != nil {
			return readErr
		}
		saved, err = e.memory.RecordReconciliation(ctx, current.State.Revision, evidence, time.Now())
		if !errors.Is(err, projectmemory.ErrConflict) {
			break
		}
	}
	if err != nil {
		return err
	}
	if err = e.fail("after_recovery_memory"); err != nil {
		return err
	}
	if saved.ProjectionPending || !goodSHA(saved.RecordSHA) || len(saved.Record.Events) == 0 {
		return ErrReconcile
	}
	last := saved.Record.Events[len(saved.Record.Events)-1]
	if last.Reconciliation == nil {
		return ErrReconcile
	}
	return s.append(journalRecord{Kind: "recovery_memory_saved", At: time.Now().UTC().Format(time.RFC3339Nano), FileID: item.FileID, Claim: item.Claim, Memory: &MemoryReceipt{ProjectID: evidence.Project.ID, Operation: saved.Record.OperationID, Revision: saved.Record.Revision, RecordSHA: saved.RecordSHA, EventID: last.ID}})
}
