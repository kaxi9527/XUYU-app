package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/organize"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestBrowserHiddenToggleAndPathBoundary(t *testing.T) {
	h := t.TempDir()
	os.Mkdir(filepath.Join(h, ".codex"), 0755)
	os.WriteFile(filepath.Join(h, "visible.txt"), []byte("x"), 0644)
	for _, hidden := range []bool{false, true} {
		r, err := browseDirectory(h, h, hidden)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if hidden {
			want = 2
		}
		if len(r.Entries) != want || r.HiddenCount != 1 {
			t.Fatalf("hidden=%v %+v", hidden, r)
		}
	}
	if _, err := browseDirectory(h, filepath.Dir(h), true); err == nil {
		t.Fatal("escape allowed")
	}
	os.Symlink(filepath.Dir(h), filepath.Join(h, "link"))
	if _, err := browseDirectory(h, filepath.Join(h, "link"), true); err == nil {
		t.Fatal("symlink allowed")
	}
	empty := filepath.Join(h, "empty")
	os.Mkdir(empty, 0755)
	if _, err := browseDirectory(h, empty, false); err != nil {
		t.Fatal(err)
	}
}
func TestHiddenDeleteRequiresSeparateConfirmation(t *testing.T) {
	s := ShelfService{}
	if _, err := s.ApplyDelete([]string{"/Users/demo/.codex"}); err == nil {
		t.Fatal("hidden delete accepted without confirmation")
	}
	if !hiddenPaths([]string{"/Users/demo/.codex/log.txt"}) || hiddenPaths([]string{"/Users/demo/report.txt"}) {
		t.Fatal("hidden detection")
	}
}
func TestSavedProfileReadbackAndNoOverwrite(t *testing.T) {
	h := t.TempDir()
	path := filepath.Join(h, "profiles", "设计.json")
	p := &worktype.Profile{Name: "设计", Folders: []string{"交付/客户"}, Rules: []worktype.Rule{{Kind: "name", Value: "定稿", Folder: "交付/客户"}}}
	if err := writeProfile(h, path, p, true); err != nil {
		t.Fatal(err)
	}
	got, err := worktype.ReadProfile(path)
	if err != nil || got.Rules[0].Folder != "交付/客户" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := writeProfile(h, path, p, true); !os.IsExist(err) {
		t.Fatal("existing template overwritten", err)
	}
	if err := writeProfile(h, filepath.Join(h, "..", "escape.json"), p, true); err == nil {
		t.Fatal("escaped profile save")
	}
}

func TestCustomProjectLayoutAppearsInProject(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	src := filepath.Join(h, "客户定稿.txt")
	os.WriteFile(src, []byte("approved"), 0644)
	profile := &worktype.Profile{Name: "我的模板", Folders: []string{"客户/交付"}, Rules: []worktype.Rule{{Kind: "name", Value: "定稿", Folder: "客户/交付"}}}
	p, err := organize.Build(cfg, h, organize.Request{Project: "自定义项目", Sources: []string{src}, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Apply(cfg, []string{p.Rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err = recordLayout(cfg, p); err != nil {
		t.Fatal(err)
	}
	d, err := shelf.Project(shelf.Env{Config: cfg, Home: h}, p.Project)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Folders) != 1 || d.Folders[0].Name != "客户/交付" || len(d.Folders[0].Entries) != 1 {
		t.Fatalf("%+v", d.Folders)
	}
}
