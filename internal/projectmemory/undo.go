package projectmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// UndoReference names the original ledger event and the filesystem facts that
// the trusted host revalidated from its original execution/recovery journal.
// A digest or model declaration alone is neither proof nor file authority.
type UndoReference struct {
	OperationID string `json:"operationID"`
	Claim       string `json:"claim"`
	Kind        string `json:"kind"` // placement or reconciliation
	EventID     string `json:"eventID"`
	ProofSHA    string `json:"proofSHA"`
	FileID      string `json:"fileID"`
	From        Fact   `json:"from"`
	To          Fact   `json:"to"`
}

// UndoEvidence is accepted only after an actual, verified local reverse move.
// It records history; it never authorizes or performs a filesystem operation.
type UndoEvidence struct {
	Project        Project       `json:"project"`
	UndoID         string        `json:"undoID"`
	Claim          string        `json:"claim"`
	PlanSHA        string        `json:"planSHA"`
	PhysicalSHA    string        `json:"physicalSHA"`
	Original       UndoReference `json:"original"`
	Scope          Scope         `json:"scope"`
	ExpectedFile   File          `json:"expectedFile"`
	Before         Fact          `json:"before"`
	After          Fact          `json:"after"`
	MemoryRevision uint64        `json:"memoryRevision"`
	OccurredAt     string        `json:"occurredAt"`
}

type UndoFact struct {
	Evidence    UndoEvidence      `json:"evidence"`
	Disposition string            `json:"disposition"`
	Reason      string            `json:"reason"`
	Changes     []PlacementChange `json:"changes"`
}

type UndoSummary struct {
	EventID           string `json:"eventID"`
	UndoID            string `json:"undoID"`
	Claim             string `json:"claim"`
	OriginalOperation string `json:"originalOperation"`
	OriginalClaim     string `json:"originalClaim"`
	OriginalKind      string `json:"originalKind"`
	OriginalEvent     string `json:"originalEvent"`
	OriginalProofSHA  string `json:"originalProofSHA"`
	FileID            string `json:"fileID"`
	From              string `json:"from"`
	To                string `json:"to"`
	OccurredAt        string `json:"occurredAt"`
	PlanSHA           string `json:"planSHA"`
	PhysicalSHA       string `json:"physicalSHA"`
	Disposition       string `json:"disposition"`
}

func UndoOperationID(undo, claim string) string {
	return "undo-" + digestJSON(struct{ Undo, Claim string }{undo, claim})
}

func (e UndoEvidence) Validate() error {
	o := e.Original
	if !idOK(e.Project.ID) || !pathOK(e.Project.Path) || !textOK(e.Project.Name, 1200) || !idOK(e.UndoID) || !idOK(e.Claim) || !hashOK(e.PlanSHA) || !hashOK(e.PhysicalSHA) || !timeOK(e.OccurredAt) || e.MemoryRevision == 0 || e.MemoryRevision >= 1<<53 || e.Scope.Validate() != nil {
		return ErrCorrupt
	}
	if !idOK(o.OperationID) || !idOK(o.Claim) || !contains([]string{"placement", "reconciliation"}, o.Kind) || !textOK(o.EventID, 160) || !hashOK(o.ProofSHA) || !idOK(o.FileID) || o.FileID != e.ExpectedFile.ID {
		return ErrCorrupt
	}
	for _, f := range []Fact{o.From, o.To, e.Before, e.After} {
		if f.validate(e.Scope) != nil || f.Kind != "file" || f.HashState != "verified" || f.ObjectKey == "" {
			return ErrCorrupt
		}
	}
	// Only the exact inverse of the original single-object move is admissible.
	// Original external members remain supported without relaxing Placement.
	from := o.From
	from.Path = o.To.Path
	after := e.Before
	after.Path = o.From.Path
	if o.From.Path == o.To.Path || !within(e.Project.Path, o.To.Path) || from != o.To || e.Before != o.To || e.After != after {
		return ErrCorrupt
	}
	f := e.ExpectedFile
	if f.Status != "present" || !timeOK(f.FirstObservedAt) || !textOK(f.LastEvent, 160) || f.Fact.validate(e.Scope) != nil || f.Fact.Path != e.Before.Path || f.Fact.Kind != "file" || f.Fact.ObjectKey != e.Before.ObjectKey || f.Fact.Size != e.Before.Size || f.Fact.ModifiedAt != e.Before.ModifiedAt || f.Fact.HashState == "verified" && f.Fact.SHA256 != e.Before.SHA256 {
		return ErrCorrupt
	}
	return nil
}

func undoReferenceMatches(s State, o UndoReference) bool {
	matches := 0
	if o.Kind == "placement" {
		for _, p := range s.Placements {
			if p.OperationID == o.OperationID && p.Claim == o.Claim && p.EventID == o.EventID && p.PhysicalSHA == o.ProofSHA && p.Outcome == "moved" && p.SourceFileID == o.FileID && p.TargetFileID == o.FileID && p.From == o.From.Path && p.To == o.To.Path {
				matches++
			}
		}
	} else if o.Kind == "reconciliation" {
		for _, p := range s.Reconciliations {
			if p.OperationID == o.OperationID && p.Claim == o.Claim && p.EventID == o.EventID && p.ReviewSHA == o.ProofSHA && p.Outcome == "at_target" && p.SourceFileID == o.FileID && p.TargetFileID == o.FileID && p.From == o.From.Path && p.To == o.To.Path {
				matches++
			}
		}
	}
	return matches == 1
}

func deriveUndo(s State, e UndoEvidence) (UndoFact, error) {
	if e.Validate() != nil || e.Project != s.Project || e.MemoryRevision > s.Revision || !undoReferenceMatches(s, e.Original) {
		return UndoFact{}, ErrCorrupt
	}
	for _, u := range s.Undos {
		if u.OriginalOperation == e.Original.OperationID && u.OriginalClaim == e.Original.Claim {
			return UndoFact{}, ErrOperationReused
		}
	}
	result := UndoFact{Evidence: e, Disposition: "history_only", Reason: "已保留实际恢复记录；当前目录或追踪状态已有变化，不覆盖新记录", Changes: []PlacementChange{}}
	if !s.Enabled || s.Scope.Digest() != e.Scope.Digest() {
		return result, nil
	}
	var source *File
	for _, file := range s.Files {
		if file.ID == e.Original.FileID {
			f := file
			source = &f
		} else if file.Fact.ObjectKey == e.Before.ObjectKey || file.Status == "present" && file.Fact.Path == e.After.Path {
			return result, nil
		}
	}
	if source == nil {
		return result, nil
	}
	if source.Status == "present" && source.Fact == e.After {
		result.Disposition, result.Reason = "already_observed", "目录已观察到准确恢复位置；保留观察事实并补充本次撤销来源"
		return result, nil
	}
	if s.Revision != e.MemoryRevision || !reflect.DeepEqual(*source, e.ExpectedFile) {
		return result, nil
	}
	after := *source
	after.Fact, after.LastEvent = e.After, ""
	result.Disposition, result.Reason = "applied", "依据已核实的本次撤销回执，将原文件身份恢复到原登记位置"
	result.Changes = []PlacementChange{{Before: source, After: after}}
	return result, nil
}

func (s *Store) RecordUndo(ctx context.Context, revision uint64, evidence UndoEvidence, recordedAt time.Time) (Result, error) {
	data, err := json.Marshal(evidence)
	if err != nil {
		return Result{}, err
	}
	var owned UndoEvidence
	if json.Unmarshal(data, &owned) != nil || owned.Validate() != nil {
		return Result{}, ErrCorrupt
	}
	return s.update(ctx, revision, UndoOperationID(owned.UndoID, owned.Claim), "undo", owned, func(previous State) ([]Event, error) {
		fact, err := deriveUndo(previous, owned)
		if err != nil {
			return nil, err
		}
		return []Event{{Kind: "placement_undone", ObservedAt: stamp(recordedAt), Source: "local_execution", Actor: "workshelf", Reason: fact.Reason, Undo: &fact}}, nil
	})
}

func applyUndoEvent(s *State, files map[string]File, event Event) error {
	if event.Source != "local_execution" || event.Actor != "workshelf" || event.Undo == nil || event.Before != nil || event.After != nil || event.Scope != nil || event.Enabled != nil || event.Automatic != nil || event.Observation != nil || event.Note != nil || event.Placement != nil || event.Reconciliation != nil {
		return ErrCorrupt
	}
	previous := *s
	previous.Revision--
	previous.Files = make([]File, 0, len(files))
	for _, file := range files {
		previous.Files = append(previous.Files, file)
	}
	expected, err := deriveUndo(previous, event.Undo.Evidence)
	if err != nil {
		return err
	}
	for i := range expected.Changes {
		expected.Changes[i].After.LastEvent = event.ID
	}
	if !reflect.DeepEqual(expected, *event.Undo) || event.Reason != expected.Reason {
		return ErrCorrupt
	}
	for _, change := range expected.Changes {
		old, exists := files[change.After.ID]
		if change.Before == nil || !exists || !reflect.DeepEqual(old, *change.Before) {
			return ErrCorrupt
		}
		files[change.After.ID] = change.After
	}
	e, o := expected.Evidence, expected.Evidence.Original
	s.Undos = append(s.Undos, UndoSummary{EventID: event.ID, UndoID: e.UndoID, Claim: e.Claim, OriginalOperation: o.OperationID, OriginalClaim: o.Claim, OriginalKind: o.Kind, OriginalEvent: o.EventID, OriginalProofSHA: o.ProofSHA, FileID: o.FileID, From: e.Before.Path, To: e.After.Path, OccurredAt: e.OccurredAt, PlanSHA: e.PlanSHA, PhysicalSHA: e.PhysicalSHA, Disposition: expected.Disposition})
	return nil
}

func (u UndoSummary) validate() error {
	if !textOK(u.EventID, 160) || !idOK(u.UndoID) || !idOK(u.Claim) || !idOK(u.OriginalOperation) || !idOK(u.OriginalClaim) || !contains([]string{"placement", "reconciliation"}, u.OriginalKind) || !textOK(u.OriginalEvent, 160) || !hashOK(u.OriginalProofSHA) || !idOK(u.FileID) || !pathOK(u.From) || !pathOK(u.To) || u.From == u.To || !timeOK(u.OccurredAt) || !hashOK(u.PlanSHA) || !hashOK(u.PhysicalSHA) || !contains([]string{"applied", "already_observed", "history_only"}, u.Disposition) {
		return ErrCorrupt
	}
	return nil
}

func undoLabel(u UndoSummary) string {
	disposition := map[string]string{"applied": "已更新项目目录", "already_observed": "保留已有观察，已补充撤销来源", "history_only": "仅补充历史，保留当前目录"}[u.Disposition]
	return fmt.Sprintf("%s · 已执行反向移动 · %s", u.OccurredAt, disposition)
}
