//go:build (darwin || linux) && cgo

package home

/*
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#ifdef __linux__
#include <fcntl.h>
#include <unistd.h>
#include <sys/syscall.h>
#endif
static int shelf_rename_exclusive(const char *from, const char *to) {
#ifdef __APPLE__
    if (renamex_np(from, to, RENAME_EXCL) == 0) return 0;
#else
    if (syscall(SYS_renameat2, AT_FDCWD, from, AT_FDCWD, to, 1) == 0) return 0;
#endif
    return errno;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"
)

// renameExclusive asks the OS to reject an existing destination atomically.
func renameExclusive(from, to string) error {
	f, t := C.CString(from), C.CString(to)
	defer C.free(unsafe.Pointer(f))
	defer C.free(unsafe.Pointer(t))
	if code := C.shelf_rename_exclusive(f, t); code != 0 {
		return &os.LinkError{Op: "rename-exclusive", Old: from, New: to, Err: syscall.Errno(code)}
	}
	return nil
}
