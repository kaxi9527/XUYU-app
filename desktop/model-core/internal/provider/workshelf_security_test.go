package provider

import (
	"errors"
	"github.com/yetone/magpie/internal/appdir"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkShelfClaudeKeychainCleanup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	oldFlag, oldDelete := claudeKeychain, workShelfDeleteClaudeKey
	claudeKeychain = true
	t.Cleanup(func() { claudeKeychain = oldFlag; workShelfDeleteClaudeKey = oldDelete })
	content := `[{"agent":"claude","user":"fixture","on":false}]`
	if err := appdir.WorkShelfWriteFile(loginsPath(), []byte(content)); err != nil {
		t.Fatal(err)
	}
	dir := claudeAccountDir("fixture")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	called := 0
	workShelfDeleteClaudeKey = func(service, account string) error {
		called++
		if service != claudeDirService(dir) {
			t.Fatal("wrong Keychain namespace")
		}
		return errors.New("fixture locked")
	}
	if err := UXQAEditLogin("claude", "fixture", nil, true); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	b, _ := os.ReadFile(loginsPath())
	if !strings.Contains(string(b), "fixture") {
		t.Fatal("account lost on cleanup failure")
	}
	workShelfDeleteClaudeKey = func(service, account string) error { called++; return nil }
	if err := UXQAEditLogin("claude", "fixture", nil, true); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatal("cleanup not retried")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("private account directory remains")
	}
}

func TestWorkShelfAccountBoundaries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	foreign := filepath.Join(t.TempDir(), "logins.json")
	content := `[{"agent":"codex","user":"fixture","on":false}]`
	if err := os.WriteFile(foreign, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, loginsPath()); err != nil {
		t.Fatal(err)
	}
	if WorkShelfAccountStorageError() == nil {
		t.Fatal("external account source accepted")
	}
	if err := writePrivate(loginsPath(), []byte("[]")); err == nil {
		t.Fatal("external account write accepted")
	}
	b, _ := os.ReadFile(foreign)
	if string(b) != content {
		t.Fatal("foreign changed")
	}
}

func TestWorkShelfClaudeRemoveBoundary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	content := `[{"agent":"claude","user":"fixture","on":false}]`
	if err := appdir.WorkShelfWriteFile(loginsPath(), []byte(content)); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, claudeDirsRoot()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(foreign, filepath.Base(claudeAccountDir("fixture")))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UXQAEditLogin("claude", "fixture", nil, true); err == nil {
		t.Fatal("linked Claude directory removed")
	}
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != "keep" {
		t.Fatal("foreign directory changed", err)
	}
	b, _ = os.ReadFile(loginsPath())
	if !strings.Contains(string(b), "fixture") {
		t.Fatal("account removed after failed cleanup")
	}
}
