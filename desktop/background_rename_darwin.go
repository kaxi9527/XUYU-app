//go:build darwin && cgo

package main

/*
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
static int background_rename_exclusive(int fd, const char *from, const char *to) {
    if (renameatx_np(fd, from, fd, to, RENAME_EXCL) == 0) return 0;
    return errno;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"
)

func backgroundPlatformSupported() bool { return true }
func renameBackgroundExclusive(root *os.Root, from, to string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	a, b := C.CString(from), C.CString(to)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	if code := C.background_rename_exclusive(C.int(dir.Fd()), a, b); code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
