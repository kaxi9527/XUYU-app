//go:build darwin || linux

package diskgrowth

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func (h History) TryLock() (func(), error) {
	if e := os.MkdirAll(h.Dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(h.Dir, "sample.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if e != nil {
		f.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, e
	}
	return func() { f.Close() }, nil
}
