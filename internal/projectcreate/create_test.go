package projectcreate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestCreateRetryAndPreserve(t *testing.T) {
	root := t.TempDir()
	w := worktype.All()[0]
	l := worktype.LayoutFor(w.Template)
	in := Input{OperationID: "test-project-create-01", Name: "春季项目", Root: root, WorkType: w.Label(), Profile: worktype.Profile{Name: l.Name, Folders: l.Folders, Roles: l.Roles}}
	one, err := Create(root, "说明.md", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Create(root, "说明.md", in); err != nil {
		t.Fatal("retry", err)
	}
	if _, err := worktype.ReadProfile(filepath.Join(one.Path, worktype.ProfileFile)); err != nil {
		t.Fatal(err)
	}
	in.OperationID = "test-project-create-02"
	if _, err := Create(root, "说明.md", in); err == nil {
		t.Fatal("existing project accepted")
	}
	in.OperationID = "test-project-create-01"
	readme := filepath.Join(one.Path, "说明.md")
	if err := os.WriteFile(readme, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, "说明.md", in); err == nil {
		t.Fatal("changed file accepted")
	}
	data, _ := os.ReadFile(readme)
	if string(data) != "user edit" {
		t.Fatal("user content overwritten")
	}
}

func TestCreateRejectsScopeAndSymlinks(t *testing.T) {
	root := t.TempDir()
	w := worktype.All()[0]
	in := Input{OperationID: "test-project-create-01", Name: "项目", Root: root, WorkType: w.Label(), Profile: worktype.Profile{Name: "自定义", Folders: []string{"文件"}}}
	for _, name := range []string{"../escape", ".hidden", "_总控", " padded "} {
		bad := in
		bad.Name = name
		if _, err := Create(root, "说明.md", bad); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	bad := in
	bad.Root = t.TempDir()
	if _, err := Create(root, "说明.md", bad); err == nil {
		t.Fatal("root drift accepted")
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, in.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, "说明.md", in); err == nil {
		t.Fatal("symlink accepted")
	}
	files, _ := os.ReadDir(external)
	if len(files) != 0 {
		t.Fatal("wrote through symlink")
	}
}
