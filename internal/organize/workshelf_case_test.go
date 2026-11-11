package organize

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/home"
)

func TestGatherProjectKeepsNestedApplicationAndGitProjectInPlace(t *testing.T) {
	cfg, userHome := fixture(t)
	source := filepath.Join(userHome, "Workspace", "WorkShelf")
	app := filepath.Join(source, "任务", "工具", "WorkShelf.app")
	appFile := filepath.Join(app, "Contents", "MacOS", "WorkShelf")
	code := filepath.Join(source, "代码", "工程")
	document := filepath.Join(source, "任务", "报告", "说明.md")
	write(t, appFile, "app executable")
	write(t, filepath.Join(code, ".git", "HEAD"), "git state")
	write(t, filepath.Join(code, "main.go"), "source")
	write(t, document, "report")
	target := filepath.Join(cfg.Root, "WorkShelf")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildAtContext(t.Context(), cfg, userHome, Request{Project: "WorkShelf", Sources: []string{source}, Profile: scopedProfile()}, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range plan.Rows {
		if row.Selected && (in(app, row.Source) || in(code, row.Source) || in(row.Source, app) || in(row.Source, code)) {
			t.Fatalf("protected project would be moved or split: %+v", row)
		}
	}
	if len(selected(plan)) == 0 {
		t.Fatalf("ordinary project documents should still be gathered: %+v", plan.Rows)
	}
	result, err := plan.Apply(cfg, selected(plan))
	if err != nil || result.Failed != 0 || result.Moved == 0 {
		t.Fatalf("apply: %+v %v", result, err)
	}
	if _, err := os.Stat(appFile); err != nil {
		t.Fatal("application package was moved:", err)
	}
	if _, err := os.Stat(filepath.Join(code, "main.go")); err != nil {
		t.Fatal("Git project was moved:", err)
	}
	restored, err := home.Undo(cfg, result.UndoPath)
	if err != nil || restored.Failed != 0 || restored.Skipped != 0 || restored.Restored != result.Moved {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if data, err := os.ReadFile(document); err != nil || string(data) != "report" {
		t.Fatalf("original document not restored: %q %v", data, err)
	}
}

func TestApplicationPackageContentsCannotBeOrganizedSeparately(t *testing.T) {
	for _, merge := range []bool{false, true} {
		cfg, userHome := fixture(t)
		appFile := filepath.Join(userHome, "Desktop", "WorkShelf.app", "Contents", "MacOS", "WorkShelf")
		write(t, appFile, "app executable")
		plan, err := Build(cfg, userHome, Request{Project: "WorkShelf", Sources: []string{appFile}, Profile: scopedProfile(), Merge: merge})
		if err != nil {
			t.Fatal(err)
		}
		if len(selected(plan)) != 0 {
			t.Fatalf("merge=%v selected a file inside an application: %+v", merge, plan.Rows)
		}
	}
}
