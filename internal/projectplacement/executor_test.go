//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func executorFixture(t *testing.T, copyPlan bool, names ...string) (*Executor, JournalView, Preview) {
	t.Helper()
	a, proposal := inputs(t, names...)
	control := filepath.Join(a.Boundaries[0], "control")
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	a.Excluded = []string{control}
	memory, err := projectmemory.NewStore(filepath.Join(control, "memory"), a.Project)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	if _, err = memory.Initialize(context.Background(), "enable", a.Scope, now); err != nil {
		t.Fatal(err)
	}
	observation := projectmemory.Observation{ScopeSHA: a.Scope.Digest(), StartedAt: now.UTC().Format(time.RFC3339Nano), EndedAt: now.UTC().Format(time.RFC3339Nano), Complete: true}
	for _, path := range append(append([]string{}, a.Scope.Roots...), a.Scope.Files...) {
		observation.Coverage = append(observation.Coverage, projectmemory.Coverage{Path: path, Status: "complete"})
	}
	for _, suggestion := range proposal.Suggestions {
		fact := suggestion.File.Fact
		fact.HashState, fact.SHA256 = "metadata_only", "" // Preview upgrades the physical hash, not the old memory CAS.
		observation.Files = append(observation.Files, fact)
	}
	if _, err = memory.RecordObservation(context.Background(), 1, "observe", observation); err != nil {
		t.Fatal(err)
	}
	state, err := memory.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for index := range proposal.Suggestions {
		for _, file := range state.State.Files {
			if file.Fact.Path == proposal.Suggestions[index].File.Fact.Path {
				proposal.Suggestions[index].File = file
			}
		}
	}
	proposal.MemoryRevision, proposal.MemoryHead = state.State.Revision, state.HeadSHA
	proposal.DecisionSHA = projectcontext.PlacementDecisionSHA(state.State)
	preview, err := BuildPreview(context.Background(), a, proposal, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for index, row := range preview.snapshot.Rows {
		if row.Action != "move" {
			t.Fatal("fixture not placeable", row.Code)
		}
		ids = append(ids, row.FileID)
		if copyPlan {
			preview.snapshot.Rows[index].Action = "copy"
			preview.snapshot.Rows[index].Reason = "显式复制计划测试；真实同卷复制，不冒充跨卷挂载验收"
		}
	}
	if copyPlan {
		preview.sha = digestJSON(preview.snapshot)
	}
	journal, err := NewJournal(control, a.Project)
	if err != nil {
		t.Fatal(err)
	}
	view, err := journal.Prepare(context.Background(), "execution-one", preview, ids)
	if err != nil {
		t.Fatal(err)
	}
	view, err = journal.Confirm(context.Background(), view.Intent.ID, view.IntentSHA, a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(journal, memory, func(context.Context, Intent) (ExecutionAuthority, error) {
		return ExecutionAuthority{Authority: a, Profile: proposal.Profile}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, view, preview
}

func TestExecutorMovesRealFilesAndWritesSameProjectMemoryOnce(t *testing.T) {
	e, v, p := executorFixture(t, false, "project/a.txt", "loose/b.txt")
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || got.Status != "completed" || got.NeedsReconciliation {
		t.Fatal("real placement incomplete", got.Status, err)
	}
	memory, err := e.memory.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if memory.State.Project != p.snapshot.Authority.Project || len(memory.State.Placements) != 2 || len(memory.State.Files) != 2 {
		t.Fatal("project identity or file count changed")
	}
	for _, row := range p.snapshot.Rows {
		if _, err = os.Lstat(row.Source); !os.IsNotExist(err) {
			t.Fatal("move left its source", err)
		}
		data, err := os.ReadFile(row.Target)
		if err != nil || string(data) != "example" {
			t.Fatal("missing destination", err)
		}
		found := false
		for _, file := range memory.State.Files {
			if file.ID == row.FileID {
				found = true
				if file.Fact.Path != row.Target || file.Fact.SHA256 != row.Before.SHA256 || file.Fact.ObjectKey != row.Object {
					t.Fatal("move lost file identity or verified content")
				}
			}
		}
		if !found {
			t.Fatal("file id disappeared")
		}
		item := got.Items[itemIndex(got, row.FileID)]
		if item.ClaimedAt == "" || item.PhysicalAt == "" || !goodSHA(item.PhysicalSHA) || item.Memory == nil || !goodSHA(item.Memory.RecordSHA) {
			t.Fatal("missing original physical/memory receipt")
		}
	}
	before := disk(t, p.snapshot.Authority.Boundaries[0])
	again, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || again.HeadSHA != got.HeadSHA || !reflect.DeepEqual(before, disk(t, p.snapshot.Authority.Boundaries[0])) {
		t.Fatal("response retry changed files or receipts", err)
	}
	read, err := e.memory.RecentEvents(context.Background(), memory.State.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = read // Public history must decode the new typed event after reopening.
}

func TestExecutorCopiesRealBytesWithIndependentIdentityAndOrigin(t *testing.T) {
	e, v, p := executorFixture(t, true, "project/a.txt")
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || got.Status != "completed" {
		t.Fatal("copy incomplete", got.Status, err)
	}
	row := p.snapshot.Rows[0]
	for _, path := range []string{row.Source, row.Target} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "example" {
			t.Fatal("source or copy lost", err)
		}
	}
	memory, err := e.memory.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(memory.State.Files) != 2 || len(memory.State.Placements) != 1 {
		t.Fatal("copy did not create its own identity")
	}
	link := memory.State.Placements[0]
	if link.Outcome != "copied" || link.SourceFileID != row.FileID || link.TargetFileID == row.FileID || link.From != row.Source || link.To != row.Target {
		t.Fatal("copy lineage missing")
	}
	item := got.Items[0]
	if item.Copy == nil || item.Copy.Object == row.Object || item.Copy.Object != item.Receipt.TargetObject {
		t.Fatal("copy publication lacks original staged object")
	}
	if _, err = os.Lstat(item.Copy.Path); !os.IsNotExist(err) {
		t.Fatal("published copy left its staging name", err)
	}
}

func TestExecutorMemoryFailuresRetryWithoutAnotherPhysicalAction(t *testing.T) {
	for _, stage := range []string{"after_physical", "before_memory", "after_memory"} {
		t.Run(stage, func(t *testing.T) {
			e, v, p := executorFixture(t, false, "project/a.txt")
			failure := errors.New("simulated lost receipt")
			e.fault = func(point string) error {
				if point == stage {
					return failure
				}
				return nil
			}
			got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if !errors.Is(err, failure) || got.Items[0].Stage != "memory_pending" {
				t.Fatal("missing durable pending state", got.Status, err)
			}
			row := p.snapshot.Rows[0]
			info, err := os.Lstat(row.Target)
			if err != nil {
				t.Fatal(err)
			}
			identity := projectmemory.ObjectIdentity(info)
			if _, err = os.Lstat(row.Source); !os.IsNotExist(err) {
				t.Fatal("physical move did not happen")
			}
			e.fault = nil
			reopened, err := NewJournal(e.journal.control, e.journal.project)
			if err != nil {
				t.Fatal(err)
			}
			e.journal = reopened
			got, err = e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if err != nil || got.Status != "completed" {
				t.Fatal("memory-only retry failed", got.Status, err)
			}
			info, err = os.Lstat(row.Target)
			if err != nil || projectmemory.ObjectIdentity(info) != identity {
				t.Fatal("retry changed physical object")
			}
			memory, err := e.memory.Read(context.Background())
			if err != nil || len(memory.State.Placements) != 1 {
				t.Fatal("memory event duplicated", err)
			}
		})
	}
}

func TestExecutorCrashAfterActionNeverReplaysClaim(t *testing.T) {
	e, v, p := executorFixture(t, false, "project/a.txt")
	failure := errors.New("simulated process exit before physical receipt")
	e.fault = func(point string) error {
		if point == "after_action" {
			return failure
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if !errors.Is(err, failure) || got.Items[0].Stage != "claimed" {
		t.Fatal("lost physical receipt not preserved", err)
	}
	row := p.snapshot.Rows[0]
	before := disk(t, p.snapshot.Authority.Project.Path)
	e.fault = nil
	if _, err = e.Execute(context.Background(), v.Intent.ID, v.IntentSHA); !errors.Is(err, ErrReconcile) {
		t.Fatal("claimed action replayed", err)
	}
	if !reflect.DeepEqual(before, disk(t, p.snapshot.Authority.Project.Path)) {
		t.Fatal("recovery attempt changed files")
	}
	if _, err = os.Lstat(row.Target); err != nil {
		t.Fatal(err)
	}
}
