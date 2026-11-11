//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func undoFixture(t *testing.T, names ...string) (*Executor, JournalView, Preview, UndoGroup) {
	t.Helper()
	e, v, p := executorFixture(t, false, names...)
	e.busy = func(context.Context, string) error { return nil }
	v, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.ReviewUndo(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, row := range u.Snapshot().Rows {
		if row.Action != "restore" {
			t.Fatal("not ready", row.Reason)
		}
		ids = append(ids, row.FileID)
	}
	v, err = e.ConfirmUndo(context.Background(), "undo-group", u, ids)
	if err != nil {
		t.Fatal("confirm", err)
	}
	return e, v, p, v.UndoGroups[0]
}

func TestUndoRestoresGroupAndSameProjectMemoryOnce(t *testing.T) {
	e, v, p, g := undoFixture(t, "project/a.txt", "loose/b.txt")
	beforeItems := append([]ItemState{}, v.Items...)
	got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if err != nil || got.UndoGroups[0].Status != "completed" {
		t.Fatal("restore", err, got.UndoGroups)
	}
	if !reflect.DeepEqual(got.Items, beforeItems) {
		t.Fatal("original receipts changed")
	}
	memory, err := e.memory.Read(context.Background())
	if err != nil || len(memory.State.Undos) != 2 || len(memory.State.Placements) != 2 {
		t.Fatal("missing history", err)
	}
	for _, row := range p.Snapshot().Rows {
		data, err := os.ReadFile(row.Source)
		if err != nil || string(data) != "example" {
			t.Fatal("not restored", err)
		}
		info, err := os.Lstat(row.Source)
		if err != nil {
			t.Fatal(err)
		}
		key, _, _ := object(info)
		if key != row.Object {
			t.Fatal("identity lost")
		}
		if _, err = os.Lstat(row.Target); !os.IsNotExist(err) {
			t.Fatal("target remains", err)
		}
		found := false
		for _, f := range memory.State.Files {
			if f.ID == row.FileID {
				found = f.Fact.Path == row.Source && f.Fact.SHA256 == row.Before.SHA256
			}
		}
		if !found {
			t.Fatal("catalog not restored")
		}
	}
	replayed, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if err != nil || replayed.HeadSHA != got.HeadSHA {
		t.Fatal("repeated execution", err)
	}
	again, err := e.memory.Read(context.Background())
	if err != nil || again.HeadSHA != memory.HeadSHA {
		t.Fatal("repeated memory", err)
	}
	review, err := e.ReviewUndo(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range review.Snapshot().Rows {
		if row.Action == "restore" {
			t.Fatal("restored object offered again")
		}
	}
}

func TestUndoConflictsPreserveFilesAndContinueGroup(t *testing.T) {
	e, v, p, g := undoFixture(t, "project/a.txt", "project/b.txt")
	row := p.Snapshot().Rows[0]
	if err := os.WriteFile(row.Source, []byte("new work"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if err != nil || got.UndoGroups[0].Status != "completed" {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, item := range got.UndoGroups[0].Items {
		counts[item.Stage]++
	}
	if counts["conflict"] != 1 || counts["undone"] != 1 {
		t.Fatal(counts)
	}
	data, _ := os.ReadFile(row.Source)
	if string(data) != "new work" {
		t.Fatal("overwrote occupied original location")
	}
	data, _ = os.ReadFile(row.Target)
	if string(data) != "example" {
		t.Fatal("lost original content")
	}
}

func TestUndoStopPersistsAndReleasesUnstartedClaims(t *testing.T) {
	e, v, _, g := undoFixture(t, "project/a.txt")
	if _, err := e.journal.RequestUndoStop(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if !errors.Is(err, ErrStopped) || got.UndoGroups[0].Status != "stopped" {
		t.Fatal(err)
	}
	u, err := e.ReviewUndo(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || u.Snapshot().Rows[0].Action != "restore" {
		t.Fatal("not released", err)
	}
	got, err = e.ConfirmUndo(context.Background(), "second-group", u, []string{u.Snapshot().Rows[0].FileID})
	if err != nil {
		t.Fatal(err)
	}
	second := got.UndoGroups[1]
	got, err = e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, second.ID, second.ManifestSHA)
	if err != nil || got.UndoGroups[1].Status != "completed" {
		t.Fatal("second group", err)
	}
}

func TestUndoInterruptedActionNeverReplaysAndMemoryCanRepair(t *testing.T) {
	for _, point := range []string{"undo_after_claim", "undo_after_native_action", "undo_after_action", "undo_after_physical", "undo_after_memory"} {
		t.Run(point, func(t *testing.T) {
			e, v, p, g := undoFixture(t, "project/a.txt")
			injected := errors.New("simulated interruption")
			e.fault = func(at string) error {
				if at == point {
					return injected
				}
				return nil
			}
			if _, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA); !errors.Is(err, injected) {
				t.Fatal("fault not reached", err)
			}
			j, err := NewJournal(e.journal.control, e.journal.project)
			if err != nil {
				t.Fatal(err)
			}
			e, err = NewExecutor(j, e.memory, e.current)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
			repair := point == "undo_after_physical" || point == "undo_after_memory"
			if repair {
				if err != nil || got.UndoGroups[0].Items[0].Stage != "undone" {
					t.Fatal("repair failed", err)
				}
			} else if !errors.Is(err, ErrReconcile) {
				t.Fatal("uncertain work replayed", err)
			}
			row := p.Snapshot().Rows[0]
			want := row.Source
			if point == "undo_after_claim" {
				want = row.Target
			}
			data, err := os.ReadFile(want)
			if err != nil || string(data) != "example" {
				t.Fatal("lost file", err)
			}
			memory, err := e.memory.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			if repair {
				n = 1
			}
			if len(memory.State.Undos) != n {
				t.Fatal("fabricated or duplicated memory", len(memory.State.Undos))
			}
		})
	}
}

func TestUndoPlanMutationBeforeActionDoesNotMove(t *testing.T) {
	e, v, p, g := undoFixture(t, "project/a.txt")
	e.fault = func(at string) error {
		if at == "undo_before_action" {
			path := filepath.Join(e.journal.undoPath(v.Intent.ID, g.ID), undoPartName(0))
			if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	if _, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA); err == nil {
		t.Fatal("accepted changed plan")
	}
	row := p.Snapshot().Rows[0]
	if _, err := os.Lstat(row.Source); !os.IsNotExist(err) {
		t.Fatal("moved after plan changed")
	}
	if data, err := os.ReadFile(row.Target); err != nil || string(data) != "example" {
		t.Fatal("lost file")
	}
}

func TestUndoChangedSourceAndAuthorityStayUntouched(t *testing.T) {
	for _, kind := range []string{"content", "object", "parent", "busy", "hardlink", "paused", "scope", "consent", "missing-plan"} {
		t.Run(kind, func(t *testing.T) {
			e, v, p, g := undoFixture(t, "project/a.txt")
			row := p.Snapshot().Rows[0]
			switch kind {
			case "content":
				if err := os.WriteFile(row.Target, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "object":
				if err := os.Rename(row.Target, row.Target+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(row.Target, []byte("example"), 0600); err != nil {
					t.Fatal(err)
				}
			case "parent":
				parent := filepath.Dir(row.Target)
				if err := os.Rename(parent, parent+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(parent+"-saved", filepath.Base(row.Target)), row.Target); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(row.Target, row.Target+".link"); err != nil {
					t.Fatal(err)
				}
			case "busy":
				e.busy = func(context.Context, string) error { return errors.New("open writer") }
			case "paused", "scope":
				m, err := e.memory.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				scope := m.State.Scope
				if kind == "scope" {
					scope.Roots = []string{filepath.Join(m.State.Project.Path, "other")}
				}
				if _, err = e.memory.SetTracking(context.Background(), m.State.Revision, "change-scope", scope, kind != "paused", time.Now()); err != nil {
					t.Fatal(err)
				}
			case "consent":
				e.current = func(context.Context, Intent) (ExecutionAuthority, error) { return ExecutionAuthority{}, ErrConsent }
			case "missing-plan":
				if err := os.Remove(filepath.Join(e.journal.undoPath(v.Intent.ID, g.ID), undoPartName(0))); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
			if kind == "paused" || kind == "scope" || kind == "consent" || kind == "missing-plan" {
				if err == nil {
					t.Fatal("authority/plan change accepted")
				}
			} else if err != nil || got.UndoGroups[0].Items[0].Stage != "conflict" {
				t.Fatal("not a per-file conflict", err)
			}
			if _, err := os.Lstat(row.Source); !os.IsNotExist(err) {
				t.Fatal("restored changed file", err)
			}
		})
	}
}

func TestUndoStopAfterOneRestoresNoMoreFiles(t *testing.T) {
	e, v, _, g := undoFixture(t, "project/a.txt", "project/b.txt")
	e.fault = func(at string) error {
		if at == "undo_after_memory" {
			_, err := e.journal.RequestUndoStop(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA, time.Now())
			return err
		}
		return nil
	}
	got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if !errors.Is(err, ErrStopped) || got.UndoGroups[0].Status != "stopped" {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, item := range got.UndoGroups[0].Items {
		counts[item.Stage]++
	}
	if counts["undone"] != 1 || counts["pending"] != 1 {
		t.Fatal(counts)
	}
}

func TestUndoCopyIsKeptAndReconciledMoveCanRestore(t *testing.T) {
	t.Run("copy", func(t *testing.T) {
		e, v, _ := executorFixture(t, true, "project/a.txt")
		e.busy = func(context.Context, string) error { return nil }
		v, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
		if err != nil {
			t.Fatal(err)
		}
		u, err := e.ReviewUndo(context.Background(), v.Intent.ID, v.IntentSHA)
		if err != nil || u.Snapshot().Rows[0].Action != "keep" {
			t.Fatal("copy offered for deletion", err)
		}
		if _, err = e.ConfirmUndo(context.Background(), "copy-delete", u, []string{u.Snapshot().Rows[0].FileID}); err == nil {
			t.Fatal("copy selected")
		}
	})
	t.Run("reconciled", func(t *testing.T) {
		e, v, p := interruptedRecovery(t, false, "after_native_action", "project/a.txt")
		r, err := e.ReviewRecovery(context.Background(), v.Intent.ID, v.IntentSHA)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.ConfirmRecovery(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		u, err := e.ReviewUndo(context.Background(), v.Intent.ID, v.IntentSHA)
		if err != nil {
			t.Fatal(err)
		}
		v, err = e.ConfirmUndo(context.Background(), "after-recovery", u, []string{u.Snapshot().Rows[0].FileID})
		if err != nil {
			t.Fatal(err)
		}
		g := v.UndoGroups[0]
		got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
		if err != nil || got.UndoGroups[0].Status != "completed" {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(p.Snapshot().Rows[0].Source); err != nil || string(data) != "example" {
			t.Fatal("not restored", err)
		}
	})
}

func TestUndoLargeGroupHasOneConfirmationAndPerActionMemory(t *testing.T) {
	names := []string{}
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("project/file-%03d.txt", i))
	}
	e, v, _, g := undoFixture(t, names...)
	got, err := e.ExecuteUndo(context.Background(), v.Intent.ID, v.IntentSHA, g.ID, g.ManifestSHA)
	if err != nil || len(got.UndoGroups) != 1 || got.UndoGroups[0].Status != "completed" {
		t.Fatal(err)
	}
	m, err := e.memory.Read(context.Background())
	if err != nil || len(m.State.Undos) != len(names) {
		t.Fatal("incomplete group", err)
	}
	for _, u := range m.State.Undos {
		if u.Disposition != "applied" {
			t.Fatal("group used stale memory revision", u.Disposition)
		}
	}
}
