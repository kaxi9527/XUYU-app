package home

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

func testCfg(t *testing.T) (config.Config, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	os.MkdirAll(root, 0o755)
	cfg := config.Default(home)
	cfg.Root = root
	return cfg, home
}

func TestHomeFileSameDiskMoveAndUndo(t *testing.T) {
	cfg, home := testCfg(t)
	srcDir := filepath.Join(home, "Desktop")
	os.MkdirAll(srcDir, 0o755)
	src := filepath.Join(srcDir, "海报.png")
	os.WriteFile(src, []byte("png-bytes"), 0o644)

	sum, err := Apply(Options{Config: cfg, Now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), Jobs: []Job{{
		Source: src, Project: "海报项目", Area: AreaLoose,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Moved != 1 || sum.Failed != 0 {
		t.Fatalf("summary=%+v items=%+v", sum, sum.Items)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be gone after same-disk move")
	}
	got := sum.Items[0].Target
	b, _ := os.ReadFile(got)
	if string(b) != "png-bytes" {
		t.Fatalf("content %q", b)
	}
	if sum.UndoPath == "" {
		t.Fatal("want undo path")
	}
	ur, err := Undo(cfg, sum.UndoPath)
	if err != nil {
		t.Fatal(err)
	}
	if ur.Restored != 1 {
		t.Fatalf("undo=%+v", ur)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source should be back")
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatal("target should be gone after undo")
	}
}

func TestHomeFileCollisionSuffix(t *testing.T) {
	cfg, home := testCfg(t)
	proj := filepath.Join(cfg.Root, "P")
	loose := filepath.Join(proj, cfg.Names.Materials, cfg.Names.Loose)
	os.MkdirAll(loose, 0o755)
	os.WriteFile(filepath.Join(loose, "a.txt"), []byte("old"), 0o644)
	src := filepath.Join(home, "a.txt")
	os.WriteFile(src, []byte("new"), 0o644)
	sum, err := Apply(Options{Config: cfg, Jobs: []Job{{Source: src, Project: "P", Area: AreaLoose}}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Moved != 1 {
		t.Fatalf("%+v", sum)
	}
	if filepath.Base(sum.Items[0].Target) != "a-02.txt" {
		t.Fatalf("target=%s", sum.Items[0].Target)
	}
}

func TestRejectSensitive(t *testing.T) {
	cfg, home := testCfg(t)
	src := filepath.Join(home, ".env")
	os.WriteFile(src, []byte("x"), 0o644)
	sum, err := Preview(Options{Config: cfg, Jobs: []Job{{Source: src, Project: "P", Area: AreaLoose}}})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Preview[0].Skip {
		t.Fatalf("%+v", sum.Preview[0])
	}
}

func TestHomeWholeProject(t *testing.T) {
	cfg, home := testCfg(t)
	src := filepath.Join(home, "Projects", "官网")
	os.MkdirAll(filepath.Join(src, "src"), 0o755)
	os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(src, "README.md"), []byte("# 官网"), 0o644)
	sum, err := Apply(Options{Config: cfg, Jobs: []Job{{Source: src, Project: "官网", Whole: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Moved != 1 {
		t.Fatalf("%+v", sum)
	}
	dst := filepath.Join(cfg.Root, "官网")
	if _, err := os.Stat(filepath.Join(dst, "src", "main.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("project dir should have moved")
	}
}

func TestCheckSHAOnCopyFallbackUsesPut(t *testing.T) {
	// sanity: put.FileSHA256 available
	_, home := testCfg(t)
	f := filepath.Join(home, "x.bin")
	os.WriteFile(f, []byte("abc"), 0o644)
	sum, err := put.FileSHA256(f)
	if err != nil || len(sum) != 64 {
		t.Fatal(sum, err)
	}
}

func TestNestedRootRejectedViaConfig(t *testing.T) {
	// wired through config.CheckNewRoot — smoke that home doesn't need it
	home := t.TempDir()
	old := filepath.Join(home, "Work")
	os.MkdirAll(filepath.Join(old, "x"), 0o755)
	if _, err := config.CheckNewRoot(filepath.Join(old, "x"), old, home); err == nil {
		t.Fatal("want error")
	}
}
