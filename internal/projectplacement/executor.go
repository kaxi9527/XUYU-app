package projectplacement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// CurrentAuthority must reread the host's registration, template and user-rule
// authority. It receives a detached intent, not permission from model text.
type ExecutionAuthority struct {
	Authority Authority
	Profile   worktype.Profile
}
type CurrentAuthority func(context.Context, Intent) (ExecutionAuthority, error)

type Executor struct {
	journal *Journal
	memory  *projectmemory.Store
	current CurrentAuthority
	fault   func(string) error                  // private fault seam; never RPC input
	busy    func(context.Context, string) error // private platform probe seam
}

func NewExecutor(journal *Journal, memory *projectmemory.Store, current CurrentAuthority) (*Executor, error) {
	if journal == nil || memory == nil || current == nil {
		return nil, ErrScope
	}
	return &Executor{journal: journal, memory: memory, current: current}, nil
}

func (e *Executor) fail(point string) error {
	if e.fault != nil {
		return e.fault(point)
	}
	return nil
}

// Execute acts only on a previously prepared and confirmed operation. A retry
// can finish the same memory receipt; claimed/uncertain work is never replayed.
func (e *Executor) Execute(ctx context.Context, id, intentSHA string) (JournalView, error) {
	return e.execute(ctx, id, intentSHA)
}

func originalFile(intent Intent, id string) (projectmemory.File, bool) {
	for _, suggestion := range intent.Preview.Proposal.Suggestions {
		if suggestion.File.ID == id {
			return suggestion.File, true
		}
	}
	return projectmemory.File{}, false
}

func (e *Executor) checkCurrent(ctx context.Context, view JournalView, row Row) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(view.Intent)
	if err != nil {
		return err
	}
	var detached Intent
	if err = json.Unmarshal(data, &detached); err != nil {
		return err
	}
	currentAuthority, err := e.current(ctx, detached)
	if err != nil {
		return err
	}
	authority := currentAuthority.Authority
	if !reflect.DeepEqual(authority, view.Intent.Preview.Authority) {
		return ErrConsent
	}
	current, err := e.memory.Read(ctx)
	if err != nil {
		return err
	}
	if current.State.Project != authority.Project || !current.State.Enabled || current.State.Scope.Digest() != authority.Scope.Digest() {
		return ErrConsent
	}
	if projectcontext.ValidatePlacementPolicy(current.State, currentAuthority.Profile, view.Intent.Preview.Proposal) != nil {
		return ErrConsent
	}
	original, ok := originalFile(view.Intent, row.FileID)
	if !ok {
		return ErrJournal
	}
	for _, file := range current.State.Files {
		if file.ID == row.FileID {
			if reflect.DeepEqual(file, original) {
				return nil
			}
			return ErrChanged
		}
	}
	return ErrChanged
}

func placementEvidence(view JournalView, item ItemState) (projectmemory.PlacementEvidence, error) {
	file, ok := originalFile(view.Intent, item.FileID)
	if !ok || item.Stage != "memory_pending" || item.Receipt == nil || item.Receipt.After == nil || !goodSHA(item.PhysicalSHA) || !validTime(item.PhysicalAt) {
		return projectmemory.PlacementEvidence{}, ErrJournal
	}
	r := item.Receipt
	if r.MemoryRevision == 0 || !validTime(r.OccurredAt) {
		return projectmemory.PlacementEvidence{}, ErrJournal
	}
	return projectmemory.PlacementEvidence{Project: view.Intent.Preview.Authority.Project, OperationID: view.Intent.ID, Claim: item.Claim, IntentSHA: view.IntentSHA, PhysicalSHA: item.PhysicalSHA, Outcome: r.Outcome, ExpectedFile: file, Before: r.Before, After: *r.After, SourceObject: r.SourceObject, TargetObject: r.TargetObject, SourceParent: r.SourceParent, TargetParent: r.TargetParent, OccurredAt: r.OccurredAt, MemoryRevision: r.MemoryRevision, AuthorizedScope: view.Intent.Preview.Authority.Scope, PolicyVersion: 1}, nil
}

func (e *Executor) saveMemory(s *journalSession, item ItemState) error {
	evidence, err := placementEvidence(s.view, item)
	if err != nil {
		return err
	}
	// Finishing already-performed work does not acquire fresh file authority.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = e.fail("before_memory"); err != nil {
		return err
	}
	var saved projectmemory.Result
	for attempt := 0; attempt < 4; attempt++ {
		current, readErr := e.memory.Read(ctx)
		if readErr != nil {
			return readErr
		}
		if current.State.Project != evidence.Project {
			return ErrScope
		}
		saved, err = e.memory.RecordPlacement(ctx, current.State.Revision, evidence, time.Now())
		if !errors.Is(err, projectmemory.ErrConflict) {
			break
		}
	}
	if err != nil {
		return err
	}
	if err = e.fail("after_memory"); err != nil {
		return err
	}
	if saved.ProjectionPending || !goodSHA(saved.RecordSHA) || len(saved.Record.Events) != 1 || saved.Record.Events[0].Placement == nil {
		return ErrReconcile
	}
	event := saved.Record.Events[0]
	if event.Placement.Disposition == "history_only" {
		// Do not release a pending receipt into more file work when current
		// permissions or facts no longer support its original projection.
		stopped, stopErr := e.journal.RequestStop(ctx, s.view.Intent.ID, s.view.IntentSHA, time.Now())
		if stopErr != nil {
			return stopErr
		}
		s.view.StopRequested = stopped.StopRequested
	}
	return s.memory(item.FileID, item.Claim, MemoryReceipt{ProjectID: evidence.Project.ID, Operation: saved.Record.OperationID, Revision: saved.Record.Revision, RecordSHA: saved.RecordSHA, EventID: event.ID}, time.Now())
}
