package home

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestExactPublishesUndoBeforeMoving(t *testing.T) {
	cfg, m := exactFixture(t)
	called := false
	_, err := ApplyExactContextWithUndoReady(context.Background(), cfg, []ExactMove{m}, func(path string) error {
		called = true
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(m.Source); err != nil {
			t.Fatal("source moved before receipt publication", err)
		}
		return errors.New("publication failed")
	})
	if !called || err == nil {
		t.Fatal("expected publication failure", err)
	}
	if _, err = os.Stat(m.Source); err != nil {
		t.Fatal("source moved after publication failed", err)
	}
}

func exactFixture(t *testing.T) (config.Config, ExactMove) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	src := filepath.Join(root, "file.txt")
	if e := os.WriteFile(src, []byte("original"), 0644); e != nil {
		t.Fatal(e)
	}
	snap, e := Snapshot(src)
	if e != nil {
		t.Fatal(e)
	}
	return cfg, ExactMove{Source: src, SourceRoot: root, Target: filepath.Join(cfg.Root, "project", "需求", "file.txt"), Snapshot: snap}
}
func TestExactJournalFailureDoesNotMove(t *testing.T) {
	cfg, m := exactFixture(t)
	if e := os.MkdirAll(filepath.Dir(undoDir(cfg)), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(undoDir(cfg), []byte("block"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := ApplyExact(cfg, []ExactMove{m}); e == nil {
		t.Fatal("expected error")
	}
	if _, e := os.Stat(m.Source); e != nil {
		t.Fatal("source moved without journal")
	}
}
func TestExclusiveMoveNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "target")
	os.WriteFile(dest, []byte("existing"), 0644)
	for i := 0; i < 3; i++ {
		src, e := os.CreateTemp(dir, "source-")
		if e != nil {
			t.Fatal(e)
		}
		src.WriteString("new")
		src.Close()
		if e := renameExclusive(src.Name(), dest); e == nil {
			t.Fatal("destination overwritten")
		}
		if _, e := os.Stat(src.Name()); e != nil {
			t.Fatal("source lost")
		}
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "existing" {
		t.Fatal("collision lost content")
	}
}
func TestExactUndoPreservesLaterEdits(t *testing.T) {
	cfg, m := exactFixture(t)
	s, e := ApplyExact(cfg, []ExactMove{m})
	if e != nil || s.Moved != 1 {
		t.Fatalf("%+v %v", s, e)
	}
	os.WriteFile(m.Target, []byte("user edited after move"), 0644)
	u, e := Undo(cfg, s.UndoPath)
	if e != nil || u.Failed != 1 {
		t.Fatalf("%+v %v", u, e)
	}
	if b, _ := os.ReadFile(m.Target); string(b) != "user edited after move" {
		t.Fatal("later edit lost")
	}
}

func TestExactExternalProjectReceiptAndRestore(t *testing.T) {
	cfg, m := exactFixture(t)
	m.TargetRoot = filepath.Join(filepath.Dir(cfg.Root), "原地项目")
	m.Target = filepath.Join(m.TargetRoot, "需求", "file.txt")
	if err := os.MkdirAll(m.TargetRoot, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := ApplyExact(cfg, []ExactMove{m})
	if err != nil || s.Moved != 1 {
		t.Fatal(s, err)
	}
	items, uncertain, err := InterruptedExactItems(cfg, s.UndoPath)
	if err != nil || uncertain != 0 || len(items) != 1 || items[0].Target != m.Target {
		t.Fatal(items, uncertain, err)
	}
	u, err := Undo(cfg, s.UndoPath)
	if err != nil || u.Restored != 1 {
		t.Fatal(u, err)
	}
	if data, err := os.ReadFile(m.Source); err != nil || string(data) != "original" {
		t.Fatal("source not restored", err)
	}
	if _, err = os.Stat(m.TargetRoot); err != nil {
		t.Fatal("external project removed", err)
	}
}
func TestExactCopyPreservesHiddenFilesModesAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	os.MkdirAll(filepath.Join(src, "assets"), 0750)
	os.WriteFile(filepath.Join(src, ".config"), []byte("config"), 0600)
	os.WriteFile(filepath.Join(src, "assets", "run.sh"), []byte("echo hi"), 0755)
	before, e := Snapshot(src)
	if e != nil {
		t.Fatal(e)
	}
	dst := filepath.Join(dir, "copied")
	if e := copyExact(src, dst); e != nil {
		t.Fatal(e)
	}
	after, e := Snapshot(dst)
	if e != nil || before != after {
		t.Fatalf("copy differs: %s %s %v", before, after, e)
	}
}
