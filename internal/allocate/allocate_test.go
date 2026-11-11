package allocate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInferFromReadme(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "00-说明.md"), "# 海报\n- 工种：电商·主图、设计·生图出图\n")
	got := InferProject(InferOptions{ProjectDir: dir})
	labs := map[string]Inferred{}
	for _, in := range got {
		labs[in.Label] = in
	}
	if labs["电商·主图"].Source != SrcReadme || !labs["电商·主图"].AlreadyInReadme {
		t.Fatalf("readme 工种丢失：%+v", got)
	}
	if labs["设计·生图出图"].Confidence != ConfHigh {
		t.Fatalf("说明里的应较有把握：%+v", labs["设计·生图出图"])
	}
}

func TestInferCodeMarkers(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	write(t, filepath.Join(dir, "main.go"), "package main\n")
	got := InferProject(InferOptions{ProjectDir: dir})
	found := false
	for _, in := range got {
		if in.Label == "开发·功能开发" && in.Source == SrcInfer && in.Confidence == ConfHigh {
			found = true
		}
	}
	if !found {
		t.Fatalf("应推断出开发·功能开发：%+v", got)
	}
}

func TestInferByFolderName(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "剪辑成片素材")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sub, "a.mp4"), "x")
	write(t, filepath.Join(sub, "b.mp4"), "y")
	write(t, filepath.Join(sub, "c.mp4"), "z")
	got := InferProject(InferOptions{ProjectDir: dir})
	ok := false
	for _, in := range got {
		if strings.Contains(in.Label, "剪辑") && in.Source == SrcInfer {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("路径含剪辑应推断相关工种：%+v", got)
	}
}

func TestClassifyImageToDesignTask(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "草稿草图.png")
	write(t, src, "img")
	s := ClassifyFile(ClassifyOptions{
		Path: src, Project: "海报", ProjectDir: dir,
		WorkTypes: []string{"设计·生图出图"}, Names: config.ZhNames(),
	})
	if s.Skip || (s.Area != home.AreaTasks && s.Area != home.AreaOutputs) || s.WorkType != "设计·生图出图" || !s.PreCheck {
		t.Fatalf("图片应进设计任务/成品：%+v", s)
	}
	if s.Task == "" {
		t.Fatalf("应有任务子目录名：%+v", s)
	}
}

func TestClassifyCodeToCode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	write(t, src, "package main\n")
	s := ClassifyFile(ClassifyOptions{
		Path: src, Project: "工具", ProjectDir: dir,
		WorkTypes: []string{"开发·功能开发"}, Names: config.ZhNames(),
	})
	if s.Area != home.AreaCode || s.Confidence != ConfHigh {
		t.Fatalf("代码应进代码栏：%+v", s)
	}
}

func TestClassifyUncertainToLoose(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "weird.xyz")
	write(t, src, "z")
	s := ClassifyFile(ClassifyOptions{
		Path: src, Project: "杂项", ProjectDir: dir,
		WorkTypes: nil, Names: config.ZhNames(),
	})
	if s.Area != home.AreaLoose || s.Confidence != ConfLow || s.PreCheck {
		t.Fatalf("拿不准应散落且待你定：%+v", s)
	}
}

func TestClassifySkipSensitiveName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".env")
	write(t, src, "SECRET=1\n")
	s := ClassifyFile(ClassifyOptions{
		Path: src, Project: "P", ProjectDir: dir, Names: config.ZhNames(),
	})
	if !s.Skip {
		t.Fatalf("密钥文件名应跳过：%+v", s)
	}
}

func TestClassifySkipAlreadyInTasks(t *testing.T) {
	n := config.ZhNames()
	dir := t.TempDir()
	src := filepath.Join(dir, n.Tasks, "2026-11-05-x", "a.png")
	write(t, src, "img")
	s := ClassifyFile(ClassifyOptions{
		Path: src, Project: "P", ProjectDir: dir,
		WorkTypes: []string{"设计·生图出图"}, Names: n,
	})
	if !s.Skip {
		t.Fatalf("已在任务里应跳过：%+v", s)
	}
}

func TestSuggestProjectFromLoose(t *testing.T) {
	n := config.ZhNames()
	root := t.TempDir()
	proj := filepath.Join(root, "海报项目")
	write(t, filepath.Join(proj, n.Readme), "# 海报\n- 工种：电商·主图\n")
	for _, d := range []string{n.Tasks, n.Outputs, n.Materials, n.Code} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loose := filepath.Join(proj, n.Materials, n.Loose)
	write(t, filepath.Join(loose, "主图-A.png"), "img")
	write(t, filepath.Join(loose, "notes.md"), "doc")
	write(t, filepath.Join(proj, "main.go"), "package main\n")

	cfg := config.Default(root)
	cfg.Root = root
	ss, err := SuggestProject(SuggestOptions{Config: cfg, Project: "海报项目", ProjectDir: proj})
	if err != nil {
		t.Fatal(err)
	}
	byBase := map[string]Suggestion{}
	for _, s := range ss {
		byBase[filepath.Base(s.Source)] = s
	}
	img := byBase["主图-A.png"]
	if img.Area != home.AreaOutputs && img.Area != home.AreaTasks {
		t.Fatalf("主图应进成品或任务：%+v", img)
	}
	if img.WorkType != "电商·主图" {
		t.Fatalf("应挂电商·主图：%+v", img)
	}
	code := byBase["main.go"]
	if code.Area != home.AreaCode {
		t.Fatalf("main.go 应进代码：%+v", code)
	}
	jobs := ToJobs(ss)
	if len(jobs) == 0 {
		t.Fatal("ToJobs 不应为空")
	}
}

func TestJobsForItemsPrefersAllocate(t *testing.T) {
	n := config.ZhNames()
	root := t.TempDir()
	proj := filepath.Join(root, "工具")
	write(t, filepath.Join(proj, n.Readme), "# 工具\n- 工种：开发·功能开发\n")
	_ = os.MkdirAll(filepath.Join(proj, n.Code), 0o755)
	src := filepath.Join(t.TempDir(), "util.go")
	write(t, src, "package util\n")
	cfg := config.Default(root)
	cfg.Root = root
	jobs, skipped := JobsForItems(cfg, []shelf.Item{{
		Path: src, Name: "util.go", Project: "工具", Placement: shelf.PlacedWrong,
	}})
	if len(skipped) != 0 {
		t.Fatalf("skipped=%v", skipped)
	}
	if len(jobs) != 1 || jobs[0].Area != home.AreaCode {
		t.Fatalf("一键归位应按工种进代码，不要散落：%+v", jobs)
	}
}
