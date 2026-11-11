package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestSourceSuggestionUsesSelectedFilesOnly(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	fig := filepath.Join(h, "首页.fig")
	image := filepath.Join(h, "图片.png")
	for _, path := range []string{fig, image, filepath.Join(h, "go.mod")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := suggestSources(cfg, h, []string{fig})
	if err != nil || got.Project != "首页" || len(got.Inferred) != 1 || got.Inferred[0].Label != "设计·UI原型" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = suggestSources(cfg, h, []string{image})
	if err != nil || len(got.Inferred) != 0 {
		t.Fatalf("unrelated sibling evidence leaked: %+v %v", got, err)
	}
}

func TestSourceSuggestionPreservesPathBoundary(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	outside := t.TempDir()
	if _, err := suggestSources(cfg, h, []string{outside}); err == nil {
		t.Fatal("outside source accepted")
	}
	link := filepath.Join(h, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := suggestSources(cfg, h, []string{link}); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := suggestSources(cfg, h, make([]string, 65)); err == nil {
		t.Fatal("unbounded source count")
	}
	if _, err := suggestSources(cfg, h, []string{"relative"}); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestHiddenSourceDoesNotBecomeAutomaticWorkType(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	p := filepath.Join(h, ".codex")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "go.mod"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := suggestSources(cfg, h, []string{p})
	if err != nil || len(got.Inferred) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
