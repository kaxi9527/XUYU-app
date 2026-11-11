package projectmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// PlacementEvidence is supplied only by the trusted local executor after its
// confirmed intent and physical receipt have been verified. It is not an RPC
// input, an agent declaration, or permission to perform filesystem operations.
type PlacementEvidence struct {
	Project         Project `json:"project"`
	OperationID     string  `json:"operationID"`
	Claim           string  `json:"claim"`
	IntentSHA       string  `json:"intentSHA"`
	PhysicalSHA     string  `json:"physicalSHA"`
	Outcome         string  `json:"outcome"`
	ExpectedFile    File    `json:"expectedFile"`
	Before          Fact    `json:"before"`
	After           Fact    `json:"after"`
	SourceObject    string  `json:"sourceObject"`
	TargetObject    string  `json:"targetObject"`
	SourceParent    string  `json:"sourceParent"`
	TargetParent    string  `json:"targetParent"`
	OccurredAt      string  `json:"occurredAt"`
	MemoryRevision  uint64  `json:"memoryRevision"`
	AuthorizedScope Scope   `json:"authorizedScope"`
	PolicyVersion   int     `json:"policyVersion"`
}

type PlacementChange struct {
	Before *File `json:"before,omitempty"`
	After  File  `json:"after"`
}

type PlacementFact struct {
	Evidence     PlacementEvidence `json:"evidence"`
	TargetFileID string            `json:"targetFileID"`
	Disposition  string            `json:"disposition"`
	Reason       string            `json:"reason"`
	Changes      []PlacementChange `json:"changes"`
}

type PlacementSummary struct {
	EventID      string `json:"eventID"`
	OperationID  string `json:"operationID"`
	Claim        string `json:"claim"`
	SourceFileID string `json:"sourceFileID"`
	TargetFileID string `json:"targetFileID"`
	From         string `json:"from"`
	To           string `json:"to"`
	Outcome      string `json:"outcome"`
	OccurredAt   string `json:"occurredAt"`
	PhysicalSHA  string `json:"physicalSHA"`
	Disposition  string `json:"disposition"`
}

func PlacementOperationID(operation, claim string) string {
	return "placement-" + digestJSON(struct{ Operation, Claim string }{operation, claim})
}

func (e PlacementEvidence) validate() error {
	if e.MemoryRevision == 0 || e.MemoryRevision >= 1<<53 {
		return ErrCorrupt
	}
	if e.PolicyVersion != 1 || !idOK(e.Project.ID) || !pathOK(e.Project.Path) || !textOK(e.Project.Name, 1200) || !idOK(e.OperationID) || !idOK(e.Claim) || !hashOK(e.IntentSHA) || !hashOK(e.PhysicalSHA) || !timeOK(e.OccurredAt) || !contains([]string{"moved", "copied"}, e.Outcome) || !idOK(e.ExpectedFile.ID) || e.ExpectedFile.Status != "present" || !timeOK(e.ExpectedFile.FirstObservedAt) || !textOK(e.ExpectedFile.LastEvent, 160) {
		return ErrCorrupt
	}
	if e.AuthorizedScope.Validate() != nil || e.Before.validate(e.AuthorizedScope) != nil || e.After.validate(e.AuthorizedScope) != nil || e.ExpectedFile.Fact.validate(e.AuthorizedScope) != nil || !within(e.Project.Path, e.After.Path) || e.Before.Path == e.After.Path {
		return ErrCorrupt
	}
	old := e.ExpectedFile.Fact
	if e.Before.Kind != "file" || e.After.Kind != "file" || e.Before.HashState != "verified" || e.After.HashState != "verified" || e.Before.SHA256 != e.After.SHA256 || e.Before.Size != e.After.Size || old.Path != e.Before.Path || old.Kind != e.Before.Kind || old.Size != e.Before.Size || old.ModifiedAt != e.Before.ModifiedAt || old.ObjectKey != "" && old.ObjectKey != e.Before.ObjectKey || old.HashState == "verified" && old.SHA256 != e.Before.SHA256 {
		return ErrCorrupt
	}
	if !textOK(e.SourceObject, 512) || !textOK(e.TargetObject, 512) || !textOK(e.SourceParent, 512) || !textOK(e.TargetParent, 512) || e.Before.ObjectKey != e.SourceObject || e.After.ObjectKey != e.TargetObject {
		return ErrCorrupt
	}
	if e.Outcome == "moved" && e.SourceObject != e.TargetObject || e.Outcome == "copied" && e.SourceObject == e.TargetObject {
		return ErrCorrupt
	}
	return nil
}

func derivePlacement(previous State, evidence PlacementEvidence) (PlacementFact, error) {
	if evidence.MemoryRevision > previous.Revision {
		return PlacementFact{}, ErrCorrupt
	}
	if evidence.validate() != nil || evidence.Project != previous.Project {
		return PlacementFact{}, ErrCorrupt
	}
	var source *File
	for _, file := range previous.Files {
		if file.ID == evidence.ExpectedFile.ID {
			f := file
			source = &f
			break
		}
	}
	if source == nil {
		return PlacementFact{}, ErrCorrupt
	}
	result := PlacementFact{Evidence: evidence, TargetFileID: source.ID, Disposition: "history_only", Reason: "执行事实已保留；当前目录有更新或追踪权限已改变，不覆盖当前记录", Changes: []PlacementChange{}}
	if evidence.Outcome == "copied" {
		result.TargetFileID = "copy_" + digestJSON(struct{ Project, Operation, Claim string }{evidence.Project.ID, evidence.OperationID, evidence.Claim})[:40]
	}
	if !previous.Enabled || previous.Scope.Digest() != evidence.AuthorizedScope.Digest() {
		return result, nil
	}
	var target *File
	for _, file := range previous.Files {
		if file.Status == "present" && file.Fact.Path == evidence.After.Path {
			f := file
			target = &f
			break
		}
	}
	if evidence.Outcome == "moved" {
		if source.Status == "present" && source.Fact == evidence.After {
			result.Disposition, result.Reason = "already_observed", "目录已观察到同一对象；补充执行来源，不改写观察事实"
			return result, nil
		}
		if !reflect.DeepEqual(*source, evidence.ExpectedFile) || target != nil {
			return result, nil
		}
		after := *source
		after.Fact = evidence.After
		after.LastEvent = ""
		result.Changes = []PlacementChange{{Before: source, After: after}}
	} else {
		if target != nil {
			if target.Fact == evidence.After && target.ID != source.ID {
				result.TargetFileID, result.Disposition, result.Reason = target.ID, "already_observed", "副本已被观察；沿用其文件身份并记录复制来源"
			}
			return result, nil
		}
		if !reflect.DeepEqual(*source, evidence.ExpectedFile) {
			return result, nil
		}
		// A missing target is safe to introduce only against the exact ledger
		// read before the action. A later observation can leave the source
		// unchanged and its wall clock can precede a delayed physical receipt.
		// Conservatively retain history on any intervening ledger update; an
		// already-observed matching target was handled above without mutation.
		if previous.Revision != evidence.MemoryRevision {
			return result, nil
		}
		for _, file := range previous.Files {
			if file.ID == result.TargetFileID || file.Fact.ObjectKey == evidence.TargetObject {
				return result, nil
			}
		}
		result.Changes = []PlacementChange{{After: File{ID: result.TargetFileID, Fact: evidence.After, Status: "present", FirstObservedAt: evidence.OccurredAt}}}
	}
	result.Disposition, result.Reason = "applied", "依据已持久保存的本地文件动作更新项目目录"
	return result, nil
}

// RecordPlacement records already-performed local work. Revoked tracking may
// suppress its current-directory projection, never its immutable history. A
// retry uses the original evidence and operation ID, not a fresh action time.
func (s *Store) RecordPlacement(ctx context.Context, revision uint64, evidence PlacementEvidence, recordedAt time.Time) (Result, error) {
	data, err := json.Marshal(evidence)
	if err != nil {
		return Result{}, err
	}
	var owned PlacementEvidence
	if err = json.Unmarshal(data, &owned); err != nil {
		return Result{}, err
	}
	evidence = owned
	if recordedAt.IsZero() || evidence.validate() != nil || evidence.Project != s.project {
		return Result{}, ErrCorrupt
	}
	return s.update(ctx, revision, PlacementOperationID(evidence.OperationID, evidence.Claim), "placement", evidence, func(previous State) ([]Event, error) {
		fact, err := derivePlacement(previous, evidence)
		if err != nil {
			return nil, err
		}
		return []Event{{Kind: "placement_recorded", ObservedAt: stamp(recordedAt), Source: "local_execution", Actor: "workshelf", Reason: fact.Reason, Placement: &fact}}, nil
	})
}

func applyPlacementEvent(state *State, files map[string]File, event Event) error {
	if event.Source != "local_execution" || event.Actor != "workshelf" || event.Placement == nil || event.Before != nil || event.After != nil || event.Scope != nil || event.Enabled != nil || event.Automatic != nil || event.Observation != nil || event.Note != nil {
		return ErrCorrupt
	}
	current := *state
	// reduce has already assigned the new record revision. Recheck against
	// the state before this single placement event, as RecordPlacement did.
	current.Revision--
	current.Files = make([]File, 0, len(files))
	for _, file := range files {
		current.Files = append(current.Files, file)
	}
	expected, err := derivePlacement(current, event.Placement.Evidence)
	if err != nil {
		return err
	}
	for index := range expected.Changes {
		expected.Changes[index].After.LastEvent = event.ID
	}
	if !reflect.DeepEqual(expected, *event.Placement) || event.Reason != expected.Reason {
		return ErrCorrupt
	}
	for _, change := range expected.Changes {
		old, exists := files[change.After.ID]
		if change.Before == nil && exists || change.Before != nil && (!exists || !reflect.DeepEqual(old, *change.Before)) {
			return ErrCorrupt
		}
		files[change.After.ID] = change.After
	}
	e := expected.Evidence
	state.Placements = append(state.Placements, PlacementSummary{EventID: event.ID, OperationID: e.OperationID, Claim: e.Claim, SourceFileID: e.ExpectedFile.ID, TargetFileID: expected.TargetFileID, From: e.Before.Path, To: e.After.Path, Outcome: e.Outcome, OccurredAt: e.OccurredAt, PhysicalSHA: e.PhysicalSHA, Disposition: expected.Disposition})
	return nil
}

func placementLabel(p PlacementSummary) string {
	action := "移动"
	if p.Outcome == "copied" {
		action = "复制，原件保留"
	}
	disposition := map[string]string{"applied": "已更新项目目录", "already_observed": "目录已有记录，已补充来源", "history_only": "仅补充历史，保留当前目录"}[p.Disposition]
	return fmt.Sprintf("%s · %s · %s", p.OccurredAt, action, disposition)
}
