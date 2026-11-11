//go:build darwin && cgo

package agents

/*
#include <fcntl.h>
#include <errno.h>
#include <stdlib.h>
static int workshelf_rule_openat(int dir, const char *path, int flags, unsigned int mode) {
    int fd = openat(dir, path, flags, mode);
    return fd < 0 ? -errno : fd;
}
*/
import "C"

import (
	"syscall"
	"unsafe"
)

func openEnrollmentAt(dir int, path string, flags int, mode uint32) (int, error) {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	fd := C.workshelf_rule_openat(C.int(dir), p, C.int(flags), C.uint(mode))
	if fd < 0 {
		return -1, syscall.Errno(-fd)
	}
	return int(fd), nil
}
