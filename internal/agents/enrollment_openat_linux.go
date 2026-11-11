package agents

import "syscall"

func openEnrollmentAt(dir int, path string, flags int, mode uint32) (int, error) {
	return syscall.Openat(dir, path, flags, mode)
}
