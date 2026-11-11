//go:build darwin

package projectmemory

import (
	"fmt"
	"os"
	"syscall"
)

func objectKey(info os.FileInfo) string {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Birthtimespec.Sec == 0 || info.Mode().IsRegular() && s.Nlink != 1 {
		return ""
	}
	return fmt.Sprintf("darwin:%d:%d:%d:%d", s.Dev, s.Ino, s.Birthtimespec.Sec, s.Birthtimespec.Nsec)
}
