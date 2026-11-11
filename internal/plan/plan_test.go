package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

func sample(home string, now time.Time) *scan.Result {
	old := now.AddDate(0, -2, 0)
	return &scan.Result{
		GeneratedAt: now,
		Groups: []scan.Group{
			{ID: 0, Name: "site", Members: []string{filepath.Join(home, "P", "site"), filepath.Join(home, "wt", "site-feature"), filepath.Join(home, "P", "site 副本")}},
			{ID: 1, Name: "海报", Members: []string{filepath.Join(home, "Desktop", "海报")}},
		},
		Projects: []scan.Project{
			{Path: filepath.Join(home, "P", "site"), Name: "site", Category: "code", Markers: []string{".git"}, ModTime: old, Group: 0, Kind: "main",
				Git: &scan.GitInfo{StatusChecked: true, Dirty: true, DirtyCount: 2, Worktrees: []string{filepath.Join(home, "wt", "site-feature")}}},
			{Path: filepath.Join(home, "wt", "site-feature"), Name: "site-feature", Category: "code", Markers: []string{".git"}, ModTime: old, Group: 0, Kind: "worktree",
				Git: &scan.GitInfo{IsWorktree: true, StatusChecked: true}},
			{Path: filepath.Join(home, "P", "site 副本"), Name: "site 副本", Category: "code", Markers: []string{"package.json"}, ModTime: old, Group: 0, Kind: "copy"},
			{Path: filepath.Join(home, "Desktop", "海报"), Name: "海报", Category: "images", Markers: []string{"12 张图"}, ModTime: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Group: 1, Kind: "single"},
		},
		Loose: []scan.LooseFile{
			{Path: filepath.Join(home, "Downloads", "site-r3.zip"), Category: "压缩包", ModTime: old, Group: 0},
			{Path: filepath.Join(home, "Downloads", "随手记.md"), Category: "文稿", ModTime: old, Group: -1},
		},
		SensitiveSkipped: 2,
	}
}

func TestBuild(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	p := Build(sample(home, now), Options{Config: cfg, Now: now, RootDevice: 1})

	if p.Counts.Projects != 2 || p.Counts.Pending != 1 || p.Counts.Dirty != 1 || p.Counts.Worktrees != 1 {
		t.Fatalf("counts: %+v", p.Counts)
	}
	items := map[string]Item{}
	for _, pp := range p.Projects {
		for _, it := range pp.Items {
			items[filepath.Base(it.Source)] = it
		}
	}
	if it := items["site"]; it.Action != ActionKeep || !strings.Contains(strings.Join(it.Risks, ","), "未提交") {
		t.Fatalf("dirty main must be keep: %+v", it)
	}
	if it := items["site-feature"]; it.Action != ActionKeep {
		t.Fatalf("worktree must be keep: %+v", it)
	}
	if it := items["site 副本"]; it.Action != ActionMove || it.Target != filepath.Join(cfg.Root, "site", "代码", "site 副本") {
		t.Fatalf("copy: %+v", it)
	}
	if it := items["海报"]; it.Target != filepath.Join(cfg.Root, "海报", "任务", "2026-09-01-海报") {
		t.Fatalf("images: %+v", it)
	}
	if it := items["site-r3.zip"]; it.Kind != "交付包" || it.Target != filepath.Join(cfg.Root, "site", "资料", "散落文件", "site-r3.zip") {
		t.Fatalf("zip: %+v", it)
	}
	if p.Pending[0].Action != ActionPending {
		t.Fatal("pending action")
	}
	// 计划不碰文件系统：~/Work 不会被建出来。
	if _, err := os.Stat(cfg.Root); !os.IsNotExist(err) {
		t.Fatal("Build must not create anything")
	}
}

func TestTargetExistsAndCrossVolume(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	if err := os.MkdirAll(filepath.Join(cfg.Root, "site", "代码", "site 副本"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := sample(home, now)
	res.Projects[2].Device = 7
	p := Build(res, Options{Config: cfg, Now: now, RootDevice: 1})
	for _, it := range p.Projects[0].Items {
		if filepath.Base(it.Source) == "site 副本" {
			r := strings.Join(it.Risks, ",")
			if it.Action != ActionKeep || !strings.Contains(r, "目标已存在") || !it.CrossVolume {
				t.Fatalf("%+v", it)
			}
			return
		}
	}
	t.Fatal("not found")
}

func TestWriteFilesNoOverwrite(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	p := Build(sample(home, now), Options{Config: cfg, Now: now, RootDevice: 1})
	out := filepath.Join(home, "out")
	j1, m1, sha, err := p.WriteFiles(out, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	j2, _, _, err := p.WriteFiles(out, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if j1 == j2 || !strings.HasSuffix(j2, "-02.json") {
		t.Fatalf("second write must get -02: %s %s", j1, j2)
	}
	var back Plan
	b, _ := os.ReadFile(j1)
	if err := json.Unmarshal(b, &back); err != nil || back.Counts.Projects != 2 {
		t.Fatalf("json: %v", err)
	}
	md, _ := os.ReadFile(m1)
	if !strings.Contains(string(md), sha) || !strings.Contains(string(md), "这只是计划") {
		t.Fatal("markdown")
	}
}
