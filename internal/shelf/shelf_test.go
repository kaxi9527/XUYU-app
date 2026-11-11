package shelf

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

func w(t *testing.T, p, s string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fixture(t *testing.T) (Env, map[string]string) {
	home := t.TempDir()
	cfg := config.Default(home)
	root := cfg.Root
	p := map[string]string{}
	w(t, filepath.Join(root, "项目A", "00-说明.md"), "# 项目A\n- 工种：设计·生图出图、电商·上架\n")
	task := filepath.Join(root, "项目A", "任务", "2026-11-05-出图")
	w(t, filepath.Join(task, "说明.md"), "- 工种：设计·生图出图\n- 模板：T01\n- 负责智能体：grok\n")
	p["taskfile"] = w(t, filepath.Join(task, "全部出图", "a.png"), "a")
	p["output"] = w(t, filepath.Join(root, "项目A", "成品", "2026-11-05-出图", "final.png"), "final")
	p["desk"] = w(t, filepath.Join(home, "Desktop", "随手.md"), "x")
	p["self"] = w(t, filepath.Join(home, "Downloads", "报告.md"), "v1")
	p["side"] = w(t, filepath.Join(home, "Documents", "资料", "清单.csv"), "c")
	p["top"] = w(t, filepath.Join(root, "散.txt"), "t")
	w(t, filepath.Join(home, ".ssh", "id_rsa"), "secret")
	w(t, filepath.Join(home, "Documents", "资料", activity.SidecarName), `{"file":"清单.csv","agent":"codex","worktype":"电商·选品"}`+"\n")
	now := time.Now()
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{p["self"]}, Agent: "claude", WorkType: "通用·文稿", Now: now}); err != nil {
		t.Fatal(err)
	}
	w(t, p["self"], "v2 改过") // 登记后又被改
	_, err := put.AppendRows(filepath.Join(root, "_总控", "资产索引分片", "资产索引.grok.csv"), []put.Row{{Time: now.Format(time.RFC3339), Project: "项目A", Task: "2026-11-05-出图", Path: p["output"], Agent: "grok"}})
	if err != nil {
		t.Fatal(err)
	}
	// 用活动记录把成品标成待批准
	activity.Append(activity.Dir(cfg), cfg.Location(), activity.Record{Time: now.Format(time.RFC3339), Path: p["output"], Agent: "grok", Source: activity.SourcePut, Status: "待批准", WorkType: "设计·生图出图"})
	return Env{Config: cfg, Home: home}, p
}

func TestRecent(t *testing.T) {
	e, p := fixture(t)
	items := Recent(e, 3, 100)
	by := map[string]Item{}
	for _, it := range items {
		by[it.Path] = it
	}
	if _, ok := by[filepath.Join(e.Home, ".ssh", "id_rsa")]; ok {
		t.Fatal("点目录/密钥不该出现")
	}
	cases := []struct {
		key, agent, conf, place string
		mod                     bool
	}{
		{"taskfile", "grok", ConfInferred, PlacedOK, false},
		{"output", "grok", ConfPut, PlacedPending, false},
		{"desk", "", ConfUnknown, PlacedWrong, false},
		{"self", "claude", ConfSelf, PlacedWrong, true},
		{"side", "codex", ConfSidecar, PlacedOutside, false},
		{"top", "", ConfUnknown, PlacedWrong, false},
	}
	for _, c := range cases {
		it, ok := by[p[c.key]]
		if !ok {
			t.Fatalf("%s 没出现", c.key)
		}
		if it.Agent != c.agent || it.Confidence != c.conf || it.Placement != c.place || it.Modified != c.mod {
			t.Errorf("%s: %+v", c.key, it)
		}
	}
	if by[p["taskfile"]].WorkType != "设计·生图出图" || by[p["taskfile"]].Project != "项目A" || by[p["taskfile"]].Task != "2026-11-05-出图" {
		t.Errorf("%+v", by[p["taskfile"]])
	}
}

func TestSearch(t *testing.T) {
	e, p := fixture(t)
	if r := Search(e, "codex", 10); len(r) != 1 || r[0].Path != p["side"] {
		t.Fatalf("按智能体搜：%+v", r)
	}
	if r := Search(e, "项目a final", 10); len(r) != 1 || r[0].Path != p["output"] {
		t.Fatalf("多词：%+v", r)
	}
	if r := Search(e, "  ", 10); r != nil {
		t.Fatal("空查询")
	}
}

func TestProjects(t *testing.T) {
	e, _ := fixture(t)
	ps := Projects(e)
	if len(ps) != 1 || ps[0].Name != "项目A" || ps[0].Tasks != 1 || ps[0].Outputs != 1 || ps[0].Pending != 1 || len(ps[0].WorkTypes) != 2 {
		t.Fatalf("%+v", ps)
	}
	d, err := Project(e, "项目A")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Columns) != 2 || d.Columns[0].WorkType != "设计·生图出图" || len(d.Columns[0].Tasks) != 1 {
		t.Fatalf("%+v", d.Columns)
	}
	tk := d.Columns[0].Tasks[0]
	if tk.Status != "待批准" || tk.Template != "T01" || tk.Files != 1 || len(tk.Outputs) != 1 || tk.Agent != "grok" {
		t.Fatalf("%+v", tk)
	}
	if d.Columns[1].WorkType != "电商·上架" || len(d.Columns[1].Tasks) != 0 {
		t.Fatalf("空栏：%+v", d.Columns[1])
	}
	if _, err := Project(e, "_总控"); err == nil {
		t.Fatal("总控不是项目")
	}
	if _, err := Project(e, "../x"); err == nil {
		t.Fatal("路径穿越")
	}
}

func TestLinkedProjectIsVisibleAndCanBeRead(t *testing.T) {
	e, _ := fixture(t)
	target := filepath.Join(e.Home, "Documents", "链接目标")
	w(t, filepath.Join(target, "资料.md"), "project content")
	link := filepath.Join(e.Config.Root, "链接项目")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	projects := Projects(e)
	var found *ProjectSummary
	for i := range projects {
		if projects[i].Name == "链接项目" {
			found = &projects[i]
		}
	}
	if found == nil || !found.Linked || found.LinkTarget != resolvedTarget || found.Path != link {
		t.Fatalf("linked project not visible: %+v", projects)
	}
	detail, err := Project(e, "链接项目")
	if err != nil || !detail.Linked || detail.LinkTarget != resolvedTarget {
		t.Fatalf("linked project detail: %+v %v", detail, err)
	}
	if len(detail.Loose) != 1 || detail.Loose[0].Name != "资料.md" {
		t.Fatalf("linked project content unavailable: %+v", detail.Loose)
	}
}
