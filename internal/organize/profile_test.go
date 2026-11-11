package organize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestCustomTemplateRulesAndManualPriority(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Desktop", "Client-photo.PNG")
	write(t, src, "image")
	profile := &worktype.Profile{Name: "客户交付", Folders: []string{"客户/审阅", "素材", "自己决定"}, Roles: map[string]string{"reference": "素材"}, Rules: []worktype.Rule{{Kind: "extension", Value: "png,jpg", Folder: "客户/审阅"}, {Kind: "name", Value: "client", Folder: "素材"}}}
	req := Request{Project: "品牌", Sources: []string{src}, Profile: profile}
	p, err := Build(cfg, h, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || !p.Rows[0].Selected || p.Rows[0].Folder != "客户/审阅" || !strings.Contains(p.Rows[0].Reason, "规则 1") {
		t.Fatalf("%+v", p.Rows)
	}
	req.Folders = map[string]string{src: "自己决定"}
	p, err = Build(cfg, h, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rows[0].Folder != "自己决定" {
		t.Fatal(p.Rows)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Moved != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if b, err := os.ReadFile(filepath.Join(cfg.Root, "品牌", "自己决定", filepath.Base(src))); err != nil || string(b) != "image" {
		t.Fatalf("%q %v", b, err)
	}
}
func TestCustomRulesCannotEscapeOrSplitProtectedProject(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "source")
	write(t, filepath.Join(src, ".git", "HEAD"), "ref")
	write(t, filepath.Join(src, "main.go"), "source")
	p := &worktype.Profile{Folders: []string{"源码"}, Rules: []worktype.Rule{{Kind: "path", Value: "source", Folder: "源码"}}}
	plan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{src}, Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rows) != 1 || !plan.Rows[0].Blocked {
		t.Fatal(plan.Rows)
	}
	hidden := filepath.Join(h, ".codex")
	write(t, filepath.Join(hidden, "config.toml"), "preferences")
	hiddenPlan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{hidden}, Profile: p})
	if err != nil || len(hiddenPlan.Rows) != 1 || !hiddenPlan.Rows[0].Blocked {
		t.Fatalf("hidden metadata was reorganized: %+v %v", hiddenPlan, err)
	}
	for _, bad := range []string{"../escape", "/tmp/escape", ".codex", "a/../b", "_总控", "a//b"} {
		p.Folders = []string{bad}
		if _, err = Build(cfg, h, Request{Project: "项目", Sources: []string{src}, Profile: p}); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestDirectoryRulesStillPlaceDescendantsAndBatchCorrectionsApply(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Downloads", "project")
	a, b := filepath.Join(src, "素材", "one.png"), filepath.Join(src, "素材", "two.png")
	write(t, a, "one")
	write(t, b, "two")
	profile := &worktype.Profile{Folders: []string{"审阅", "资料"}, Rules: []worktype.Rule{{Kind: "extension", Value: "png", Folder: "审阅"}}}
	req := Request{Project: "项目", Sources: []string{src}, Profile: profile}
	p, err := Build(cfg, h, req)
	if err != nil || len(selected(p)) != 2 {
		t.Fatalf("custom rule ignored inside a folder: %+v %v", p, err)
	}
	profile.Rules = nil
	req.Sources = []string{a, b}
	p, err = Build(cfg, h, req)
	if err != nil || len(selected(p)) != 0 || p.Rows[0].Reason != p.Rows[1].Reason {
		t.Fatalf("unmapped custom template should be a consistent group: %+v %v", p, err)
	}
	req.Folders = map[string]string{a: "资料", b: "资料"}
	p, err = Build(cfg, h, req)
	if err != nil || len(selected(p)) != 2 {
		t.Fatalf("batch correction lost: %+v %v", p, err)
	}
}
