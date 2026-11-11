package home

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExactStopBeforeStartingDoesNotCreateTarget(t *testing.T) {
	cfg, m := exactFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sum, err := ApplyExactContext(ctx, cfg, []ExactMove{m})
	if err != nil || sum.Stopped != 1 || sum.Moved != 0 || sum.Failed != 0 {
		t.Fatalf("%+v %v", sum, err)
	}
	if _, err = os.Stat(cfg.Root); !os.IsNotExist(err) {
		t.Fatal("cancelled execution created target", err)
	}
	if b, err := os.ReadFile(m.Source); err != nil || string(b) != "original" {
		t.Fatal("source changed", err)
	}
}

func TestExactStopFinishesCurrentFileAndRestoresCompletedMoves(t *testing.T) {
	cfg, first := exactFixture(t)
	moves := []ExactMove{first}
	for _, name := range []string{"second.txt", "third.txt"} {
		src := filepath.Join(first.SourceRoot, name)
		if err := os.WriteFile(src, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		snap, err := Snapshot(src)
		if err != nil {
			t.Fatal(err)
		}
		moves = append(moves, ExactMove{Source: src, Target: filepath.Join(filepath.Dir(first.Target), name), SourceRoot: first.SourceRoot, Snapshot: snap})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Once the first file is visible at its destination, stop during the next
	// file's validation. That current operation finishes; the third never starts.
	moves[1].ValidateContext = func() error {
		if _, err := os.Stat(first.Target); err == nil {
			cancel()
		}
		return nil
	}
	sum, err := ApplyExactContext(ctx, cfg, moves)
	if err != nil || sum.Moved != 2 || sum.Stopped != 1 || sum.Failed != 0 {
		t.Fatalf("%+v %v", sum, err)
	}
	if _, err = os.Stat(moves[2].Source); err != nil {
		t.Fatal("unstarted file moved", err)
	}
	if _, err = os.Stat(moves[2].Target); !os.IsNotExist(err) {
		t.Fatal("unstarted target exists", err)
	}
	restored, err := Undo(cfg, sum.UndoPath)
	if err != nil || restored.Restored != 2 || restored.Unchanged != 1 || restored.Skipped != 0 || restored.Failed != 0 {
		t.Fatalf("undo=%+v %v", restored, err)
	}
	for i, m := range moves {
		want := "original"
		if i > 0 {
			want = filepath.Base(m.Source)
		}
		b, err := os.ReadFile(m.Source)
		if err != nil || string(b) != want {
			t.Fatal("recovery content mismatch", err)
		}
	}
}
