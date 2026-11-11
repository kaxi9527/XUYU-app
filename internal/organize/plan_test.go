package organize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (config.Config, string) {
	t.Helper()
	h := t.TempDir()
	cfg := config.Default(h)
	return cfg, h
}
func selected(p *Plan) []string {
	var ids []string
	for _, r := range p.Rows {
		if r.Selected && !r.Blocked {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func TestFinalNamedRuntimeLogStaysUntilExplicitlyChosen(t *testing.T) {
	cfg, h := fixture(t)
	file := filepath.Join(h, "Desktop", "desktop-test-final.log")
	write(t, file, "test log")
	req := Request{Project: "日志检查", Sources: []string{file}, WorkTypes: []string{"开发·功能开发"}}
	p, err := Build(cfg, h, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Selected || !p.Rows[0].KeepOriginal || p.Rows[0].Folder == "交付说明" {
		t.Fatalf("log promoted to delivery: %+v", p.Rows)
	}
	req.Folders = map[string]string{file: "设计与测试"}
	p, err = Build(cfg, h, req)
	if err != nil || !p.Rows[0].Selected || p.Rows[0].KeepOriginal {
		t.Fatalf("explicit placement lost: %+v %v", p, err)
	}
}

func TestDesignGatherApplyAndUndo(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "品牌改版"), filepath.Join(h, "Downloads", "品牌改版交付")
	inputs := map[string]string{filepath.Join(a, "需求说明.md"): "brief", filepath.Join(a, "素材", "参考图.png"): "reference", filepath.Join(a, "设计源文件.psd"): "editable", filepath.Join(a, "草稿.png"): "draft", filepath.Join(b, "品牌改版-final.png"): "final"}
	for path, content := range inputs {
		write(t, path, content)
	}
	p, e := Build(cfg, h, Request{Project: "品牌改版", Sources: []string{a, b}, WorkTypes: []string{"设计·UI原型"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Rows) != 5 || len(selected(p)) != 5 {
		t.Fatalf("rows=%+v", p.Rows)
	}
	for _, r := range p.Rows {
		if _, e := os.Stat(r.Source); e != nil {
			t.Fatal("preview changed source", e)
		}
	}
	sum, e := p.Apply(cfg, selected(p))
	if e != nil || sum.Moved != 5 || sum.Failed != 0 {
		t.Fatalf("sum=%+v err=%v", sum, e)
	}
	want := []string{"需求/需求说明.md", "参考素材/素材/参考图.png", "源文件/设计源文件.psd", "过程稿/草稿.png", "交付稿/品牌改版-final.png"}
	for _, rel := range want {
		if _, e := os.Stat(filepath.Join(p.Target, rel)); e != nil {
			t.Fatal(rel, e)
		}
	}
	u, e := home.Undo(cfg, sum.UndoPath)
	if e != nil || u.Restored != 5 || u.Failed != 0 {
		t.Fatalf("undo=%+v %v", u, e)
	}
	for path, content := range inputs {
		b, e := os.ReadFile(path)
		if e != nil || string(b) != content {
			t.Fatal("restore mismatch", path, e)
		}
	}
}
func TestCodeAndVideoBundlesStayIntact(t *testing.T) {
	for _, tc := range []struct{ name, wt, marker, target string }{{"开发", "开发·功能开发", "go.mod", "代码工程"}, {"剪辑", "视频剪辑·剪辑成片", "project.prproj", "剪辑工程"}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, h := fixture(t)
			src := filepath.Join(h, "Downloads", "工程")
			write(t, filepath.Join(src, tc.marker), "marker")
			write(t, filepath.Join(src, "assets", "素材.png"), "asset")
			write(t, filepath.Join(src, "src", "main.go"), "package main")
			p, e := Build(cfg, h, Request{Project: "完整项目", Sources: []string{src}, WorkTypes: []string{tc.wt}})
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Rows) != 1 || !p.Rows[0].Directory || p.Rows[0].Folder != tc.target {
				t.Fatalf("split bundle: %+v", p.Rows)
			}
			s, e := p.Apply(cfg, selected(p))
			if e != nil || s.Moved != 1 {
				t.Fatalf("%+v %v", s, e)
			}
			if b, e := os.ReadFile(filepath.Join(p.Rows[0].Target, "assets", "素材.png")); e != nil || string(b) != "asset" {
				t.Fatal("relative paths lost")
			}
		})
	}
}
func TestAutomaticPlacementKeepsProtectedProjectsInPlace(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Documents", "project")
	write(t, filepath.Join(src, "不明.bin"), "unknown")
	write(t, filepath.Join(src, "image.png"), "image")
	write(t, filepath.Join(src, "app", ".git", "HEAD"), "ref: refs/heads/main")
	write(t, filepath.Join(src, "app", "package.json"), "{}")
	p, e := Build(cfg, h, Request{Project: "项目", Sources: []string{src}, WorkTypes: []string{"设计·UI原型"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(selected(p)) != 2 {
		t.Fatal("ordinary files should receive automatic non-delivery destinations", p.Rows)
	}
	blocked := false
	for _, r := range p.Rows {
		if r.Directory && r.Blocked {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("Git tree not protected")
	}
	if _, e := p.Apply(cfg, []string{"forged"}); e == nil {
		t.Fatal("forged row accepted")
	}
}

func TestThousandsOfExistingFilesAreNotIndividualDecisions(t *testing.T) {
	cfg, h := fixture(t)
	project := filepath.Join(cfg.Root, "设计项目")
	existing := filepath.Join(project, "screenshots")
	for i := 0; i < 2400; i++ {
		write(t, filepath.Join(existing, fmt.Sprintf("screen-%04d.png", i)), "fixture")
	}
	write(t, filepath.Join(project, "requirements.md"), "brief")
	loose := filepath.Join(h, "Downloads", "设计项目-icon.png")
	write(t, loose, "icon")
	p, err := Build(cfg, h, Request{Project: "设计项目", Sources: []string{project, loose}, WorkTypes: []string{"设计·UI原型"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 3 || len(selected(p)) != 2 {
		t.Fatalf("expanded existing project: %+v", p.Rows)
	}
	var existingID string
	for _, row := range p.Rows {
		if row.Source == existing {
			if !row.Existing || row.Selected || !row.Directory || row.Target != existing {
				t.Fatal(row)
			}
			existingID = row.ID
		}
	}
	if existingID == "" {
		t.Fatal("existing folder absent from preview")
	}
	if _, err = p.Apply(cfg, []string{existingID}); err == nil {
		t.Fatal("existing folder accepted as a move")
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Moved != 2 {
		t.Fatalf("%+v %v", sum, err)
	}
	entries, err := os.ReadDir(existing)
	if err != nil || len(entries) != 2400 {
		t.Fatal("existing directory changed", err)
	}
	u, err := home.Undo(cfg, sum.UndoPath)
	if err != nil || u.Restored != 2 {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestGroupedFolderPreservesRelativePathsAndProtectsNestedRepository(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Downloads", "品牌改版")
	for i := 0; i < 120; i++ {
		write(t, filepath.Join(src, "素材", "images", fmt.Sprintf("img-%d.png", i)), "image")
	}
	write(t, filepath.Join(src, "工作", "app", ".git", "HEAD"), "ref")
	p, err := Build(cfg, h, Request{Project: "品牌改版", Sources: []string{src}, WorkTypes: []string{"设计·UI原型"}})
	if err != nil || len(p.Rows) != 2 || len(selected(p)) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Moved != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if _, err = os.Stat(filepath.Join(p.Target, "参考素材", "素材", "images", "img-119.png")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(src, "工作", "app", ".git", "HEAD")); err != nil {
		t.Fatal("nested repository moved", err)
	}
	u, err := home.Undo(cfg, sum.UndoPath)
	if err != nil || u.Restored != 1 {
		t.Fatalf("%+v %v", u, err)
	}
}
func TestCorrectionsConflictsAndStalePreview(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Desktop", "unknown.png")
	write(t, src, "before")
	write(t, filepath.Join(cfg.Root, "项目", "交付稿", "unknown.png"), "existing")
	req := Request{Project: "项目", Sources: []string{src}, WorkTypes: []string{"设计·UI原型"}, Folders: map[string]string{src: "交付稿"}}
	p, e := Build(cfg, h, req)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(p.Rows[0].Target, "unknown-02.png") {
		t.Fatal(p.Rows)
	}
	write(t, src, "after changed")
	if _, e := p.Apply(cfg, selected(p)); e == nil {
		t.Fatal("stale preview executed")
	}
	if b, _ := os.ReadFile(filepath.Join(cfg.Root, "项目", "交付稿", "unknown.png")); string(b) != "existing" {
		t.Fatal("overwrote collision")
	}
	req.Folders[src] = "../../escape"
	if _, e := Build(cfg, h, req); e == nil {
		t.Fatal("folder escape accepted")
	}
}
func TestNestedSourcesDeduplicatedAndSymlinksRejected(t *testing.T) {
	cfg, h := fixture(t)
	dir := filepath.Join(h, "Desktop", "project")
	file := filepath.Join(dir, "brief.txt")
	write(t, file, "brief")
	p, e := Build(cfg, h, Request{Project: "项目", Sources: []string{dir, file}, WorkTypes: []string{"设计·UI原型"}})
	if e != nil || len(p.Rows) != 1 {
		t.Fatalf("%+v %v", p, e)
	}
	link := filepath.Join(h, "link")
	if e := os.Symlink(dir, link); e != nil {
		t.Fatal(e)
	}
	if _, e := Build(cfg, h, Request{Project: "项目", Sources: []string{filepath.Join(link, "brief.txt")}, WorkTypes: []string{"设计·UI原型"}}); e == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
