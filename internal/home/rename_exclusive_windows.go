//go:build windows

package home

import (
	"os"
	"syscall"
)

func renameExclusive(from, to string) error {
	if err := syscall.Rename(from, to); err != nil {
		return &os.LinkError{Op: "rename-exclusive", Old: from, New: to, Err: err}
	}
	return nil
}
