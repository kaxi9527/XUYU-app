package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkShelfWriteConfined(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "settings.json")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if err := WorkShelfWriteFile(link, []byte("new")); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := WorkShelfReadFile(link); err == nil {
		t.Fatal("symlink read accepted")
	}
	if err := WorkShelfRemoveAll(link); err == nil {
		t.Fatal("symlink deletion accepted")
	}
	if err := os.Symlink(filepath.Dir(foreign), filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := WorkShelfWriteFile(filepath.Join(root, "nested", "foreign.json"), []byte("new")); err == nil {
		t.Fatal("parent symlink accepted")
	}
	if err := WorkShelfWriteFile(foreign, []byte("new")); err == nil {
		t.Fatal("foreign path accepted")
	}
	data, _ := os.ReadFile(foreign)
	if string(data) != "keep" {
		t.Fatal("foreign changed")
	}
	path := filepath.Join(root, "own", "accounts.json")
	for _, text := range []string{"first", "second"} {
		if err := WorkShelfWriteFile(path, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	data, _ = os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(data) != "second" || fi.Mode().Perm() != 0600 {
		t.Fatal("private atomic update")
	}
}
