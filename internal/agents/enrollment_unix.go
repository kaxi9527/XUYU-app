//go:build linux || (darwin && cgo)

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Walk by directory handles with O_NOFOLLOW, so replacing an ancestor with a
// symlink cannot redirect a rule write to another tree.
func openEnrollment(path string, flags int) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("规则需要完整路径")
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	dir, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range parts[:len(parts)-1] {
		next, e := openEnrollmentAt(dir, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		syscall.Close(dir)
		if e != nil {
			return nil, e
		}
		dir = next
	}
	fd, err := openEnrollmentAt(dir, parts[len(parts)-1], flags|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	syscall.Close(dir)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func enrollmentSupported() bool { return true }
func singleEnrollmentLink(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}
func lockEnrollment(ctx context.Context, f *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func unlockEnrollment(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
