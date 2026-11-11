//go:build darwin && cgo

package home

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
int workshelf_cross_operation(int operation, const char *source, const char *target, char **message);
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

func moveCrossDevice(source, target string) error {
	before, err := crossSnapshot(source)
	if err != nil {
		return err
	}
	if err := crossOperation(0, source, target); err != nil {
		return fmt.Errorf("跨磁盘传输未完成，源文件保留，请核对目标 %s：%w", target, err)
	}
	after, sourceErr := crossSnapshot(source)
	transferred, targetErr := crossSnapshot(target)
	if sourceErr != nil || targetErr != nil || before != after || before != transferred {
		return fmt.Errorf("跨磁盘传输期间文件发生变化或核对失败，源文件与目标保留，请检查：%s", target)
	}
	if err := crossOperation(1, source, ""); err != nil {
		return fmt.Errorf("目标已传输，但源位置未完全移除，请检查两处：%w", err)
	}
	return nil
}

func crossOperation(operation int, source, target string) error {
	from, to := C.CString(source), C.CString(target)
	defer C.free(unsafe.Pointer(from))
	defer C.free(unsafe.Pointer(to))
	var message *C.char
	if C.workshelf_cross_operation(C.int(operation), from, to, &message) != 0 {
		defer C.free(unsafe.Pointer(message))
		return errors.New(C.GoString(message))
	}
	return nil
}
