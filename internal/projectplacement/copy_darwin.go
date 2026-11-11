//go:build darwin && cgo

package projectplacement

/*
#include <copyfile.h>
#include <errno.h>
static int shelf_copy_file_all(int source, int target) {
    if (fcopyfile(source, target, NULL, COPYFILE_ALL) == 0) return 0;
    return errno;
}
*/
import "C"

import (
	"os"
	"syscall"
)

// Copy data, permissions, timestamps, ACLs and extended attributes through
// already-open descriptors. Content and object identity are checked separately.
func copyFileMetadataAndData(source, target *os.File) error {
	if code := C.shelf_copy_file_all(C.int(source.Fd()), C.int(target.Fd())); code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
