package allocate

import (
	"path/filepath"
	"testing"
)

func TestImageFormatsDoNotChooseFirstBusinessRole(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.png", "b.jpg", "c.png"} {
		write(t, filepath.Join(dir, name), "fixture")
	}
	for _, in := range InferProject(InferOptions{ProjectDir: dir}) {
		if in.Label == "电商·主图" && in.PreConfirm {
			t.Fatal("image format was promoted to e-commerce business evidence")
		}
	}
}
func TestEditableUIFileProvidesWorkTypeEvidence(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "首页.fig"), "fixture")
	for _, in := range InferProject(InferOptions{ProjectDir: dir}) {
		if in.Label == "设计·UI原型" && in.PreConfirm {
			return
		}
	}
	t.Fatal("UI source evidence missing")
}

func TestCodeEvidencePrecedesExampleImageNames(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n")
	write(t, filepath.Join(dir, "电商主图示例.png"), "fixture")
	inferred := InferProject(InferOptions{ProjectDir: dir})
	if len(inferred) == 0 || inferred[0].Label != "开发·功能开发" {
		t.Fatalf("code project was classified from an example filename: %#v", inferred)
	}
}
