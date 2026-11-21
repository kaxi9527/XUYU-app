//go:build darwin || linux

package diskgrowth

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func Volume(home string) string {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/System/Volumes/Data"); err == nil {
			return "/System/Volumes/Data"
		}
	}
	path := home
	for {
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		a, e := os.Stat(path)
		b, f := os.Stat(parent)
		if e != nil || f != nil || deviceID(a) != deviceID(b) {
			return path
		}
		path = parent
	}
}
func deviceID(s os.FileInfo) uint64 { return uint64(s.Sys().(*syscall.Stat_t).Dev) }
func fileUsage(_ string, s os.FileInfo) (int64, identity, bool, error) {
	i := s.Sys().(*syscall.Stat_t)
	return i.Blocks * 512, identity{uint64(i.Dev), uint64(i.Ino)}, i.Nlink > 1, nil
}
func Capacity(path string) (uint64, uint64, error) {
	var s syscall.Statfs_t
	err := syscall.Statfs(path, &s)
	return uint64(s.Bavail) * uint64(s.Bsize), uint64(s.Blocks) * uint64(s.Bsize), err
}
