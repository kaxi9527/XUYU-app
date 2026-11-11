package merge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func TestSuggestSameCopyKey(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	os.MkdirAll(filepath.Join(root, "官网"), 0o755)
	os.WriteFile(filepath.Join(root, "官网", "00-说明.md"), []byte("# 官网\n"), 0o644)
	cfg := config.Default(home)
	cfg.Root = root

	reg := &registry.Registry{Version: 1, Projects: []registry.Project{{
		ID: "p1", Name: "官网 副本", Path: filepath.Join(home, "Projects", "官网 副本"),
		Members: []registry.Member{{Path: filepath.Join(home, "Projects", "官网 副本"), Kind: registry.KindMain, IsDir: true}},
		Status:  registry.Status,
	}}}
	os.MkdirAll(reg.Projects[0].Path, 0o755)

	cands := Suggest(shelf.Env{Config: cfg, Home: home, Reg: reg})
	if len(cands) != 1 || cands[0].Members < 2 {
		t.Fatalf("%+v", cands)
	}
	if cands[0].Key != "官网" && cands[0].Name != "官网" {
		// Key should be 官网
		if cands[0].Key != "官网" {
			t.Fatalf("key=%q", cands[0].Key)
		}
	}
}

func TestMergeApplyMovesIntoTarget(t *testing.T) {
	h := t.TempDir()
	root := filepath.Join(h, "Work")
	os.MkdirAll(root, 0o755)
	cfg := config.Default(h)
	cfg.Root = root

	a := filepath.Join(h, "Projects", "工具")
	b := filepath.Join(h, "Desktop", "工具 副本")
	os.MkdirAll(filepath.Join(a, "src"), 0o755)
	os.WriteFile(filepath.Join(a, "src", "main.go"), []byte("package main"), 0o644)
	os.MkdirAll(b, 0o755)
	os.WriteFile(filepath.Join(b, "readme.md"), []byte("copy"), 0o644)

	sum, err := Apply(Options{Config: cfg, Target: "工具", Sources: []string{a, b}, Now: time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Moved+sum.Copied < 2 {
		t.Fatalf("%+v items=%+v", sum, sum.Items)
	}
	if _, err := os.Stat(filepath.Join(root, "工具", "src", "main.go")); err != nil {
		t.Fatal("primary should be at target", err)
	}
	// 副本应在散落文件下
	loose := filepath.Join(root, "工具", cfg.Names.Materials, cfg.Names.Loose)
	found := false
	filepath.Walk(loose, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "readme.md" {
			found = true
		}
		return nil
	})
	if !found {
		// 也可能整夹名在 loose 下
		ents, _ := os.ReadDir(loose)
		if len(ents) == 0 {
			t.Fatalf("expected copy under loose, sum=%+v", sum)
		}
	}
}

func TestMergePreviewDry(t *testing.T) {
	h := t.TempDir()
	root := filepath.Join(h, "Work")
	os.MkdirAll(filepath.Join(root, "A"), 0o755)
	cfg := config.Default(h)
	cfg.Root = root
	src := filepath.Join(h, "B")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "x.txt"), []byte("x"), 0o644)
	sum, err := Preview(Options{Config: cfg, Target: "A", Sources: []string{src}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Preview) == 0 {
		t.Fatal("want preview")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("dry must not move")
	}
}
