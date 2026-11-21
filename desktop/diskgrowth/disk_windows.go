package diskgrowth

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

func Volume(home string) string   { return filepath.VolumeName(home) + string(filepath.Separator) }
func deviceID(os.FileInfo) uint64 { return 0 }
func Capacity(path string) (uint64, uint64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, 0, e
	}
	var free, total uint64
	e = windows.GetDiskFreeSpaceEx(p, &free, &total, nil)
	return free, total, e
}
func fileUsage(path string, _ os.FileInfo) (int64, identity, bool, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, identity{}, false, e
	}
	h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return 0, identity{}, false, e
	}
	defer windows.CloseHandle(h)
	var standard struct {
		Allocation, End   int64
		Links             uint32
		Delete, Directory byte
		Padding           [2]byte
	}
	if e = windows.GetFileInformationByHandleEx(h, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&standard)), uint32(unsafe.Sizeof(standard))); e != nil {
		return 0, identity{}, false, e
	}
	if standard.Links <= 1 {
		return standard.Allocation, identity{}, false, nil
	}
	var i windows.ByHandleFileInformation
	e = windows.GetFileInformationByHandle(h, &i)
	return standard.Allocation, identity{uint64(i.VolumeSerialNumber), uint64(i.FileIndexHigh)<<32 | uint64(i.FileIndexLow)}, true, e
}
