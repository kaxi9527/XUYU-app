//go:build unix

package plan

import (
	"os"
	"syscall"
)

// deviceOf 返回文件所在的设备号，用来判断是不是跨卷。
func deviceOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
