package projectmemory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These are ledger protocol fixtures; actual native movement is exercised in
// projectplacement/executor_test.go, not claimed from synthetic facts here.
func placementFixture(t *testing.T, outcome string) (*Store, Scope, PlacementEvidence) {
	t.Helper()
	store, scope := memoryFixture(t)
	initialize(t, store, scope)
	before := fact(scope, "draft.txt", "object-source", "example")
	old := before
	old.SHA256 = ""
	old.HashState = "metadata_only"
	observe(t, store, observation(scope, 1, old), "baseline")
	view := readMemory(t, store)
	after := before
	after.Path = filepath.Join(scope.Roots[0], "资料", "draft.txt")
	if outcome == "copied" {
		after.ObjectKey = "object-copy"
	}
	e := PlacementEvidence{Project: store.project, OperationID: "placement-one", Claim: "claim-one", IntentSHA: strings.Repeat("a", 64), PhysicalSHA: strings.Repeat("b", 64), Outcome: outcome, ExpectedFile: view.State.Files[0], Before: before, After: after, SourceObject: before.ObjectKey, TargetObject: after.ObjectKey, SourceParent: "source-parent", TargetParent: "target-parent", OccurredAt: stamp(testNow.Add(2 * time.Minute)), AuthorizedScope: scope, PolicyVersion: 1}
	e.MemoryRevision = view.State.Revision
	return store, scope, e
}

func TestPlacementUsesOldMemoryBaselineAndDurableOriginalReceipt(t *testing.T) {
	s, _, e := placementFixture(t, "moved")
	v := readMemory(t, s)
	r, err := s.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(3*time.Minute))
	if err != nil || r.ProjectionPending || r.Record.Events[0].Placement.Disposition != "applied" {
		t.Fatal("metadata-only baseline rejected", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.directory, "events", recordName(r.Record.Revision)))
	if err != nil || digest(raw) != r.RecordSHA || r.RecordSHA == digestJSON(r.Record) {
		t.Fatal("receipt hash not from actual newline-terminated segment", err)
	}
	v = readMemory(t, s)
	if len(v.State.Placements) != 1 || v.State.Files[0].ID != e.ExpectedFile.ID || v.State.Files[0].Fact != e.After {
		t.Fatal("same-project move not projected")
	}
	again, err := s.RecordPlacement(context.Background(), 0, e, testNow.Add(5*time.Minute))
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("retry changed original event/receipt", err)
	}
	changed := e
	changed.After.Path = filepath.Join(s.project.Path, "different.txt")
	if _, err = s.RecordPlacement(context.Background(), v.State.Revision, changed, testNow.Add(6*time.Minute)); !errors.Is(err, ErrOperationReused) {
		t.Fatal("same claim accepted another physical result", err)
	}
}

func TestPlacementLateEvidencePreservesScopeAndNewerObservations(t *testing.T) {
	for _, scenario := range []string{"paused", "scope-removed", "already-observed", "later-edit", "later-missing"} {
		t.Run(scenario, func(t *testing.T) {
			s, scope, e := placementFixture(t, "moved")
			v := readMemory(t, s)
			disposition := "history_only"
			switch scenario {
			case "paused":
				_, _ = s.SetTracking(context.Background(), v.State.Revision, "pause", scope, false, testNow.Add(3*time.Minute))
			case "scope-removed":
				_, _ = s.SetTracking(context.Background(), v.State.Revision, "narrow", Scope{Roots: []string{filepath.Join(s.project.Path, "elsewhere")}}, true, testNow.Add(3*time.Minute))
			case "already-observed":
				observe(t, s, observation(scope, 3, e.After), "after")
				disposition = "already_observed"
			case "later-edit":
				later := e.After
				later.SHA256 = digest([]byte("changed"))
				observe(t, s, observation(scope, 3, later), "later")
			case "later-missing":
				observe(t, s, observation(scope, 3), "missing")
			}
			before := readMemory(t, s)
			r, err := s.RecordPlacement(context.Background(), before.State.Revision, e, testNow.Add(4*time.Minute))
			if err != nil || r.Record.Events[0].Placement.Disposition != disposition {
				t.Fatal("late evidence disposition wrong", err)
			}
			after := readMemory(t, s)
			if !reflect.DeepEqual(before.State.Files, after.State.Files) || before.State.Enabled != after.State.Enabled || before.State.Scope.Digest() != after.State.Scope.Digest() || len(after.State.Placements) != 1 {
				t.Fatal("historical receipt overwrote newer facts or authorization")
			}
		})
	}
}

func TestPlacementCopyReusesOnlyVerifiedObservedIdentity(t *testing.T) {
	for _, scenario := range []string{"new", "observed", "different-object", "later-missing"} {
		t.Run(scenario, func(t *testing.T) {
			s, scope, e := placementFixture(t, "copied")
			expectedID := ""
			disposition := "applied"
			if scenario != "new" {
				files := []Fact{e.Before}
				if scenario != "later-missing" {
					target := e.After
					if scenario == "different-object" {
						target.ObjectKey = "foreign-object"
					}
					files = append(files, target)
				}
				observe(t, s, observation(scope, 3, files...), "after-copy")
				for _, file := range readMemory(t, s).State.Files {
					if file.Fact.Path == e.After.Path {
						expectedID = file.ID
					}
				}
				disposition = "history_only"
				if scenario == "observed" {
					disposition = "already_observed"
				}
			}
			v := readMemory(t, s)
			r, err := s.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(4*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			event := r.Record.Events[0].Placement
			if event.Disposition != disposition || event.TargetFileID == e.ExpectedFile.ID {
				t.Fatal("copy identity collapsed")
			}
			if scenario == "observed" && event.TargetFileID != expectedID || scenario == "different-object" && event.TargetFileID == expectedID {
				t.Fatal("claimed an unrelated observed object")
			}
			after := readMemory(t, s)
			if scenario != "new" && !reflect.DeepEqual(v.State.Files, after.State.Files) {
				t.Fatal("late copy receipt changed observed files")
			}
			if scenario == "new" && len(after.State.Files) != 2 {
				t.Fatal("copy file identity missing")
			}
		})
	}
}

func TestPlacementDurabilityAndProjectionRetryKeepOneEvent(t *testing.T) {
	for _, window := range []string{"directory-sync", "projection"} {
		t.Run(window, func(t *testing.T) {
			s, _, e := placementFixture(t, "moved")
			v := readMemory(t, s)
			failure := errors.New("persistent storage failure")
			if window == "directory-sync" {
				s.beforePlacementSync = func() error { return failure }
			} else {
				s.afterCommit = func() error { return failure }
			}
			first, err := s.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(3*time.Minute))
			if window == "directory-sync" {
				if !errors.Is(err, failure) {
					t.Fatal("failed barrier acknowledged", err)
				}
				if _, err = s.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(4*time.Minute)); !errors.Is(err, failure) {
					t.Fatal("visible record bypassed failed barrier", err)
				}
			} else if err != nil || !first.ProjectionPending || !hashOK(first.RecordSHA) {
				t.Fatal("projection failure erased actual commit", err)
			}
			s.beforePlacementSync, s.afterCommit = nil, nil
			got, err := s.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(5*time.Minute))
			if err != nil || got.ProjectionPending || got.Record.Revision != v.State.Revision+1 || got.Record.Events[0].ObservedAt != stamp(testNow.Add(3*time.Minute)) || len(readMemory(t, s).State.Placements) != 1 {
				t.Fatal("retry duplicated/replaced original commit", err)
			}
		})
	}
}

func TestPlacementConcurrentStoresReturnSameOriginalCommit(t *testing.T) {
	s, _, e := placementFixture(t, "moved")
	v := readMemory(t, s)
	other, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	var results [2]Result
	var errs [2]error
	var wg sync.WaitGroup
	for index, store := range []*Store{s, other} {
		wg.Add(1)
		go func(index int, store *Store) {
			defer wg.Done()
			results[index], errs[index] = store.RecordPlacement(context.Background(), v.State.Revision, e, testNow.Add(3*time.Minute))
		}(index, store)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].RecordSHA != results[1].RecordSHA || len(readMemory(t, s).State.Placements) != 1 {
		t.Fatal("concurrent receipt duplicated", errs)
	}
}
