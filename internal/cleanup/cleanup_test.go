package cleanup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
)

func TestSuggestEmptyAndCopy(t *testing.T) {
	home := t.TempDir()
	desk := filepath.Join(home, "Desktop")
	os.MkdirAll(desk, 0o755)
	empty := filepath.Join(desk, "空的旧文件夹")
	os.MkdirAll(empty, 0o755)
	os.WriteFile(filepath.Join(desk, "报告 副本.pdf"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(desk, "正常文件.txt"), []byte("y"), 0o644)

	root := filepath.Join(home, "Work")
	os.MkdirAll(filepath.Join(root, "_总控"), 0o755)
	cfg := config.Default(home)
	cfg.Root = root

	reg := &registry.Registry{
		GeneratedAt: time.Now(),
		Projects: []registry.Project{{
			ID: "p1", Name: "官网", Path: filepath.Join(home, "Projects", "官网"),
			Members: []registry.Member{
				{Path: filepath.Join(home, "Projects", "官网"), Kind: registry.KindMain, IsDir: true},
				{Path: filepath.Join(home, "Downloads", "官网 副本"), Kind: registry.KindCopy, IsDir: true},
				{Path: filepath.Join(home, "Downloads", "官网-r3.zip"), Kind: registry.KindArchive, IsDir: false},
			},
		}},
	}
	os.MkdirAll(filepath.Join(home, "Projects", "官网"), 0o755)
	os.MkdirAll(filepath.Join(home, "Downloads", "官网 副本"), 0o755)
	os.WriteFile(filepath.Join(home, "Downloads", "官网-r3.zip"), []byte("zip"), 0o644)

	got, err := Suggest(Options{Config: cfg, Home: home, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	var cats []string
	pre := 0
	for _, s := range got {
		cats = append(cats, s.Category+":"+s.Name)
		if s.PreCheck {
			pre++
			if s.Confidence != ConfRecommend {
				t.Fatalf("precheck must be recommend: %+v", s)
			}
		}
		if s.Confidence == ConfDecide && s.PreCheck {
			t.Fatalf("decide must not precheck: %+v", s)
		}
	}
	if len(got) < 3 {
		t.Fatalf("want >=3 suggestions, got %v", cats)
	}
	if pre < 1 {
		t.Fatalf("want some prechecked, got %v", cats)
	}
}

func TestProbeProjectLight(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "old.bak"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "x", "a"), []byte("1"), 0o644)
	got, err := ProbeProjectLight(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("got %d", len(got))
	}
	for _, s := range got {
		if s.PreCheck {
			t.Fatalf("probe must not precheck: %+v", s)
		}
	}
}

func TestLooksLikeCopyName(t *testing.T) {
	if !looksLikeCopyName("官网 副本") || !looksLikeCopyName("report (1).pdf") {
		t.Fatal("expected copy names")
	}
	if looksLikeCopyName("正常名字.md") {
		t.Fatal("false positive")
	}
}
