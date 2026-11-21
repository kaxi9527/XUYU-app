package diskgrowth

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func (h History) TryLock() (func(), error) {
	if e := os.MkdirAll(h.Dir, 0700); e != nil {
		return nil, e
	}
	path, e := windows.UTF16PtrFromString(filepath.Join(h.Dir, "sample.lock"))
	if e != nil {
		return nil, e
	}
	handle, e := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return func() { windows.CloseHandle(handle) }, nil
}
