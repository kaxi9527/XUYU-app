package projectmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"time"
)

// ReconciliationEvidence is supplied by the trusted local recovery service
// after a user confirms a fresh, exact review. It is not proof of who performed
// a prior filesystem action and is never a browser-supplied file operation.
type ReconciliationEvidence struct {
	Project        Project `json:"project"`
	OperationID    string  `json:"operationID"`
	Claim          string  `json:"claim"`
	IntentSHA      string  `json:"intentSHA"`
	ReviewSHA      string  `json:"reviewSHA"`
	JournalHead    string  `json:"journalHead"`
	MemoryRevision uint64  `json:"memoryRevision"`
	MemoryHead     string  `json:"memoryHead"`
	Scope          Scope   `json:"scope"`
	ExpectedFile   File    `json:"expectedFile"`
	Before         Fact    `json:"before"`
	TargetPath     string  `json:"targetPath"`
	CopyObject     string  `json:"copyObject,omitempty"`
	Source         *Fact   `json:"source,omitempty"`
	Target         *Fact   `json:"target,omitempty"`
	Temporary      *Fact   `json:"temporary,omitempty"`
	Outcome        string  `json:"outcome"`
	ClaimedAt      string  `json:"claimedAt"`
	ObservedAt     string  `json:"observedAt"`
	ConfirmedAt    string  `json:"confirmedAt"`
	ConfirmedBy    string  `json:"confirmedBy"`
	Performer      string  `json:"performer"`
}

type ReconciliationFact struct {
	Evidence     ReconciliationEvidence `json:"evidence"`
	TargetFileID string                 `json:"targetFileID"`
	Disposition  string                 `json:"disposition"`
	Reason       string                 `json:"reason"`
}

type ReconciliationSummary struct {
	EventID       string `json:"eventID"`
	OperationID   string `json:"operationID"`
	Claim         string `json:"claim"`
	SourceFileID  string `json:"sourceFileID"`
	TargetFileID  string `json:"targetFileID"`
	From          string `json:"from"`
	To            string `json:"to"`
	Outcome       string `json:"outcome"`
	Disposition   string `json:"disposition"`
	ObservedAt    string `json:"observedAt"`
	ConfirmedAt   string `json:"confirmedAt"`
	ReviewSHA     string `json:"reviewSHA"`
	TemporaryPath string `json:"temporaryPath,omitempty"`
}

func ReconciliationOperationID(operation, claim, review string) string {
	return "reconciliation-" + digestJSON(struct{ Operation, Claim, Review string }{operation, claim, review})
}

func (e ReconciliationEvidence) Validate() error {
	if !idOK(e.Project.ID) || !pathOK(e.Project.Path) || !textOK(e.Project.Name, 1200) || !idOK(e.OperationID) || !idOK(e.Claim) || !hashOK(e.IntentSHA) || !hashOK(e.ReviewSHA) || !hashOK(e.JournalHead) || !hashOK(e.MemoryHead) || e.MemoryRevision == 0 || e.MemoryRevision >= 1<<53 || e.Scope.Validate() != nil || !idOK(e.ExpectedFile.ID) || !timeOK(e.ClaimedAt) || !timeOK(e.ObservedAt) || !timeOK(e.ConfirmedAt) || e.ConfirmedBy != "local_user" || e.Performer != "unknown" {
		return ErrCorrupt
	}
	claimed, _ := time.Parse(time.RFC3339Nano, e.ClaimedAt)
	observed, _ := time.Parse(time.RFC3339Nano, e.ObservedAt)
	confirmed, _ := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
	if observed.Before(claimed) || confirmed.Before(observed) || e.Before.validate(e.Scope) != nil || e.Before.Kind != "file" || e.Before.HashState != "verified" || e.Before.ObjectKey == "" || !pathOK(e.TargetPath) || !e.Scope.Allows(e.TargetPath) || !within(e.Project.Path, e.TargetPath) || e.TargetPath == e.Before.Path || e.ExpectedFile.Fact.validate(Scope{Files: []string{e.ExpectedFile.Fact.Path}}) != nil || !contains([]string{"present", "missing", "untracked"}, e.ExpectedFile.Status) || !timeOK(e.ExpectedFile.FirstObservedAt) || !textOK(e.ExpectedFile.LastEvent, 160) {
		return ErrCorrupt
	}
	for _, fact := range []*Fact{e.Source, e.Target, e.Temporary} {
		if fact != nil && (fact.validate(e.Scope) != nil || fact.Kind != "file" || fact.HashState != "verified" || fact.ObjectKey == "") {
			return ErrCorrupt
		}
	}
	if e.Source != nil && e.Source.Path != e.Before.Path || e.Target != nil && e.Target.Path != e.TargetPath || e.Temporary != nil && e.Temporary.Path != filepath.Join(filepath.Dir(e.TargetPath), ".workshelf-copy-"+e.Claim) {
		return ErrCorrupt
	}
	match := func(f *Fact, key string) bool {
		return f != nil && f.ObjectKey == key && f.Size == e.Before.Size && f.SHA256 == e.Before.SHA256 && f.ModifiedAt == e.Before.ModifiedAt
	}
	switch e.Outcome {
	case "at_source":
		if !match(e.Source, e.Before.ObjectKey) || e.Target != nil || e.Temporary != nil || e.CopyObject != "" {
			return ErrCorrupt
		}
	case "at_target":
		if e.Source != nil || !match(e.Target, e.Before.ObjectKey) || e.Temporary != nil || e.CopyObject != "" {
			return ErrCorrupt
		}
	case "copy_published":
		if e.CopyObject == "" || e.CopyObject == e.Before.ObjectKey || !match(e.Source, e.Before.ObjectKey) || !match(e.Target, e.CopyObject) || e.Temporary != nil {
			return ErrCorrupt
		}
	case "copy_unpublished":
		if !match(e.Source, e.Before.ObjectKey) || e.Target != nil {
			return ErrCorrupt
		}
	default:
		return ErrCorrupt
	}
	return nil
}

func reconciliationChanges(previous State, e ReconciliationEvidence) (ReconciliationFact, []PlacementChange, error) {
	if e.Validate() != nil || e.Project != previous.Project || e.MemoryRevision > previous.Revision {
		return ReconciliationFact{}, nil, ErrCorrupt
	}
	for _, old := range previous.Reconciliations {
		if old.OperationID == e.OperationID && old.Claim == e.Claim {
			return ReconciliationFact{}, nil, ErrOperationReused
		}
	}
	fact := ReconciliationFact{Evidence: e, TargetFileID: e.ExpectedFile.ID, Disposition: "history_only", Reason: "核对历史已保存；当前目录或权限已有更新，保留最新记录。原动作操作者未知"}
	if e.Outcome == "copy_published" {
		fact.TargetFileID = "recoveredcopy_" + digestJSON(struct{ Project, Operation, Claim string }{e.Project.ID, e.OperationID, e.Claim})[:40]
	}
	if !previous.Enabled || previous.Scope.Digest() != e.Scope.Digest() || previous.Revision != e.MemoryRevision {
		return fact, nil, nil
	}
	var source *File
	for _, f := range previous.Files {
		if f.ID == e.ExpectedFile.ID {
			copy := f
			source = &copy
			break
		}
	}
	if source == nil {
		return ReconciliationFact{}, nil, ErrCorrupt
	}
	if !reflect.DeepEqual(*source, e.ExpectedFile) || source.Status == "untracked" || source.Fact.ObjectKey != e.Before.ObjectKey || source.Fact.Path != e.Before.Path && source.Fact.Path != e.TargetPath {
		return fact, nil, nil
	}
	// Identity ambiguity anywhere in the project cannot be resolved by checking
	// just these two paths. In particular, never rewrite a third path.
	for _, f := range previous.Files {
		if f.ID != source.ID && f.Fact.ObjectKey == e.Before.ObjectKey {
			return fact, nil, nil
		}
	}
	var desired Fact
	if e.Outcome == "at_target" || e.Outcome == "copy_published" {
		desired = *e.Target
	} else {
		desired = *e.Source
	}
	if e.Outcome == "copy_published" {
		for _, f := range previous.Files {
			if f.ID == source.ID {
				continue
			}
			if f.Fact.ObjectKey == desired.ObjectKey || f.Status == "present" && f.Fact.Path == desired.Path || f.ID == fact.TargetFileID {
				if f.Status == "present" && f.Fact == desired {
					// Only reuse an identity if no other record claims this object.
					for _, other := range previous.Files {
						if other.ID != f.ID && other.Fact.ObjectKey == desired.ObjectKey {
							return fact, nil, nil
						}
					}
					fact.TargetFileID, fact.Disposition, fact.Reason = f.ID, "already_observed", "同一副本已有目录记录；只补充用户核对及计划来源，原动作操作者未知"
				}
				return fact, nil, nil
			}
		}
		fact.Disposition, fact.Reason = "applied", "依据用户确认的精确位置核对登记副本；原动作操作者未知"
		return fact, []PlacementChange{{After: File{ID: fact.TargetFileID, Fact: desired, Status: "present", FirstObservedAt: e.ObservedAt}}}, nil
	}
	for _, f := range previous.Files {
		if f.ID != source.ID && f.Status == "present" && f.Fact.Path == desired.Path {
			return fact, nil, nil
		}
	}
	if source.Status == "present" && source.Fact == desired {
		fact.Disposition, fact.Reason = "already_observed", "当前目录与精确位置核对一致；只补充用户确认，原动作操作者未知"
		return fact, nil, nil
	}
	after := *source
	after.Fact, after.Status, after.LastEvent = desired, "present", ""
	fact.Disposition, fact.Reason = "applied", "依据用户确认的精确位置核对更新原文件身份；原动作操作者未知"
	return fact, []PlacementChange{{Before: source, After: after}}, nil
}

func reconciliationEvents(previous State, e ReconciliationEvidence, recordedAt string) ([]Event, error) {
	fact, changes, err := reconciliationChanges(previous, e)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	for _, change := range changes {
		kind := "first_observed"
		if change.Before != nil {
			kind = "metadata_changed"
			if change.Before.Fact.Path != change.After.Fact.Path {
				kind = "location_changed"
			} else if change.Before.Status != "present" {
				kind = "seen_again"
			}
		}
		after := change.After
		events = append(events, Event{Kind: kind, ObservedAt: e.ObservedAt, Source: "filesystem_observation", Actor: "unknown", Reason: "用户确认了原计划精确位置的核对结果；未推断原文件动作的操作者", Before: change.Before, After: &after})
	}
	events = append(events, Event{Kind: "placement_reconciled", ObservedAt: recordedAt, Source: "local_user", Actor: "local_user", Reason: fact.Reason, Reconciliation: &fact})
	return events, nil
}

func validateReconciliationRecord(previous State, r Record) error {
	last := r.Events[len(r.Events)-1]
	if last.Kind != "placement_reconciled" || last.Reconciliation == nil {
		return ErrCorrupt
	}
	e := last.Reconciliation.Evidence
	if r.OperationID != ReconciliationOperationID(e.OperationID, e.Claim, e.ReviewSHA) {
		return ErrCorrupt
	}
	expected, err := reconciliationEvents(previous, e, last.ObservedAt)
	if err != nil {
		return err
	}
	for i := range expected {
		expected[i].ID = fmt.Sprintf("%d:%d", r.Revision, i+1)
		if expected[i].After != nil {
			expected[i].After.LastEvent = expected[i].ID
		}
	}
	if !reflect.DeepEqual(expected, r.Events) {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) RecordReconciliation(ctx context.Context, revision uint64, e ReconciliationEvidence, recordedAt time.Time) (Result, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return Result{}, err
	}
	var owned ReconciliationEvidence
	if json.Unmarshal(data, &owned) != nil || owned.Validate() != nil || owned.Project != s.project || recordedAt.IsZero() {
		return Result{}, ErrCorrupt
	}
	return s.update(ctx, revision, ReconciliationOperationID(owned.OperationID, owned.Claim, owned.ReviewSHA), "reconciliation", owned, func(previous State) ([]Event, error) { return reconciliationEvents(previous, owned, stamp(recordedAt)) })
}

func reconciliationSummary(event Event) ReconciliationSummary {
	f := event.Reconciliation
	e := f.Evidence
	s := ReconciliationSummary{EventID: event.ID, OperationID: e.OperationID, Claim: e.Claim, SourceFileID: e.ExpectedFile.ID, TargetFileID: f.TargetFileID, From: e.Before.Path, To: e.TargetPath, Outcome: e.Outcome, Disposition: f.Disposition, ObservedAt: e.ObservedAt, ConfirmedAt: e.ConfirmedAt, ReviewSHA: e.ReviewSHA}
	if e.Temporary != nil {
		s.TemporaryPath = e.Temporary.Path
	}
	return s
}
