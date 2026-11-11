//go:build (darwin || linux) && cgo

package projectplacement

/*
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <fcntl.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif
static int shelf_renameat_exclusive(int fromdir, const char *from, int todir, const char *to) {
#ifdef __APPLE__
    if (renameatx_np(fromdir, from, todir, to, RENAME_EXCL) == 0) return 0;
#else
    if (syscall(SYS_renameat2, fromdir, from, todir, to, 1) == 0) return 0;
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

// Both directories remain anchored to open capabilities. No absolute path or
// multi-component relative path is accepted by the OS mutation primitive.
func renameAtExclusive(fromRoot *os.Root, from string, toRoot *os.Root, to string) error {
	if !safeName(from) || !safeName(to) {
		return ErrScope
	}
	fd, err := fromRoot.Open(".")
	if err != nil {
		return err
	}
	defer fd.Close()
	td, err := toRoot.Open(".")
	if err != nil {
		return err
	}
	defer td.Close()
	f, t := C.CString(from), C.CString(to)
	defer C.free(unsafe.Pointer(f))
	defer C.free(unsafe.Pointer(t))
	if code := C.shelf_renameat_exclusive(C.int(fd.Fd()), f, C.int(td.Fd()), t); code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
