package autoorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestAutomaticProjectCollectAndObjectionRestore(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(h, "Desktop", "需求说明.md"), filepath.Join(h, "Downloads", "首页.psd")}
	var records []activity.Record
	for i, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		records = append(records, activity.Record{Path: p, Project: "品牌项目", WorkType: "设计·UI原型", Agent: "codex", Time: time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)})
	}
	policy := Policy{Enabled: true, Roots: []string{h}}
	groups := Candidates(cfg, policy, records)
	if len(groups) != 1 || len(groups[0].Files) != 2 || groups[0].Reason != "" {
		t.Fatal(groups)
	}
	if _, err := Execute(context.Background(), cfg, h, Policy{Roots: []string{h}}, groups[0]); err == nil {
		t.Fatal("disabled policy executed")
	}
	o, err := Execute(context.Background(), cfg, h, policy, groups[0])
	if err != nil || o.Status != "organized" || o.Summary == nil || o.Summary.Moved != 2 {
		t.Fatal(o, err)
	}
	for _, p := range paths {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("source not moved", p, err)
		}
	}
	retry, err := Execute(context.Background(), cfg, h, policy, groups[0])
	if err != nil || retry.ID != o.ID || retry.Status != "organized" {
		t.Fatal("retry not idempotent", retry, err)
	}
	restored, err := Restore(cfg, o.ID)
	if err != nil || restored.Status != "restored" || restored.Restore.Restored != 2 {
		t.Fatal(restored, err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "artifact" {
			t.Fatal("source not restored", p, err)
		}
	}
	if _, err = os.Stat(o.Target); !os.IsNotExist(err) {
		t.Fatal("empty automatically created project was not removed", err, restored.Error)
	}
	again, err := Restore(cfg, o.ID)
	if err != nil || again.Status != "restored" {
		t.Fatal(again, err)
	}
}

func TestRestoredMarkedFileIsNotCollectedAgainWithNewFiles(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop := filepath.Join(h, "Desktop")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	oldFile, newFile := filepath.Join(desktop, "old.psd"), filepath.Join(desktop, "new.psd")
	baseTime := time.Now().Add(-time.Minute)
	mark := func(path, content string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{path}, Agent: "codex", Project: "品牌", WorkType: "设计·UI原型", Now: at}); err != nil {
			t.Fatal(err)
		}
	}
	mark(oldFile, "old", baseTime)
	p := Policy{Enabled: true, Roots: []string{h}}
	records, err := activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	first := Candidates(cfg, p, records)
	if len(first) != 1 {
		t.Fatal(first)
	}
	legacyCandidate := first[0]
	legacyCandidate.Signals = append([]FileSignal(nil), legacyCandidate.Signals...)
	legacyCandidate.Signals[0].ActivitySHA = ""
	if operationID(legacyCandidate) != operationID(first[0]) {
		t.Fatal("existing operation identity changed after adding mark provenance")
	}
	op, err := Execute(context.Background(), cfg, h, p, first[0])
	if err != nil || op.Status != "organized" {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	mark(newFile, "new", baseTime.Add(time.Second))
	records, err = activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0] != newFile {
		t.Fatalf("restored marked file was collected again: %+v", groups)
	}
	mark(oldFile, "updated", baseTime)
	records, err = activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	groups = Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("new mark was not eligible: %+v", groups)
	}
}

func TestLegacyRestoredMarkDoesNotRejoinNewBatch(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	desktop := filepath.Join(h, "Desktop")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	oldFile, newFile := filepath.Join(desktop, "old.psd"), filepath.Join(desktop, "new.psd")
	for _, path := range []string{oldFile, newFile} {
		if err := os.WriteFile(path, []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mark := func(path string, at time.Time) {
		t.Helper()
		if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{path}, Agent: "codex", Project: "品牌", WorkType: "设计·UI原型", Now: at}); err != nil {
			t.Fatal(err)
		}
	}
	mark(oldFile, time.Now().Add(-time.Minute))
	p := Policy{Enabled: true, Roots: []string{h}}
	records, err := activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	op, err := Execute(context.Background(), cfg, h, p, Candidates(cfg, p, records)[0])
	if err != nil {
		t.Fatal(err)
	}
	op, err = Restore(cfg, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	op.Signals[0].ActivitySHA = ""
	if err = save(cfg, op); err != nil {
		t.Fatal(err)
	}
	mark(newFile, time.Now().Add(time.Second))
	records, err = activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	groups := Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0] != newFile {
		t.Fatalf("legacy restored mark rejoined the batch: %+v", groups)
	}
	mark(oldFile, time.Now().Add(2*time.Second))
	records, err = activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	groups = Candidates(cfg, p, records)
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("new mark was suppressed by legacy receipt: %+v", groups)
	}
}

func TestAutomaticOrganizationExplainsProtectedProjectFiles(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "existing-project")
	file := filepath.Join(project, "references", "design.png")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"project"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{project}}, Candidate{Project: "新项目", WorkType: "设计·UI原型", Files: []string{file}})
	if err == nil || !strings.Contains(err.Error(), "保持完整工程的内部结构") {
		t.Fatalf("project protection was not explained: %v", err)
	}
	if data, e := os.ReadFile(file); e != nil || string(data) != "image" {
		t.Fatalf("protected file changed: %v", e)
	}
}

func TestAutomaticOrganizationExplainsPartiallyProtectedFiles(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "existing-project")
	protected := filepath.Join(project, "references", "design.png")
	loose := filepath.Join(h, "loose.png")
	if err := os.MkdirAll(filepath.Dir(protected), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(project, "package.json"): `{"name":"project"}`, protected: "inside", loose: "outside"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, Candidate{Project: "新项目", WorkType: "设计·UI原型", Files: []string{protected, loose}})
	if err != nil || o.Status != "partial" || !strings.Contains(o.Error, "保持完整工程的内部结构") {
		t.Fatalf("partial organization did not explain protected file: %+v %v", o, err)
	}
	if _, err = Restore(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "inside" {
		t.Fatal("protected file changed", err)
	}
	if data, err := os.ReadFile(loose); err != nil || string(data) != "outside" {
		t.Fatal("loose file was not restored", err)
	}
}

func TestAutomaticOrganizationKeepsSymlinkSourceAndMovesOtherFiles(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	realDir, linkDir := filepath.Join(h, "real"), filepath.Join(h, "linked")
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(linkDir, "linked.png")
	loose := filepath.Join(h, "loose.png")
	if err := os.WriteFile(filepath.Join(realDir, "linked.png"), []byte("linked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, []byte("loose"), 0600); err != nil {
		t.Fatal(err)
	}
	op, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, Candidate{Project: "新项目", WorkType: "设计·UI原型", Files: []string{linked, loose}})
	if err != nil || op.Status != "partial" || op.Summary == nil || op.Summary.Moved != 1 || !strings.Contains(op.Error, "路径包含软链接") {
		t.Fatal("symlink prevented the whole group", op, err)
	}
	if data, err := os.ReadFile(linked); err != nil || string(data) != "linked" {
		t.Fatal("linked source changed", err)
	}
	if _, err := os.Stat(loose); !os.IsNotExist(err) {
		t.Fatal("loose source was not moved", err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(loose); err != nil || string(data) != "loose" {
		t.Fatal("loose source was not restored", err)
	}
}

func TestInterruptedOperationKeepsReceiptAndCanRestore(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(h, "source.psd")
	if err := os.WriteFile(src, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Project: "中断项目", WorkType: "设计·UI原型", Files: []string{src}}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil || len(o.UndoPaths) != 1 {
		t.Fatal(o, err)
	}
	// Simulate an exit after the move but before the batch summary is persisted.
	o.Status = "organizing"
	o.Summary = nil
	if err = save(cfg, o); err != nil {
		t.Fatal(err)
	}
	if err = FinishInterrupted(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	o, err = read(cfg, o.ID)
	if err != nil || o.Status != "partial" || o.UndoPath == "" || o.Summary == nil || o.Summary.Moved != 1 || len(o.Summary.Items) != 1 || o.Summary.Items[0].Source != src {
		t.Fatal(o, err)
	}
	o, err = Restore(cfg, o.ID)
	if err != nil || o.Status != "restored" {
		t.Fatal(o, err)
	}
	if data, err := os.ReadFile(src); err != nil || string(data) != "design" {
		t.Fatal("file not restored", err)
	}
}

func TestInterruptedReconstructsOnlyCompletedMoves(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(h, "first.psd"), filepath.Join(h, "second.psd")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte(file), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Candidate{Project: "部分完成", WorkType: "设计·UI原型", Files: files}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil || o.Summary == nil || len(o.Summary.Items) != 2 {
		t.Fatal(o, err)
	}
	// One entry was not moved when the simulated process stopped.
	second := o.Summary.Items[1]
	if err = os.Rename(second.Target, second.Source); err != nil {
		t.Fatal(err)
	}
	o.Status, o.Summary = "organizing", nil
	if err = save(cfg, o); err != nil {
		t.Fatal(err)
	}
	if err = FinishInterrupted(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	o, err = read(cfg, o.ID)
	if err != nil || o.Summary == nil || o.Summary.Moved != 1 || len(o.Summary.Items) != 1 || o.Summary.Items[0].Source == second.Source {
		t.Fatal(o, err)
	}
	o, err = Restore(cfg, o.ID)
	if err != nil || o.Status != "restored" || o.Restore.Restored != 1 || o.Restore.Unchanged != 1 {
		t.Fatal(o, err)
	}
	for _, file := range files {
		if data, err := os.ReadFile(file); err != nil || string(data) != file {
			t.Fatal(file, err)
		}
	}
}

func TestInterruptedChangedTargetRemainsUncertain(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	if err := os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(h, "draft.psd")
	if err := os.WriteFile(file, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Project: "更改后中断", WorkType: "设计·UI原型", Files: []string{file}}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil || o.Summary == nil || len(o.Summary.Items) != 1 {
		t.Fatal(o, err)
	}
	target := o.Summary.Items[0].Target
	if err = os.WriteFile(target, []byte("later edit"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Status, o.Summary = "organizing", nil
	if err = save(cfg, o); err != nil {
		t.Fatal(err)
	}
	if err = FinishInterrupted(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	o, err = read(cfg, o.ID)
	if err != nil || o.Summary == nil || o.Summary.Moved != 0 || len(o.Summary.Warnings) == 0 || !strings.Contains(o.Error, "待核对") {
		t.Fatal(o, err)
	}
	o, err = Restore(cfg, o.ID)
	if err != nil || o.Status != "restore_partial" || o.Restore.Failed != 1 {
		t.Fatal(o, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "later edit" {
		t.Fatal("later edit lost", err)
	}
}

func TestLargeGroupCollectsAndRestoresEveryBatch(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	loose := filepath.Join(h, "Desktop")
	os.MkdirAll(loose, 0700)
	c := Candidate{Project: "批量项目", WorkType: "设计·UI原型"}
	for i := 0; i < 130; i++ {
		p := filepath.Join(loose, fmt.Sprintf("设计稿-%03d.psd", i))
		if err := os.WriteFile(p, []byte(fmt.Sprint(i)), 0600); err != nil {
			t.Fatal(err)
		}
		c.Files = append(c.Files, p)
	}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil || o.Status != "organized" || o.Summary.Moved != 130 || len(o.UndoPaths) != 3 {
		t.Fatal("batch organization", o.Status, o.Summary, len(o.UndoPaths), err)
	}
	r, err := Restore(cfg, o.ID)
	if err != nil || r.Status != "restored" || r.Restore.Restored != 130 {
		t.Fatal("batch restore", r.Status, r.Restore, err)
	}
	for i, p := range c.Files {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != fmt.Sprint(i) {
			t.Fatal("file not restored", i, err)
		}
	}
}

func TestLaterOutputJoinsAutomaticallyCreatedProject(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	policy := Policy{Enabled: true, Roots: []string{h}}
	first := filepath.Join(h, "first.psd")
	second := filepath.Join(h, "second.psd")
	os.WriteFile(first, []byte("first"), 0600)
	c1 := Candidate{Project: "持续项目", WorkType: "设计·UI原型", Files: []string{first}}
	o1, err := Execute(context.Background(), cfg, h, policy, c1)
	if err != nil {
		t.Fatal(err)
	}
	if err = MarkMemoryInitialized(cfg, o1.ID); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(second, []byte("second"), 0600)
	c2 := Candidate{Project: c1.Project, WorkType: c1.WorkType, Files: []string{second}}
	o2, err := Execute(context.Background(), cfg, h, policy, c2)
	if err != nil || o2.CreatedProject || o2.Status != "organized" {
		t.Fatal("later output not collected", o2, err)
	}
	if _, err = Restore(cfg, o2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(o1.Target); err != nil {
		t.Fatal("restoring later output removed project", err)
	}
	if _, err = Restore(cfg, o1.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{first, second} {
		if _, err = os.Stat(p); err != nil {
			t.Fatal("file not restored", p, err)
		}
	}
}

func TestAutomaticOrganizationFollowsProjectCustomRules(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "总文件夹项目"
		if external {
			name = "原地项目"
		}
		t.Run(name, func(t *testing.T) {
			h := t.TempDir()
			cfg := config.Default(h)
			project := filepath.Join(cfg.Root, "品牌项目")
			candidate := Candidate{Project: "品牌项目", WorkType: "设计·UI原型"}
			if external {
				project = filepath.Join(h, "Projects", "品牌项目")
				candidate.ProjectKey, candidate.TargetPath = "place:brand", project
			}
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			profile := worktype.Profile{Name: "自己的模板", Folders: []string{"资料", "设计成品"}, Roles: map[string]string{"source": "资料"}, Rules: []worktype.Rule{{Kind: "extension", Value: "psd", Folder: "设计成品"}}}
			body, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			profilePath := filepath.Join(project, worktype.ProfileFile)
			if err = os.WriteFile(profilePath, body, 0600); err != nil {
				t.Fatal(err)
			}
			if external {
				reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "brand", Name: "品牌项目", Path: project}}}
				if _, err = registry.Save(cfg, reg, h); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(h, "Desktop", "封面.psd")
			if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(file, []byte("design"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate.Files = []string{file}
			o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, candidate)
			if err != nil || o.Status != "organized" || o.Summary == nil || o.Summary.Moved != 1 {
				t.Fatal(o, err)
			}
			if !strings.Contains(o.PlacementReasons[file], "匹配自定义规则") {
				t.Fatal("custom rule evidence missing", o.PlacementReasons)
			}
			target := filepath.Join(project, "设计成品", "封面.psd")
			if data, e := os.ReadFile(target); e != nil || string(data) != "design" {
				t.Fatal("custom rule ignored", e)
			}
			if _, err = Restore(cfg, o.ID); err != nil {
				t.Fatal(err)
			}
			if data, e := os.ReadFile(file); e != nil || string(data) != "design" {
				t.Fatal("file not restored", e)
			}
			if data, e := os.ReadFile(profilePath); e != nil || string(data) != string(body) {
				t.Fatal("custom profile changed", e)
			}
		})
	}
}

func TestAutomaticOrganizationReportsInvalidProjectProfile(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(cfg.Root, "品牌项目")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, worktype.ProfileFile), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(h, "draft.psd")
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Project: "品牌项目", WorkType: "设计·UI原型", Files: []string{file}}
	if _, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c); err == nil || !strings.Contains(err.Error(), "自定义整理规则无法读取") {
		t.Fatal("invalid profile ignored", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file changed", err)
	}
}

func TestRegisteredProjectFilesStayWhenSameNameExistsInProjectRoot(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "in-place", "品牌")
	member := filepath.Join(h, "in-place", "品牌-version")
	inside := filepath.Join(project, "任务", "report.md")
	insideMember := filepath.Join(member, "assets", "design.png")
	loose := filepath.Join(h, "Desktop", "loose.png")
	for _, file := range []string{inside, insideMember, loose} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, "品牌"), 0700); err != nil {
		t.Fatal(err)
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "brand", Name: "品牌", Path: project, Members: []registry.Member{{Path: member, IsDir: true}}}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	var records []activity.Record
	for _, file := range []string{inside, insideMember, loose} {
		records = append(records, activity.Record{Path: file, Project: "品牌", Agent: "codex", WorkType: "设计·UI原型"})
	}
	groups := Candidates(cfg, Policy{Enabled: true, Roots: []string{h}}, records)
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0] != loose {
		t.Fatalf("files already in the registered project were treated as scattered: %+v", groups)
	}
}

func TestSameNamedExternalProjectsAreNotMergedByGuess(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	file := filepath.Join(h, "Desktop", "draft.psd")
	one := filepath.Join(h, "first", "品牌")
	two := filepath.Join(h, "second", "品牌")
	for _, dir := range []string{filepath.Dir(file), one, two, cfg.Root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "one", Name: "品牌", Path: one}, {ID: "two", Name: "品牌", Path: two}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	r := activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", WorkType: "设计·UI原型", Note: `{"session":"ambiguous","sourcePath":"/synthetic/session.jsonl"}`}
	p := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(r): "品牌"}}
	groups := Candidates(cfg, p, []activity.Record{r})
	if len(groups) != 1 {
		t.Fatal(groups)
	}
	if _, err := Execute(context.Background(), cfg, h, p, groups[0]); err == nil || !strings.Contains(err.Error(), "多个同名项目") {
		t.Fatal("ambiguous destination was accepted", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("source changed", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "品牌")); !os.IsNotExist(err) {
		t.Fatal("duplicate project created", err)
	}
}

func TestSessionAssignmentDoesNotOverrideLaterKnownProject(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "Projects", "新项目")
	oldProject := filepath.Join(h, "Projects", "旧项目")
	code := filepath.Join(project, "main.go")
	newFile := filepath.Join(h, "Desktop", "new.psd")
	oldFile := filepath.Join(h, "Desktop", "old.psd")
	for _, dir := range []string{filepath.Join(project, ".git"), oldProject, filepath.Dir(newFile), cfg.Root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{code: "package main", newFile: "new work", oldFile: "old work"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "new", Name: "新项目", Path: project}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"session": "same-session", "sourcePath": "/synthetic/session.jsonl", "workingDir": project})
	oldMetadata, _ := json.Marshal(map[string]any{"session": "same-session", "sourcePath": "/synthetic/session.jsonl", "workingDir": oldProject})
	newRecord := activity.Record{Path: newFile, Project: "新项目", WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: string(metadata)}
	oldRecord := activity.Record{Path: oldFile, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: string(oldMetadata)}
	policy := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(oldRecord): "旧项目"}}
	single := Candidates(cfg, policy, []activity.Record{newRecord})
	if len(single) != 1 || single[0].Project != "旧项目" {
		t.Fatalf("explicit choice changed without a work-directory switch: %+v", single)
	}
	groups := Candidates(cfg, policy, []activity.Record{newRecord, oldRecord})
	if len(groups) != 2 {
		t.Fatalf("different projects in one session were merged: %+v", groups)
	}
	var next Candidate
	for _, group := range groups {
		if group.Project == "新项目" {
			next = group
		}
	}
	if next.ProjectKey != "place:new" || next.TargetPath != project || len(next.Files) != 1 || next.Files[0] != newFile {
		t.Fatalf("known project lost to earlier session assignment: %+v", next)
	}
	op, err := Execute(context.Background(), cfg, h, policy, next)
	if err != nil || op.Target != project || op.Summary == nil || op.Summary.Moved != 1 {
		t.Fatalf("known project was not used: %+v %v", op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{code: "package main", newFile: "new work", oldFile: "old work"} {
		if data, e := os.ReadFile(path); e != nil || string(data) != want {
			t.Fatalf("file changed or not restored: %v", e)
		}
	}
}

func TestSessionAssignmentStaysWithinSameRegisteredProject(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "Projects", "工程")
	subdir := filepath.Join(project, "src")
	file := filepath.Join(h, "Desktop", "draft.psd")
	for _, dir := range []string{cfg.Root, subdir, filepath.Dir(file)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "code", Name: "工程", Path: project}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	metadata := func(cwd string) string {
		b, _ := json.Marshal(map[string]any{"session": "one", "sourcePath": "/synthetic/session.jsonl", "workingDir": cwd})
		return string(b)
	}
	old := activity.Record{Path: filepath.Join(h, "Desktop", "previous.psd"), Agent: "codex", Source: "会话文件操作", Note: metadata(project)}
	current := activity.Record{Path: file, Project: "工程", WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: metadata(subdir)}
	p := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(old): "用户选定项目"}}
	groups := Candidates(cfg, p, []activity.Record{old, current})
	if len(groups) != 1 || groups[0].Project != "用户选定项目" {
		t.Fatalf("moving within one project changed the user's assignment: %+v", groups)
	}
}

func TestSessionSeparatesNewUnregisteredWorkContext(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	oldDir, newDir := filepath.Join(h, "old-work"), filepath.Join(h, "new-work")
	oldFile, newFile := filepath.Join(h, "Desktop", "old.psd"), filepath.Join(h, "Downloads", "new.psd")
	for _, dir := range []string{cfg.Root, oldDir, newDir, filepath.Dir(oldFile), filepath.Dir(newFile)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{oldFile, newFile} {
		if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := func(cwd string, line int) string {
		b, _ := json.Marshal(map[string]any{"session": "same-session", "sourcePath": "/synthetic/session.jsonl", "workingDir": cwd, "line": line})
		return string(b)
	}
	old := activity.Record{Path: oldFile, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: metadata(oldDir, 2)}
	newWork := activity.Record{Path: newFile, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: metadata(newDir, 6)}
	p := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(old): "用户选择的旧项目"}}
	records := []activity.Record{newWork, old}
	groups := UnassignedSessions(cfg, p, records)
	if len(groups) != 1 || groups[0].Count != 1 || groups[0].Examples[0] != newFile || groups[0].ID == SessionKey(old) {
		t.Fatalf("new work was not independently grouped: %+v", groups)
	}
	tasks := ModelTasks(cfg, p, records)
	if len(tasks) != 1 || tasks[0].ID != groups[0].ID {
		t.Fatalf("new work was not offered for model judgment: %+v", tasks)
	}
	if !applyModelDecision(&p, tasks[0], ModelDecision{Project: "新项目", WorkType: "设计·UI原型", Confident: true}) {
		t.Fatal("could not save independent model decision")
	}
	candidates := Candidates(cfg, p, records)
	if len(candidates) != 2 {
		t.Fatalf("projects were merged: %+v", candidates)
	}
	var newCandidate Candidate
	for _, candidate := range candidates {
		if candidate.Project == "新项目" {
			newCandidate = candidate
		}
	}
	if len(newCandidate.Files) != 1 || newCandidate.Files[0] != newFile || newCandidate.Signals[0].SessionKey != groups[0].ID {
		t.Fatalf("new work inherited the old project or model evidence: %+v", newCandidate)
	}
	op, err := Execute(context.Background(), cfg, h, p, newCandidate)
	if err != nil || op.Status != "organized" || op.Summary == nil || op.Summary.Moved != 1 {
		t.Fatal(op, err)
	}
	if _, err = Restore(cfg, op.ID); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{oldFile, newFile} {
		if data, e := os.ReadFile(file); e != nil || string(data) != "design" {
			t.Fatalf("file changed or not restored: %v", e)
		}
	}
}

func TestSessionContextChoiceSurvivesProjectRegistration(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	oldDir, newDir := filepath.Join(h, "old-work"), filepath.Join(h, "new-work")
	oldFile, newFile := filepath.Join(h, "Desktop", "old.psd"), filepath.Join(h, "Downloads", "new.psd")
	for _, dir := range []string{cfg.Root, oldDir, newDir, filepath.Dir(oldFile), filepath.Dir(newFile)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{oldFile, newFile} {
		if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := func(cwd string, line int) string {
		b, _ := json.Marshal(map[string]any{"session": "same", "sourcePath": "/synthetic/session.jsonl", "workingDir": cwd, "line": line})
		return string(b)
	}
	old := activity.Record{Path: oldFile, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: metadata(oldDir, 2)}
	newWork := activity.Record{Path: newFile, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: metadata(newDir, 6)}
	p := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(old): "旧项目"}}
	records := []activity.Record{old, newWork}
	groups := UnassignedSessions(cfg, p, records)
	if len(groups) != 1 {
		t.Fatal(groups)
	}
	p.SessionProjects[groups[0].ID] = "用户指定新项目"
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "new", Name: "新工程", Path: newDir}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	if groups = UnassignedSessions(cfg, p, records); len(groups) != 0 {
		t.Fatalf("project registration lost the user's context choice: %+v", groups)
	}
	candidates := Candidates(cfg, p, records)
	for _, candidate := range candidates {
		if candidate.Project == "用户指定新项目" && len(candidate.Files) == 1 && candidate.Files[0] == newFile {
			return
		}
	}
	t.Fatalf("project registration changed the user's choice: %+v", candidates)
}

func TestUnassignedSessionContextsCanBeJudgedIndependently(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	var records []activity.Record
	for i, name := range []string{"一", "二"} {
		dir := filepath.Join(h, "work-"+name)
		file := filepath.Join(h, "Desktop", name+".psd")
		for _, path := range []string{cfg.Root, dir, filepath.Dir(file)} {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(file, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(map[string]any{"session": "same", "sourcePath": "/synthetic/session.jsonl", "workingDir": dir, "line": i + 1})
		records = append(records, activity.Record{Path: file, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: string(metadata)})
	}
	p := Policy{Enabled: true, Roots: []string{h}}
	groups := UnassignedSessions(cfg, p, records)
	if len(groups) != 2 || groups[0].ID == groups[1].ID || groups[0].Count != 1 || groups[1].Count != 1 {
		t.Fatalf("distinct work contexts were merged: %+v", groups)
	}
	tasks := ModelTasks(cfg, p, records)
	if len(tasks) != 2 {
		t.Fatal(tasks)
	}
	for i, task := range tasks {
		if !applyModelDecision(&p, task, ModelDecision{Project: fmt.Sprintf("项目%d", i), WorkType: "设计·UI原型", Confident: true}) {
			t.Fatal("could not save independent decisions")
		}
	}
	candidates := Candidates(cfg, p, records)
	if len(candidates) != 2 || candidates[0].Project == candidates[1].Project || len(candidates[0].Files) != 1 || len(candidates[1].Files) != 1 {
		t.Fatalf("independent decisions were merged: %+v", candidates)
	}
}

func TestSessionContextsOutsideFileRootsStaySeparateWithoutDisclosure(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	var records []activity.Record
	for i, name := range []string{"old", "new"} {
		file := filepath.Join(h, "Desktop", name+".psd")
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(map[string]any{"session": "same", "sourcePath": "/synthetic/session.jsonl", "workingDir": "/not-authorized/" + name, "line": i + 1})
		records = append(records, activity.Record{Path: file, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: string(metadata)})
	}
	p := Policy{Enabled: true, Roots: []string{h}, SessionProjects: map[string]string{SessionKey(records[0]): "原项目"}}
	groups := UnassignedSessions(cfg, p, records)
	if len(groups) != 1 || groups[0].Count != 1 || groups[0].Examples[0] != records[1].Path {
		t.Fatalf("different session contexts outside file roots were merged: %+v", groups)
	}
	tasks := ModelTasks(cfg, p, records)
	data, err := json.Marshal(tasks)
	if err != nil || len(tasks) != 1 || strings.Contains(string(data), "/not-authorized/") {
		t.Fatalf("model task exposed the session working directory: %s %v", data, err)
	}
}

func TestRegisteredProjectOutsideFileRootsKeepsOneContext(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	project := filepath.Join(h, "Projects", "工程")
	desktop, downloads := filepath.Join(h, "Desktop"), filepath.Join(h, "Downloads")
	for _, dir := range []string{cfg.Root, filepath.Join(project, "src"), desktop, downloads} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "code", Name: "工程", Path: project}}}
	if _, err := registry.Save(cfg, reg, h); err != nil {
		t.Fatal(err)
	}
	var records []activity.Record
	for i, entry := range []struct{ dir, file string }{{project, filepath.Join(desktop, "first.psd")}, {filepath.Join(project, "src"), filepath.Join(downloads, "second.psd")}} {
		if err := os.WriteFile(entry.file, []byte("design"), 0600); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(map[string]any{"session": "same", "sourcePath": "/synthetic/session.jsonl", "workingDir": entry.dir, "line": i + 1})
		records = append(records, activity.Record{Path: entry.file, WorkType: "设计·UI原型", Agent: "codex", Source: "会话文件操作", Note: string(metadata)})
	}
	p := Policy{Enabled: true, Roots: []string{desktop, downloads}, SessionProjects: map[string]string{SessionKey(records[0]): "用户指定项目"}}
	candidates := Candidates(cfg, p, records)
	if len(candidates) != 1 || candidates[0].Project != "用户指定项目" || len(candidates[0].Files) != 2 {
		t.Fatalf("one registered project outside file roots split the user's choice: %+v", candidates)
	}
	p.SessionProjects = nil
	records[1].Project = "工程"
	candidates = Candidates(cfg, p, records)
	if len(candidates) != 1 || candidates[0].TargetPath != "" || candidates[0].ProjectKey != "" {
		t.Fatalf("out-of-scope project became a placement target: %+v", candidates)
	}
}

func TestLooseFilesUnderWorkRootAreCollected(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	file := filepath.Join(cfg.Root, "散落需求.md")
	os.WriteFile(file, []byte("brief"), 0600)
	p := Policy{Enabled: true, Roots: []string{h}}
	groups := Candidates(cfg, p, []activity.Record{{Path: file, Project: "新项目", WorkType: "设计·UI原型", Agent: "codex"}})
	if len(groups) != 1 {
		t.Fatal("loose file under work root was skipped", groups)
	}
	o, err := Execute(context.Background(), cfg, h, p, groups[0])
	if err != nil || o.Status != "organized" {
		t.Fatal(o, err)
	}
	if _, err = Restore(cfg, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("file not restored", err)
	}
}

func TestObjectionPreservesLaterProjectWork(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	path := filepath.Join(h, "draft.psd")
	os.WriteFile(path, []byte("draft"), 0600)
	c := Candidate{Project: "设计项目", WorkType: "设计·UI原型", Files: []string{path}}
	o, err := Execute(context.Background(), cfg, h, Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(o.Target, "later.txt")
	if err = os.WriteFile(newFile, []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Restore(cfg, o.ID)
	if err != nil || r.Status != "restored" || r.Error == "" {
		t.Fatal(r, err)
	}
	if b, err := os.ReadFile(newFile); err != nil || string(b) != "later" {
		t.Fatal("later work lost", err)
	}
}
