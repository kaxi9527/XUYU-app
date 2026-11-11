//go:build !unix

package put

import "os"

// 非 Unix 系统第一版不加锁（WorkShelf 第一版只支持 macOS / Linux）。
func lockFile(f *os.File) error   { return nil }
func unlockFile(f *os.File) error { return nil }
