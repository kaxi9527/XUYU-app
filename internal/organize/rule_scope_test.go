package organize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func scopedProfile() *worktype.Profile {
	return &worktype.Profile{Name: "设计", Folders: []string{"需求", "参考资料", "工作文件", "交付"},
		Roles: map[string]string{"brief": "需求", "reference": "参考资料", "source": "工作文件", "draft": "工作文件", "delivery": "交付"}}
}

func TestUnmatchedRulePreservesWholeFolders(t *testing.T) {
	for _, merge := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "merge"}[merge], func(t *testing.T) {
			cfg, h := fixture(t)
			src := filepath.Join(h, "Desktop", "项目")
			write(t, filepath.Join(src, "素材", "a.png"), "a")
			write(t, filepath.Join(src, "素材", "细节", "b.txt"), "b")
			profile := scopedProfile()
			req := Request{Project: "项目", Sources: []string{src}, Profile: profile, Merge: merge}
			before, err := Build(cfg, h, req)
			if err != nil {
				t.Fatal(err)
			}
			profile.Rules = []worktype.Rule{{Kind: "extension", Value: "notpresent", Folder: "交付"}}
			after, err := Build(cfg, h, req)
			if err != nil {
				t.Fatal(err)
			}
			shape := func(p *Plan) map[string]string {
				m := map[string]string{}
				for _, row := range p.Rows {
					m[row.Source] = row.Target
				}
				return m
			}
			if !reflect.DeepEqual(shape(before), shape(after)) {
				t.Fatalf("unmatched rule changed placement: before=%v after=%v", shape(before), shape(after))
			}
		})
	}
}

func TestPartialRuleAndCorrectionKeepUnmatchedRelativePaths(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "rule", true: "manual"}[manual], func(t *testing.T) {
			cfg, h := fixture(t)
			src := filepath.Join(h, "Desktop", "项目")
			content := map[string]string{
				filepath.Join(src, "素材", "pick.png"):           "pick",
				filepath.Join(src, "素材", "keep.txt"):           "keep",
				filepath.Join(src, "素材", "nested", "deep.svg"): "deep",
			}
			for path, data := range content {
				write(t, path, data)
			}
			profile := scopedProfile()
			req := Request{Project: "项目", Sources: []string{src}, Profile: profile, Merge: true}
			picked := filepath.Join(src, "素材", "pick.png")
			if manual {
				req.Folders = map[string]string{picked: "交付"}
			} else {
				profile.Rules = []worktype.Rule{{Kind: "name", Value: "pick", Folder: "交付"}}
			}
			plan, err := Build(cfg, h, req)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				picked:                               filepath.Join(cfg.Root, "项目", "交付", "pick.png"),
				filepath.Join(src, "素材", "keep.txt"): filepath.Join(cfg.Root, "项目", "参考资料", "素材", "keep.txt"),
				filepath.Join(src, "素材", "nested", "deep.svg"): filepath.Join(cfg.Root, "项目", "参考资料", "素材", "nested", "deep.svg"),
			}
			for _, row := range plan.Rows {
				if target, ok := want[row.Source]; ok {
					if row.Target != target || !row.Selected {
						t.Errorf("unexpected placement: %+v want %s", row, target)
					}
					delete(want, row.Source)
				}
			}
			if len(want) != 0 {
				t.Fatalf("missing rows: %v", want)
			}
			if t.Failed() {
				return
			}
			result, err := plan.Apply(cfg, selected(plan))
			if err != nil || result.Failed != 0 || result.Moved != 3 {
				t.Fatalf("%+v %v", result, err)
			}
			undone, err := home.Undo(cfg, result.UndoPath)
			if err != nil || undone.Restored != 3 || undone.Failed != 0 {
				t.Fatalf("%+v %v", undone, err)
			}
			for path, data := range content {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != data {
					t.Fatalf("restore %s: %q %v", path, got, err)
				}
			}
		})
	}
}

func TestRuleDoesNotMigrateExistingProjectDirectories(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(cfg.Root, "项目")
	old := filepath.Join(src, "旧归档", "nested", "keep.png")
	write(t, old, "existing")
	profile := scopedProfile()
	profile.Rules = []worktype.Rule{{Kind: "extension", Value: "png", Folder: "交付"}}
	plan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{src}, Profile: profile, Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected(plan)) != 0 {
		t.Fatalf("rule selected previously archived content: %+v", plan.Rows)
	}
	found := false
	for _, row := range plan.Rows {
		if row.Source == filepath.Join(src, "旧归档") && row.Existing {
			found = true
		}
	}
	if !found {
		t.Fatalf("existing directory was expanded: %+v", plan.Rows)
	}
}

func TestRuleCanPlaceWholeNestedBundleWithoutSplitting(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Desktop", "项目")
	bundle := filepath.Join(src, "projects", "frontend")
	write(t, filepath.Join(bundle, "package.json"), "{}")
	write(t, filepath.Join(bundle, "src", "index.ts"), "export {}")
	write(t, filepath.Join(src, "projects", "keep.txt"), "keep")
	profile := scopedProfile()
	profile.Rules = []worktype.Rule{{Kind: "name", Value: "frontend", Folder: "交付"}}
	plan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{src}, Profile: profile, Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range plan.Rows {
		if row.Source == bundle {
			found = true
			if !row.Directory || row.Target != filepath.Join(cfg.Root, "项目", "交付", "frontend") {
				t.Fatal(row)
			}
		}
		if row.Source != bundle && in(row.Source, bundle) {
			t.Fatalf("split project: %+v", row)
		}
	}
	if !found {
		t.Fatalf("whole project rule ignored: %+v", plan.Rows)
	}
}

func TestRuleInspectionReportsIncompleteRootWithoutGuessing(t *testing.T) {
	cfg, h := fixture(t)
	safe := filepath.Join(h, "a.txt")
	huge := filepath.Join(h, "many-files")
	write(t, safe, "safe")
	for i := 0; i < 5001; i++ {
		write(t, filepath.Join(huge, fmt.Sprintf("%04d.txt", i)), "x")
	}
	profile := scopedProfile()
	profile.Rules = []worktype.Rule{{Kind: "extension", Value: "txt", Folder: "交付"}}
	plan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{safe, huge}, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) == 0 || len(selected(plan)) != 1 {
		t.Fatalf("coverage was not disclosed: %+v", plan)
	}
	for _, row := range plan.Rows {
		if in(row.Source, huge) && (!row.Blocked || row.Target != "") {
			t.Fatalf("incomplete rule check created an action: %+v", row)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "项目")); !os.IsNotExist(err) {
		t.Fatal("preview created destination", err)
	}
}

func TestRuleInspectionRejectsDirectoryDriftAndCancellation(t *testing.T) {
	_, h := fixture(t)
	src := filepath.Join(h, "files")
	write(t, filepath.Join(src, "a.txt"), "a")
	profile := scopedProfile()
	profile.Rules = []worktype.Rule{{Kind: "extension", Value: "png", Folder: "交付"}}
	req := Request{Profile: profile}
	scope, err := inspectPlacementScope(context.Background(), []string{src}, req, filepath.Join(h, "target"), "说明.md", time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "now-matches.png"), "new")
	if err := os.Chtimes(src, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if scope.verify(context.Background()) == nil {
		t.Fatal("accepted stale rule coverage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inspectPlacementScope(ctx, []string{src}, req, filepath.Join(h, "target"), "说明.md", time.Now().Add(time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPartialRuleLeavesUnknownRemainderUnselected(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Desktop", "project")
	pick, keep := filepath.Join(src, "misc", "pick.png"), filepath.Join(src, "misc", "keep.xyz")
	write(t, pick, "pick")
	write(t, keep, "keep")
	profile := &worktype.Profile{Folders: []string{"审阅", "待用"}, Rules: []worktype.Rule{{Kind: "extension", Value: "png", Folder: "审阅"}}}
	plan, err := Build(cfg, h, Request{Project: "项目", Sources: []string{src}, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range plan.Rows {
		if row.Source == pick && (!row.Selected || row.Folder != "审阅") {
			t.Fatal(row)
		}
		if row.Source == keep && (row.Selected || row.Target != "") {
			t.Fatalf("guessed a destination for unmatched content: %+v", row)
		}
	}
	if len(selected(plan)) != 1 {
		t.Fatal(plan.Rows)
	}
}
