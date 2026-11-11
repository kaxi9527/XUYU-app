//go:build unix

package appdir

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkShelfFIFORejected(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	p := filepath.Join(root, "logins.json")
	if err := syscall.Mkfifo(p, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := WorkShelfReadFile(p); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked")
	}
}
