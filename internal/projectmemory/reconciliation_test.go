package projectmemory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Domain fixtures only; native recovery tests exercise actual temporary files.
func reconciliationFixture(t *testing.T, copy bool) (*Store, ReconciliationEvidence) {
	t.Helper()
	outcome := "moved"
	if copy {
		outcome = "copied"
	}
	s, scope, p := placementFixture(t, outcome)
	v := readMemory(t, s)
	e := ReconciliationEvidence{Project: s.project, OperationID: p.OperationID, Claim: p.Claim, IntentSHA: p.IntentSHA, ReviewSHA: strings.Repeat("c", 64), JournalHead: strings.Repeat("d", 64), MemoryRevision: v.State.Revision, MemoryHead: v.HeadSHA, Scope: scope, ExpectedFile: p.ExpectedFile, Before: p.Before, TargetPath: p.After.Path, Target: &p.After, Outcome: "at_target", ClaimedAt: stamp(testNow.Add(time.Minute)), ObservedAt: stamp(testNow.Add(2 * time.Minute)), ConfirmedAt: stamp(testNow.Add(3 * time.Minute)), ConfirmedBy: "local_user", Performer: "unknown"}
	if copy {
		e.Source = &p.Before
		e.CopyObject = p.After.ObjectKey
		e.Outcome = "copy_published"
	}
	return s, e
}

func TestReconciliationUpdatesOnlyExactMembersWithoutGlobalObservation(t *testing.T) {
	s, e := reconciliationFixture(t, false)
	facts := []Fact{e.ExpectedFile.Fact}
	for i := 0; i < 99; i++ {
		facts = append(facts, fact(e.Scope, fmt.Sprintf("other-%02d.txt", i), fmt.Sprintf("object-%02d", i), "unchanged"))
	}
	observe(t, s, observation(e.Scope, 1, facts...), "hundred-baseline")
	before := readMemory(t, s)
	e.MemoryRevision, e.MemoryHead = before.State.Revision, before.HeadSHA
	for _, f := range before.State.Files {
		if f.ID == e.ExpectedFile.ID {
			e.ExpectedFile = f
		}
	}
	r, err := s.RecordReconciliation(context.Background(), before.State.Revision, e, testNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	after := readMemory(t, s)
	if len(after.State.Files) != 100 || len(after.State.Reconciliations) != 1 || len(after.State.Placements) != 0 || len(r.Record.Events) != 2 {
		t.Fatal("wrong identity/history count")
	}
	if after.State.LastObservedAt != before.State.LastObservedAt || after.State.ObservationComplete != before.State.ObservationComplete || !reflect.DeepEqual(after.State.Coverage, before.State.Coverage) {
		t.Fatal("local review replaced whole-project coverage")
	}
	for _, old := range before.State.Files {
		for _, now := range after.State.Files {
			if old.ID == now.ID {
				if old.ID == e.ExpectedFile.ID {
					if now.Fact != *e.Target || now.Status != "present" {
						t.Fatal("original identity not updated")
					}
				} else if old != now {
					t.Fatal("uninspected file changed", old.ID)
				}
			}
		}
	}
	if r.Record.Events[0].Source != "filesystem_observation" || r.Record.Events[0].Actor != "unknown" || r.Record.Events[1].Source != "local_user" || r.Record.Events[1].Actor != "local_user" || r.Record.Events[1].Reconciliation.Evidence.Performer != "unknown" {
		t.Fatal("confirmation was attributed as execution")
	}
	raw, err := os.ReadFile(filepath.Join(s.directory, "events", recordName(r.Record.Revision)))
	if err != nil || digest(raw) != r.RecordSHA {
		t.Fatal("receipt not actual segment", err)
	}
	markdown, err := os.ReadFile(filepath.Join(s.directory, "memory.md"))
	if err != nil || !strings.Contains(string(markdown), "中断整理核对") {
		t.Fatal("human-readable memory missing", err)
	}
}

func TestReconciliationNewerStateAndPausedScopeAreHistoricalOnly(t *testing.T) {
	for _, kind := range []string{"paused", "scope-removed", "same-time-observation", "agent-note"} {
		t.Run(kind, func(t *testing.T) {
			s, e := reconciliationFixture(t, false)
			v := readMemory(t, s)
			switch kind {
			case "paused":
				_, _ = s.SetTracking(context.Background(), v.State.Revision, "pause", e.Scope, false, testNow.Add(3*time.Minute))
			case "scope-removed":
				_, _ = s.SetTracking(context.Background(), v.State.Revision, "narrow", Scope{Roots: []string{filepath.Join(s.project.Path, "elsewhere")}}, true, testNow.Add(3*time.Minute))
			case "same-time-observation":
				observe(t, s, observation(e.Scope, 1, e.ExpectedFile.Fact), "second-observation")
			case "agent-note":
				_, err := s.AddNote(context.Background(), v.State.Revision, "note", Note{Kind: "agent_proposal", Text: "A proposal", Agent: "test"}, testNow.Add(3*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
			}
			before := readMemory(t, s)
			r, err := s.RecordReconciliation(context.Background(), before.State.Revision, e, testNow.Add(4*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Record.Events) != 1 || r.Record.Events[0].Reconciliation.Disposition != "history_only" {
				t.Fatal("stale review projected")
			}
			after := readMemory(t, s)
			if !reflect.DeepEqual(before.State.Files, after.State.Files) || before.State.Enabled != after.State.Enabled || before.State.Scope.Digest() != after.State.Scope.Digest() || !reflect.DeepEqual(before.State.Coverage, after.State.Coverage) {
				t.Fatal("history overwrote current state")
			}
		})
	}
}

func TestReconciliationCopyIdentityAndThirdPathAmbiguity(t *testing.T) {
	for _, kind := range []string{"new-copy", "observed-copy", "foreign-target", "third-path"} {
		t.Run(kind, func(t *testing.T) {
			s, e := reconciliationFixture(t, true)
			if kind != "new-copy" {
				target := *e.Target
				if kind == "foreign-target" {
					target.ObjectKey = "foreign"
				}
				if kind == "third-path" {
					target.Path = filepath.Join(s.project.Path, "not-inspected.txt")
				}
				observe(t, s, observation(e.Scope, 2, e.ExpectedFile.Fact, target), "observed")
			}
			before := readMemory(t, s)
			e.MemoryRevision, e.MemoryHead = before.State.Revision, before.HeadSHA
			for _, f := range before.State.Files {
				if f.ID == e.ExpectedFile.ID {
					e.ExpectedFile = f
				}
			}
			r, err := s.RecordReconciliation(context.Background(), before.State.Revision, e, testNow.Add(4*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			last := r.Record.Events[len(r.Record.Events)-1].Reconciliation
			want := map[string]string{"new-copy": "applied", "observed-copy": "already_observed", "foreign-target": "history_only", "third-path": "history_only"}[kind]
			if last.Disposition != want || last.TargetFileID == e.ExpectedFile.ID {
				t.Fatal("wrong copy identity/disposition", last.Disposition)
			}
			after := readMemory(t, s)
			if kind == "new-copy" {
				if len(after.State.Files) != 2 {
					t.Fatal("new copy not registered")
				}
			} else if !reflect.DeepEqual(before.State.Files, after.State.Files) {
				t.Fatal("uninspected/current facts overwritten")
			}
		})
	}
}

func TestReconciliationDurableIdempotencyAndRecordValidation(t *testing.T) {
	s, e := reconciliationFixture(t, false)
	v := readMemory(t, s)
	failure := errors.New("directory durability failure")
	s.beforePlacementSync = func() error { return failure }
	if _, err := s.RecordReconciliation(context.Background(), v.State.Revision, e, testNow.Add(4*time.Minute)); !errors.Is(err, failure) {
		t.Fatal("sync failure acknowledged", err)
	}
	if _, err := s.RecordReconciliation(context.Background(), v.State.Revision, e, testNow.Add(5*time.Minute)); !errors.Is(err, failure) {
		t.Fatal("retry skipped barrier", err)
	}
	s.beforePlacementSync = nil
	r, err := s.RecordReconciliation(context.Background(), v.State.Revision, e, testNow.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RecordReconciliation(context.Background(), 0, e, testNow.Add(7*time.Minute))
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("retry changed original record", err)
	}
	if r.Record.Events[len(r.Record.Events)-1].ObservedAt != stamp(testNow.Add(4*time.Minute)) {
		t.Fatal("record time refreshed")
	}
	changed := e
	changed.ReviewSHA = strings.Repeat("f", 64)
	latest := readMemory(t, s)
	if _, err = s.RecordReconciliation(context.Background(), latest.State.Revision, changed, testNow.Add(8*time.Minute)); !errors.Is(err, ErrOperationReused) {
		t.Fatal("same claim confirmed twice", err)
	}
	bad := r.Record
	bad.Events = append([]Event{}, r.Record.Events...)
	bad.Events[0].Actor = "workshelf"
	if _, err = applyRecord(v.State, bad); !errors.Is(err, ErrCorrupt) {
		t.Fatal("replay trusted fabricated performer", err)
	}
	bad = r.Record
	bad.Events = append([]Event{}, r.Record.Events...)
	after := *bad.Events[0].After
	after.Fact.Path = filepath.Join(s.project.Path, "uninspected.txt")
	bad.Events[0].After = &after
	if _, err = applyRecord(v.State, bad); !errors.Is(err, ErrCorrupt) {
		t.Fatal("typed record allowed extra path", err)
	}
}
