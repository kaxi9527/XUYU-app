package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These synthetic physical facts test real Store persistence and reduction.
// Actual reverse filesystem operations belong to the later native executor.
func undoFixture(t *testing.T, kind string, external bool, count int) (*Store, UndoEvidence) {
	t.Helper()
	s, scope := memoryFixture(t)
	from := fact(scope, "draft.txt", "original-object", "example")
	if external {
		from.Path = filepath.Join(filepath.Dir(s.project.Path), "loose", "draft.txt")
		scope.Files = []string{from.Path}
	}
	initialize(t, s, scope)
	facts := []Fact{from}
	for i := 1; i < count; i++ {
		facts = append(facts, fact(scope, fmt.Sprintf("other-%03d.txt", i), fmt.Sprintf("other-%03d", i), "untouched"))
	}
	obs := observation(scope, 1, facts...)
	for _, path := range scope.Files {
		obs.Coverage = append(obs.Coverage, Coverage{Path: path, Status: "complete"})
	}
	observe(t, s, obs, "baseline")
	v := readMemory(t, s)
	var file File
	for _, f := range v.State.Files {
		if f.Fact.Path == from.Path {
			file = f
		}
	}
	to := from
	to.Path = filepath.Join(s.project.Path, "资料", "draft.txt")
	ref := UndoReference{OperationID: "original-operation", Claim: "original-claim", Kind: kind, ProofSHA: strings.Repeat("b", 64), FileID: file.ID, From: from, To: to}
	var saved Result
	var err error
	if kind == "placement" {
		p := PlacementEvidence{Project: s.project, OperationID: ref.OperationID, Claim: ref.Claim, IntentSHA: strings.Repeat("a", 64), PhysicalSHA: ref.ProofSHA, Outcome: "moved", ExpectedFile: file, Before: from, After: to, SourceObject: from.ObjectKey, TargetObject: to.ObjectKey, SourceParent: "source-parent", TargetParent: "target-parent", OccurredAt: stamp(testNow.Add(2 * time.Minute)), MemoryRevision: v.State.Revision, AuthorizedScope: scope, PolicyVersion: 1}
		saved, err = s.RecordPlacement(context.Background(), v.State.Revision, p, testNow.Add(3*time.Minute))
	} else {
		p := ReconciliationEvidence{Project: s.project, OperationID: ref.OperationID, Claim: ref.Claim, IntentSHA: strings.Repeat("a", 64), ReviewSHA: ref.ProofSHA, JournalHead: strings.Repeat("d", 64), MemoryRevision: v.State.Revision, MemoryHead: v.HeadSHA, Scope: scope, ExpectedFile: file, Before: from, TargetPath: to.Path, Target: &to, Outcome: "at_target", ClaimedAt: stamp(testNow.Add(time.Minute)), ObservedAt: stamp(testNow.Add(2 * time.Minute)), ConfirmedAt: stamp(testNow.Add(3 * time.Minute)), ConfirmedBy: "local_user", Performer: "unknown"}
		saved, err = s.RecordReconciliation(context.Background(), v.State.Revision, p, testNow.Add(3*time.Minute))
	}
	if err != nil {
		t.Fatal(err)
	}
	ref.EventID = saved.Record.Events[len(saved.Record.Events)-1].ID
	v = readMemory(t, s)
	for _, f := range v.State.Files {
		if f.ID == file.ID {
			file = f
		}
	}
	return s, UndoEvidence{Project: s.project, UndoID: "undo-one", Claim: "undo-claim", PlanSHA: strings.Repeat("c", 64), PhysicalSHA: strings.Repeat("e", 64), Original: ref, Scope: scope, ExpectedFile: file, Before: to, After: from, MemoryRevision: v.State.Revision, OccurredAt: stamp(testNow.Add(4 * time.Minute))}
}

func cloneUndo(e UndoEvidence) UndoEvidence {
	data, _ := json.Marshal(e)
	var out UndoEvidence
	_ = json.Unmarshal(data, &out)
	return out
}

func TestUndoRecordsOriginalIdentityAndPreservesOtherMembers(t *testing.T) {
	for _, kind := range []string{"placement", "reconciliation"} {
		for _, external := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/external-%t", kind, external), func(t *testing.T) {
				s, e := undoFixture(t, kind, external, 100)
				before := readMemory(t, s)
				oldBytes, _ := os.ReadFile(filepath.Join(s.directory, "events", recordName(before.State.Revision)))
				r, err := s.RecordUndo(context.Background(), before.State.Revision, e, testNow.Add(5*time.Minute))
				if err != nil || r.ProjectionPending || len(r.Record.Events) != 1 || r.Record.Events[0].Undo.Disposition != "applied" {
					t.Fatal("undo not projected", err)
				}
				after := readMemory(t, s)
				if len(after.State.Undos) != 1 || len(after.State.Files) != 100 || after.State.LastObservedAt != before.State.LastObservedAt || after.State.ObservationComplete != before.State.ObservationComplete || !reflect.DeepEqual(after.State.Coverage, before.State.Coverage) {
					t.Fatal("local undo changed global coverage")
				}
				for i, f := range before.State.Files {
					got := after.State.Files[i]
					if f.ID == e.Original.FileID {
						if got.Fact != e.After || got.ID != f.ID || got.LastEvent != r.Record.Events[0].ID {
							t.Fatal("wrong restored identity", got.ID)
						}
					} else if f != got {
						t.Fatal("other member changed", f.ID)
					}
				}
				if !reflect.DeepEqual(after.State.Placements, before.State.Placements) || !reflect.DeepEqual(after.State.Reconciliations, before.State.Reconciliations) {
					t.Fatal("original evidence overwritten")
				}
				oldAgain, _ := os.ReadFile(filepath.Join(s.directory, "events", recordName(before.State.Revision)))
				if string(oldBytes) != string(oldAgain) {
					t.Fatal("original ledger segment changed")
				}
				raw, err := os.ReadFile(filepath.Join(s.directory, "events", recordName(r.Record.Revision)))
				if err != nil || digest(raw) != r.RecordSHA || digestJSON(r.Record) == r.RecordSHA {
					t.Fatal("not actual persisted SHA", err)
				}
				memory, err := os.ReadFile(filepath.Join(s.directory, "memory.md"))
				if err != nil || !strings.Contains(string(memory), "WorkShelf 撤销记录") || !strings.Contains(string(memory), e.Original.ProofSHA) {
					t.Fatal("undo provenance not rendered", err)
				}
				restarted, err := NewStore(filepath.Dir(s.directory), s.project)
				if err != nil {
					t.Fatal(err)
				}
				again, err := restarted.RecordUndo(context.Background(), 0, e, testNow.Add(-time.Hour))
				if err != nil || !reflect.DeepEqual(r, again) || readMemory(t, restarted).State.Revision != after.State.Revision {
					t.Fatal("restart or clock rollback changed original receipt", err)
				}
				changed := cloneUndo(e)
				changed.UndoID = "another-undo"
				if _, err = restarted.RecordUndo(context.Background(), after.State.Revision, changed, testNow.Add(6*time.Minute)); !errors.Is(err, ErrOperationReused) {
					t.Fatal("original claim reversed twice", err)
				}
			})
		}
	}
}

func TestUndoLateReceiptPreservesNewerFactsAndPermissions(t *testing.T) {
	for _, scenario := range []string{"pause", "scope", "note", "same-time-observation", "edited", "missing", "already-restored", "foreign-original"} {
		t.Run(scenario, func(t *testing.T) {
			s, e := undoFixture(t, "placement", false, 1)
			v := readMemory(t, s)
			var err error
			switch scenario {
			case "pause":
				_, err = s.SetTracking(context.Background(), v.State.Revision, "pause", e.Scope, false, testNow.Add(4*time.Minute))
			case "scope":
				_, err = s.SetTracking(context.Background(), v.State.Revision, "scope", Scope{Roots: []string{filepath.Join(s.project.Path, "elsewhere")}}, true, testNow.Add(4*time.Minute))
			case "note":
				_, err = s.AddNote(context.Background(), v.State.Revision, "note", Note{Kind: "agent_proposal", Text: "No new authority", Agent: "test"}, testNow.Add(4*time.Minute))
			default:
				facts := []Fact{e.Before}
				if scenario == "edited" {
					facts[0].SHA256 = strings.Repeat("f", 64)
				}
				if scenario == "missing" {
					facts = nil
				}
				if scenario == "already-restored" {
					facts = []Fact{e.After}
				}
				if scenario == "foreign-original" {
					foreign := e.After
					foreign.ObjectKey = "foreign"
					facts = append(facts, foreign)
				}
				observe(t, s, observation(e.Scope, 1, facts...), "later-observation")
			}
			if err != nil {
				t.Fatal(err)
			}
			before := readMemory(t, s)
			r, err := s.RecordUndo(context.Background(), before.State.Revision, e, testNow.Add(5*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			want := "history_only"
			if scenario == "already-restored" {
				want = "already_observed"
			}
			if r.Record.Events[0].Undo.Disposition != want || len(r.Record.Events[0].Undo.Changes) != 0 {
				t.Fatal("late receipt projected", scenario)
			}
			after := readMemory(t, s)
			if !reflect.DeepEqual(before.State.Files, after.State.Files) || before.State.Enabled != after.State.Enabled || before.State.Scope.Digest() != after.State.Scope.Digest() || !reflect.DeepEqual(before.State.Coverage, after.State.Coverage) || before.State.LastObservedAt != after.State.LastObservedAt {
				t.Fatal("new state overwritten")
			}
		})
	}
}

func TestUndoRejectsUnboundOrChangedEvidence(t *testing.T) {
	mutations := map[string]func(*UndoEvidence){
		"original-event":     func(e *UndoEvidence) { e.Original.EventID = "2:99" },
		"original-claim":     func(e *UndoEvidence) { e.Original.Claim = "another" },
		"original-hash":      func(e *UndoEvidence) { e.Original.ProofSHA = strings.Repeat("f", 64) },
		"original-operation": func(e *UndoEvidence) { e.Original.OperationID = "another" },
		"original-kind":      func(e *UndoEvidence) { e.Original.Kind = "reconciliation" },
		"foreign-file":       func(e *UndoEvidence) { e.Original.FileID = "another" },
		"other-destination":  func(e *UndoEvidence) { e.After.Path = filepath.Join(e.Project.Path, "other.txt") },
		"replacement-object": func(e *UndoEvidence) { e.Before.ObjectKey = "replacement" },
		"different-content":  func(e *UndoEvidence) { e.Before.SHA256 = strings.Repeat("f", 64) },
		"different-size":     func(e *UndoEvidence) { e.After.Size++ },
		"future-revision":    func(e *UndoEvidence) { e.MemoryRevision += 10 },
		"out-of-scope":       func(e *UndoEvidence) { e.Scope = Scope{Files: []string{e.Before.Path}} },
		"unknown-object":     func(e *UndoEvidence) { e.ExpectedFile.Fact.ObjectKey = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, e := undoFixture(t, "placement", false, 1)
			before := readMemory(t, s)
			mutate(&e)
			if _, err := s.RecordUndo(context.Background(), before.State.Revision, e, testNow.Add(5*time.Minute)); !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted changed evidence", err)
			}
			if after := readMemory(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid evidence changed ledger")
			}
		})
	}
}

func TestUndoReplayValidatesActorPayloadAndChanges(t *testing.T) {
	s, e := undoFixture(t, "placement", false, 1)
	before := readMemory(t, s)
	r, err := s.RecordUndo(context.Background(), before.State.Revision, e, testNow.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"actor", "source", "payload", "extra-change", "changed-path", "operation", "foreign-kind", "reason"} {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(r.Record)
			var bad Record
			_ = json.Unmarshal(data, &bad)
			switch name {
			case "actor":
				bad.Events[0].Actor = "unknown"
			case "source":
				bad.Events[0].Source = "local_user"
			case "payload":
				bad.Events[0].Note = &Note{Kind: "user_decision", Text: "arbitrary"}
			case "extra-change":
				bad.Events[0].Undo.Changes = append(bad.Events[0].Undo.Changes, bad.Events[0].Undo.Changes[0])
			case "changed-path":
				bad.Events[0].Undo.Changes[0].After.Fact.Path = filepath.Join(s.project.Path, "different.txt")
			case "operation":
				bad.OperationID = "unbound"
			case "foreign-kind":
				bad.Events[0].Kind = "placement_recorded"
			case "reason":
				bad.Events[0].Reason = "claim all restored"
			}
			if _, err := applyRecord(before.State, bad); !errors.Is(err, ErrCorrupt) {
				t.Fatal("forged record replayed", err)
			}
		})
	}
}

func TestUndoConcurrentFirstCommitAndRequestIdentity(t *testing.T) {
	s, e := undoFixture(t, "placement", false, 1)
	before := readMemory(t, s)
	start := make(chan struct{})
	results := make(chan Result, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		other, err := NewStore(filepath.Dir(s.directory), s.project)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			<-start
			r, err := store.RecordUndo(context.Background(), before.State.Revision, e, testNow.Add(5*time.Minute))
			if err != nil {
				t.Errorf("concurrent first commit: %v", err)
				return
			}
			results <- r
		}(other)
	}
	close(start)
	wg.Wait()
	close(results)
	var first Result
	n := 0
	for r := range results {
		if n == 0 {
			first = r
		} else if !reflect.DeepEqual(first, r) {
			t.Fatal("concurrent requests returned different receipts")
		}
		n++
	}
	after := readMemory(t, s)
	if n != 6 || after.State.Revision != before.State.Revision+1 || len(after.State.Undos) != 1 {
		t.Fatal("concurrent request committed more than once")
	}
	changed := cloneUndo(e)
	changed.PhysicalSHA = strings.Repeat("f", 64)
	if _, err := s.RecordUndo(context.Background(), after.State.Revision, changed, testNow.Add(6*time.Minute)); !errors.Is(err, ErrOperationReused) {
		t.Fatal("same undo identity accepted changed receipt", err)
	}
}

func TestUndoRejectsCopyHistoryEvenWithForgedSingleObjectFacts(t *testing.T) {
	s, scope, p := placementFixture(t, "copied")
	v := readMemory(t, s)
	r, err := s.RecordPlacement(context.Background(), v.State.Revision, p, testNow.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	v = readMemory(t, s)
	var file File
	for _, f := range v.State.Files {
		if f.Fact == p.After {
			file = f
		}
	}
	from := p.After
	from.Path = p.Before.Path
	e := UndoEvidence{Project: s.project, UndoID: "false-undo", Claim: "false-claim", PlanSHA: strings.Repeat("c", 64), PhysicalSHA: strings.Repeat("e", 64), Original: UndoReference{OperationID: p.OperationID, Claim: p.Claim, Kind: "placement", EventID: r.Record.Events[0].ID, ProofSHA: p.PhysicalSHA, FileID: file.ID, From: from, To: p.After}, Scope: scope, ExpectedFile: file, Before: p.After, After: from, MemoryRevision: v.State.Revision, OccurredAt: stamp(testNow.Add(4 * time.Minute))}
	if e.Validate() != nil {
		t.Fatal("fixture does not reach original ledger reference check")
	}
	if _, err = s.RecordUndo(context.Background(), v.State.Revision, e, testNow.Add(5*time.Minute)); !errors.Is(err, ErrCorrupt) {
		t.Fatal("copy was accepted as original single-object move", err)
	}
	if after := readMemory(t, s); !reflect.DeepEqual(v, after) {
		t.Fatal("rejected copy changed ledger")
	}
}

func TestUndoDoesNotMergeAmbiguousObjectIdentities(t *testing.T) {
	s, e := undoFixture(t, "placement", false, 1)
	v := readMemory(t, s)
	// Two distinct catalog entries claiming one object is not permission to
	// merge them. Exercise the reducer even at the captured revision.
	alias := e.ExpectedFile
	alias.ID = "ambiguous-alias"
	alias.Status = "missing"
	alias.Fact.Path = filepath.Join(s.project.Path, "third.txt")
	v.State.Files = append(v.State.Files, alias)
	fact, err := deriveUndo(v.State, e)
	if err != nil || fact.Disposition != "history_only" || len(fact.Changes) != 0 {
		t.Fatal("undo merged ambiguous file identities", err)
	}
}

func TestUndoDurabilityProjectionAndConcurrentReplay(t *testing.T) {
	s, e := undoFixture(t, "placement", false, 1)
	v := readMemory(t, s)
	failure := errors.New("sync failed")
	s.beforePlacementSync = func() error { return failure }
	if _, err := s.RecordUndo(context.Background(), v.State.Revision, e, testNow.Add(5*time.Minute)); !errors.Is(err, failure) {
		t.Fatal("missing write barrier", err)
	}
	if _, err := s.RecordUndo(context.Background(), v.State.Revision, e, testNow.Add(6*time.Minute)); !errors.Is(err, failure) {
		t.Fatal("retry skipped barrier", err)
	}
	s.beforePlacementSync = nil
	r, err := s.RecordUndo(context.Background(), 0, e, testNow.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if r.Record.Events[0].ObservedAt != stamp(testNow.Add(5*time.Minute)) {
		t.Fatal("retry replaced original timestamp")
	}
	other, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			again, err := store.RecordUndo(context.Background(), 0, e, testNow.Add(8*time.Minute))
			if err != nil || !reflect.DeepEqual(r, again) {
				t.Errorf("concurrent replay changed receipt: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if len(readMemory(t, s).State.Undos) != 1 {
		t.Fatal("concurrent duplicate undo")
	}
	s2, e2 := undoFixture(t, "placement", false, 1)
	v2 := readMemory(t, s2)
	s2.afterCommit = func() error { return errors.New("projection failed") }
	pending, err := s2.RecordUndo(context.Background(), v2.State.Revision, e2, testNow.Add(5*time.Minute))
	if err != nil || !pending.ProjectionPending {
		t.Fatal("projection failure reported complete", err)
	}
	s2.afterCommit = nil
	restarted, err := NewStore(filepath.Dir(s2.directory), s2.project)
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := restarted.RecordUndo(context.Background(), 0, e2, testNow.Add(9*time.Minute))
	if err != nil || fixed.ProjectionPending || fixed.RecordSHA != pending.RecordSHA || !reflect.DeepEqual(fixed.Record, pending.Record) {
		t.Fatal("projection repair changed event", err)
	}
}
