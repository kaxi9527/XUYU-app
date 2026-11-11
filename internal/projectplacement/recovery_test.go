//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func interruptedRecovery(t *testing.T, copy bool, point string, names ...string) (*Executor, JournalView, Preview) {
	t.Helper()
	e, v, p := executorFixture(t, copy, names...)
	failure := errors.New("interrupted fixture")
	e.fault = func(at string) error {
		if at == point {
			return failure
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if !errors.Is(err, failure) || got.Items[0].Stage != "claimed" && got.Items[0].Stage != "uncertain" {
		t.Fatal("fixture not interrupted at an unresolved action", got.Status, err)
	}
	e.fault = nil
	return e, got, p
}

func recoveryUserTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s:%s", info.Mode(), projectmemory.ObjectIdentity(info))
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + sum(data)
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecoveryPreservesOriginalParentBindings(t *testing.T) {
	for _, when := range []string{"before-review", "after-review"} {
		t.Run(when, func(t *testing.T) {
			e, view, preview := interruptedRecovery(t, false, "after_native_action", "project/inbox/a.txt")
			ctx := context.Background()
			parent := filepath.Dir(preview.snapshot.Rows[0].Source)
			rename := func() {
				if err := os.Rename(parent, parent+"-renamed"); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before-review" {
				rename()
			}
			before := recoveryUserTree(t, view.Intent.Preview.Authority.Project.Path)
			review, err := e.ReviewRecovery(ctx, view.Intent.ID, view.IntentSHA)
			if when == "after-review" {
				if err != nil {
					t.Fatal(err)
				}
				rename()
				before = recoveryUserTree(t, view.Intent.Preview.Authority.Project.Path)
				_, err = e.ConfirmRecovery(ctx, review)
			}
			if !errors.Is(err, ErrChanged) {
				t.Fatal("missing bound ancestor must stay unresolved", err)
			}
			if !reflect.DeepEqual(before, recoveryUserTree(t, view.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("rejected recovery changed project files")
			}
			current, err := e.journal.Read(ctx, view.Intent.ID)
			if err != nil || current.HeadSHA != view.HeadSHA || current.Items[0].Recovery != nil {
				t.Fatal("rejected recovery changed journal", err)
			}
		})
	}
}

func TestRecoveryIgnoresUnrelatedPendingItemDirectoryChange(t *testing.T) {
	e, view, _ := interruptedRecovery(t, false, "after_native_action", "project/first/a.txt", "project/second/b.txt")
	ctx := context.Background()
	remaining, ok := selectedRow(view, view.Items[1].FileID)
	if !ok || view.Items[1].Stage != "pending" {
		t.Fatal("fixture lacks an unrelated pending item")
	}
	parent := filepath.Dir(remaining.Source)
	if err := os.Rename(parent, parent+"-renamed"); err != nil {
		t.Fatal(err)
	}
	before := recoveryUserTree(t, view.Intent.Preview.Authority.Project.Path)
	review, err := e.ReviewRecovery(ctx, view.Intent.ID, view.IntentSHA)
	if err != nil {
		t.Fatal("unrelated directory must not block this item's recovery", err)
	}
	got, err := e.ConfirmRecovery(ctx, review)
	if err != nil || got.Items[0].Stage != "reconciled" || got.Items[1].Stage != "pending" {
		t.Fatal("local recovery failed or executed remaining item", err)
	}
	if !reflect.DeepEqual(before, recoveryUserTree(t, view.Intent.Preview.Authority.Project.Path)) {
		t.Fatal("local recovery changed project files")
	}
}

func TestRecoveryConfirmsObservedLocationsWithoutFileActions(t *testing.T) {
	for _, test := range []struct {
		name, point, outcome string
		copy                 bool
	}{
		{"unstarted-move", "after_claim", "at_source", false},
		{"moved-without-physical", "after_action", "at_target", false},
		{"uncertain-move", "after_native_action", "at_target", false},
		{"unpublished-copy-unproven-object", "copy_before_ready", "copy_unpublished", true},
		{"unpublished-copy-recorded-object", "copy_before_publish", "copy_unpublished", true},
		{"published-copy", "after_action", "copy_published", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, v, _ := interruptedRecovery(t, test.copy, test.point, "project/a.txt", "project/b.txt")
			ctx := context.Background()
			before := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
			memoryBefore, err := e.memory.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil {
				t.Fatal(err)
			}
			if review.snapshot.Outcome != test.outcome {
				t.Fatal("wrong observed outcome", review.snapshot.Outcome)
			}
			if !reflect.DeepEqual(before, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("read-only review changed user files")
			}
			journalAfterReview, err := e.journal.Read(ctx, v.Intent.ID)
			if err != nil || journalAfterReview.HeadSHA != v.HeadSHA {
				t.Fatal("review changed execution journal", err)
			}
			got, err := e.ConfirmRecovery(ctx, review)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "stopped" || !got.StopRequested || got.NeedsReconciliation || got.Items[0].Stage != "reconciled" || got.Items[1].Stage != "pending" {
				t.Fatal("recovery resumed execution or remained unacknowledged", got.Status)
			}
			if !reflect.DeepEqual(v.Items[0].Receipt, got.Items[0].Receipt) {
				t.Fatal("recovery fabricated/replaced physical receipt")
			}
			if !reflect.DeepEqual(before, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("confirmation moved or removed user files")
			}
			memory, err := e.memory.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(memory.State.Reconciliations) != 1 || len(memory.State.Placements) != 0 || memory.State.LastObservedAt != memoryBefore.State.LastObservedAt || !reflect.DeepEqual(memory.State.Coverage, memoryBefore.State.Coverage) {
				t.Fatal("incorrect project history/coverage")
			}
			remaining, ok := selectedRow(v, v.Items[1].FileID)
			if !ok {
				t.Fatal("missing remaining row")
			}
			acted, ok := selectedRow(v, v.Items[0].FileID)
			if !ok {
				t.Fatal("missing acted row")
			}
			if _, err = os.Stat(remaining.Source); err != nil {
				t.Fatal("remaining file moved", err)
			}
			for _, f := range memory.State.Files {
				if f.ID == acted.FileID && test.outcome == "at_target" && f.Fact.Path != acted.Target {
					t.Fatal("move identity not preserved")
				}
			}
			again, err := e.ConfirmRecovery(ctx, review)
			if err != nil || again.HeadSHA != got.HeadSHA {
				t.Fatal("confirmation replay changed receipt", err)
			}
			if _, err = e.Execute(ctx, v.Intent.ID, v.IntentSHA); err != nil {
				t.Fatal("stopped readback failed", err)
			}
			repeated, err := e.memory.Read(ctx)
			if err != nil || repeated.HeadSHA != memory.HeadSHA {
				t.Fatal("completed recovery replay wrote memory", err)
			}
		})
	}
}

func TestRecoveryRestartFinishesOnlyOriginalConfirmedSnapshot(t *testing.T) {
	for _, point := range []string{"after_recovery_confirmed", "before_recovery_memory", "after_recovery_memory"} {
		t.Run(point, func(t *testing.T) {
			e, v, _ := interruptedRecovery(t, false, "after_action", "project/a.txt", "project/b.txt")
			ctx := context.Background()
			review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("lost recovery response")
			e.fault = func(at string) error {
				if at == point {
					return failure
				}
				return nil
			}
			pending, err := e.ConfirmRecovery(ctx, review)
			if !errors.Is(err, failure) || pending.Items[0].Stage != "reconciliation_pending" {
				t.Fatal("missing durable recovery snapshot", err)
			}
			files := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
			journal, err := NewJournal(e.journal.control, e.journal.project)
			if err != nil {
				t.Fatal(err)
			}
			memory, err := projectmemory.NewStore(filepath.Join(e.journal.control, "memory"), e.journal.project)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := NewExecutor(journal, memory, func(context.Context, Intent) (ExecutionAuthority, error) {
				t.Fatal("history retry reread file authorization")
				return ExecutionAuthority{}, ErrConsent
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := reopened.Execute(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil || got.Status != "stopped" || !reflect.DeepEqual(pending.Items[0].Recovery, got.Items[0].Recovery) {
				t.Fatal("original recovery boundary not retained", err)
			}
			state, err := memory.Read(ctx)
			if err != nil || len(state.State.Reconciliations) != 1 {
				t.Fatal("duplicate/missing recovery", err)
			}
			if !reflect.DeepEqual(files, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("restart changed user files")
			}
		})
	}
}

func TestRecoveryRejectsChangedReviewAndPreservesForeignFiles(t *testing.T) {
	for _, kind := range []string{"changed-file", "occupied-target", "paused", "scope-changed", "expired", "tampered"} {
		t.Run(kind, func(t *testing.T) {
			e, v, p := interruptedRecovery(t, false, "after_claim", "project/a.txt")
			ctx := context.Background()
			review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "changed-file":
				write(t, p.snapshot.Rows[0].Source, "later work")
			case "occupied-target":
				write(t, p.snapshot.Rows[0].Target, "foreign")
			case "paused", "scope-changed":
				current, err := e.memory.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				scope := current.State.Scope
				if kind == "scope-changed" {
					scope = projectmemory.Scope{Roots: []string{filepath.Join(current.State.Project.Path, "elsewhere")}}
				}
				if _, err = e.memory.SetTracking(ctx, current.State.Revision, "scope-change", scope, kind != "paused", time.Now()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				review.snapshot.ObservedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
				review.snapshot.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				review.sha = digestJSON(review.snapshot)
			case "tampered":
				review.snapshot.Target.Path = filepath.Join(v.Intent.Preview.Authority.Project.Path, "wrong.txt")
			}
			files := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
			if _, err = e.ConfirmRecovery(ctx, review); err == nil {
				t.Fatal("stale/mutated review accepted")
			}
			if !reflect.DeepEqual(files, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("rejected review changed files")
			}
			current, err := e.memory.Read(ctx)
			if err != nil || len(current.State.Reconciliations) != 0 {
				t.Fatal("rejected review wrote memory", err)
			}
		})
	}
}

func TestRecoveryConfirmedThenPausedRecordsHistoryWithoutReactivation(t *testing.T) {
	e, v, _ := interruptedRecovery(t, false, "after_action", "project/a.txt", "project/b.txt")
	ctx := context.Background()
	review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	var before projectmemory.View
	e.fault = func(point string) error {
		if point != "before_recovery_memory" {
			return nil
		}
		current, err := e.memory.Read(ctx)
		if err != nil {
			return err
		}
		if _, err = e.memory.SetTracking(ctx, current.State.Revision, "pause-after-confirm", current.State.Scope, false, time.Now()); err != nil {
			return err
		}
		before, err = e.memory.Read(ctx)
		return err
	}
	got, err := e.ConfirmRecovery(ctx, review)
	if err != nil || got.Status != "stopped" {
		t.Fatal(err)
	}
	after, err := e.memory.Read(ctx)
	if err != nil || after.State.Enabled || !reflect.DeepEqual(before.State.Files, after.State.Files) || len(after.State.Reconciliations) != 1 || after.State.Reconciliations[0].Disposition != "history_only" {
		t.Fatal("recovery reactivated or overwrote paused state", err)
	}
}

func TestRecoveryEqualBytesDoNotEstablishFileIdentity(t *testing.T) {
	for _, copyPlan := range []bool{false, true} {
		t.Run(fmt.Sprint(copyPlan), func(t *testing.T) {
			e, v, p := interruptedRecovery(t, copyPlan, "after_claim", "project/a.txt")
			ctx := context.Background()
			row := p.snapshot.Rows[0]
			write(t, row.Target, "example")
			review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil {
				t.Fatal(err)
			}
			if review.snapshot.Outcome != "conflict" {
				t.Fatal("same content claimed unrelated object")
			}
			files := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
			if _, err = e.ConfirmRecovery(ctx, review); !errors.Is(err, ErrReconcile) {
				t.Fatal("ambiguous review accepted", err)
			}
			if !reflect.DeepEqual(files, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
				t.Fatal("foreign object changed")
			}
		})
	}
}

func TestRecoveryPreservesUnprovenTemporaryObject(t *testing.T) {
	e, v, p := interruptedRecovery(t, true, "after_claim", "project/a.txt")
	ctx := context.Background()
	row := p.snapshot.Rows[0]
	temporary := copyTemporary(row, v.Items[0].Claim)
	write(t, temporary, "foreign partial content")
	before := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
	review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	if review.snapshot.Outcome != "copy_unpublished" || review.snapshot.CopyObject != "" || review.snapshot.Temporary.Fact == nil {
		t.Fatal("unproven residual fixture missing")
	}
	if _, err = e.ConfirmRecovery(ctx, review); err != nil {
		t.Fatal(err)
	}
	state, err := e.memory.Read(ctx)
	if err != nil || len(state.State.Files) != 1 || len(state.State.Reconciliations) != 1 || state.State.Reconciliations[0].TemporaryPath != temporary {
		t.Fatal("temporary object was promoted as project output", err)
	}
	if !reflect.DeepEqual(before, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
		t.Fatal("unowned temporary object removed or changed")
	}
}

func TestRecoveryRejectsReboundAndProtectedDirectories(t *testing.T) {
	for _, kind := range []string{"symlink", "engineering", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			e, v, p := interruptedRecovery(t, false, "after_claim", "project/a.txt")
			ctx := context.Background()
			root := v.Intent.Preview.Authority.Project.Path
			review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
			if err != nil {
				t.Fatal(err)
			}
			checkedRoot := root
			switch kind {
			case "symlink":
				checkedRoot = root + "-original"
				if err = os.Rename(root, checkedRoot); err != nil {
					t.Fatal(err)
				}
				foreign := root + "-foreign"
				write(t, filepath.Join(foreign, "keep.txt"), "keep")
				if err = os.Symlink(foreign, root); err != nil {
					t.Fatal(err)
				}
			case "engineering":
				if err = os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err = os.Link(p.snapshot.Rows[0].Source, filepath.Join(root, "linked.txt")); err != nil {
					t.Fatal(err)
				}
			}
			files := recoveryUserTree(t, checkedRoot)
			if _, err = e.ConfirmRecovery(ctx, review); err == nil {
				t.Fatal("changed/protected directory accepted")
			}
			if !reflect.DeepEqual(files, recoveryUserTree(t, checkedRoot)) {
				t.Fatal("protected file tree changed")
			}
		})
	}
}

func TestRecoveryNewObservationAfterConfirmationWins(t *testing.T) {
	e, v, _ := interruptedRecovery(t, false, "after_action", "project/a.txt", "project/b.txt")
	ctx := context.Background()
	review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	var observed projectmemory.View
	e.fault = func(point string) error {
		if point != "before_recovery_memory" {
			return nil
		}
		current, err := e.memory.Read(ctx)
		if err != nil {
			return err
		}
		if _, err = e.memory.Observe(ctx, current.State.Revision, "new-observation-after-confirm", current.State.Scope.Digest()); err != nil {
			return err
		}
		observed, err = e.memory.Read(ctx)
		return err
	}
	if _, err = e.ConfirmRecovery(ctx, review); err != nil {
		t.Fatal(err)
	}
	after, err := e.memory.Read(ctx)
	if err != nil || len(after.State.Reconciliations) != 1 || after.State.Reconciliations[0].Disposition != "history_only" || !reflect.DeepEqual(observed.State.Files, after.State.Files) || !reflect.DeepEqual(observed.State.Coverage, after.State.Coverage) || observed.State.LastObservedAt != after.State.LastObservedAt {
		t.Fatal("recovery overwrote the newer real observation", err)
	}
}

func TestRecoveryProjectionFailureAndLostStopEvidenceRemainVisible(t *testing.T) {
	e, v, _ := interruptedRecovery(t, false, "after_action", "project/a.txt")
	ctx := context.Background()
	review, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(e.journal.control, "memory", e.journal.project.ID, "index.json")
	backup := index + ".fixture-backup"
	e.fault = func(point string) error {
		if point != "before_recovery_memory" {
			return nil
		}
		if err := os.Rename(index, backup); err != nil {
			return err
		}
		return os.Mkdir(index, 0700)
	}
	pending, err := e.ConfirmRecovery(ctx, review)
	if !errors.Is(err, ErrReconcile) || pending.Items[0].Stage != "reconciliation_pending" {
		t.Fatal("failed projection was acknowledged", err)
	}
	if err = os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(backup, index); err != nil {
		t.Fatal(err)
	}
	e.fault = nil
	got, err := e.ConfirmRecovery(ctx, review)
	if err != nil || got.Status != "stopped" {
		t.Fatal("projection repair failed", err)
	}
	state, err := e.memory.Read(ctx)
	if err != nil || state.ProjectionPending || len(state.State.Reconciliations) != 1 {
		t.Fatal("projection retry duplicated event", err)
	}
	for _, name := range []string{"stop.json", "stop-commit.json"} {
		if err = os.Remove(filepath.Join(e.journal.directory(), v.Intent.ID, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.journal.Read(ctx, v.Intent.ID); !errors.Is(err, ErrReconcile) {
		t.Fatal("reconciliation lost its permanent stop evidence silently", err)
	}
}

func TestRecoveryReviewIsDetachedAndExpiryIsExact(t *testing.T) {
	e, v, _ := interruptedRecovery(t, false, "after_claim", "project/a.txt")
	p, err := e.ReviewRecovery(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	detached := p.Snapshot()
	detached.Source.Fact.SHA256 = "changed"
	detached.Directories[0].Object = "changed"
	if digestJSON(p.snapshot) != p.sha {
		t.Fatal("snapshot exposed mutable review")
	}
	// Exercise the actual TTL predicate without sleeping or changing clocks.
	observed, _ := time.Parse(time.RFC3339Nano, p.snapshot.ObservedAt)
	d := RecoveryDecision{Review: p.snapshot, ReviewSHA: p.sha, ConfirmedAt: observed.Add(9 * time.Minute).Format(time.RFC3339Nano)}
	if err = validateRecovery(v, d); err != nil {
		t.Fatal("valid TTL rejected", err)
	}
	d.ConfirmedAt = p.snapshot.ExpiresAt
	if !errors.Is(validateRecovery(v, d), ErrConsent) {
		t.Fatal("expiry boundary accepted")
	}
}

func TestRecoveryTwoInstancesKeepOneConfirmation(t *testing.T) {
	e, v, _ := interruptedRecovery(t, false, "after_action", "project/a.txt")
	ctx := context.Background()
	p, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	j, err := NewJournal(e.journal.control, e.journal.project)
	if err != nil {
		t.Fatal(err)
	}
	m, err := projectmemory.NewStore(filepath.Join(e.journal.control, "memory"), e.journal.project)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewExecutor(j, m, e.current)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	views := make([]JournalView, 2)
	errs := make([]error, 2)
	for i, executor := range []*Executor{e, other} {
		wg.Add(1)
		go func(index int, ex *Executor) { defer wg.Done(); views[index], errs[index] = ex.ConfirmRecovery(ctx, p) }(i, executor)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || views[0].HeadSHA != views[1].HeadSHA {
		t.Fatal("same confirmation was not idempotent", errs)
	}
	if _, err = other.ConfirmRecovery(ctx, second); !errors.Is(err, ErrOperation) {
		t.Fatal("different review reused the confirmed claim", err)
	}
	state, err := m.Read(ctx)
	if err != nil || len(state.State.Reconciliations) != 1 {
		t.Fatal("concurrent confirmation duplicated memory", err)
	}
}

func TestRecoveryHundredRealFilesOnlyChangesOneDirectoryEntry(t *testing.T) {
	names := make([]string, 100)
	for i := range names {
		names[i] = fmt.Sprintf("project/item-%03d.txt", i)
	}
	e, v, _ := interruptedRecovery(t, false, "after_action", names...)
	ctx := context.Background()
	before, err := e.memory.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files := recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)
	p, err := e.ReviewRecovery(ctx, v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ConfirmRecovery(ctx, p); err != nil {
		t.Fatal(err)
	}
	after, err := e.memory.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, old := range before.State.Files {
		for _, now := range after.State.Files {
			if old.ID == now.ID && old != now {
				changed++
				if now.ID != v.Items[0].FileID || now.Fact.Path != p.snapshot.Target.Path {
					t.Fatal("wrong member changed")
				}
			}
		}
	}
	if changed != 1 || len(after.State.Files) != 100 || !reflect.DeepEqual(before.State.Coverage, after.State.Coverage) || !reflect.DeepEqual(files, recoveryUserTree(t, v.Intent.Preview.Authority.Project.Path)) {
		t.Fatal("partial recovery modified unrelated files/coverage")
	}
}
