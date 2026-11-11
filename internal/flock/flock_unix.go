//go:build unix

// Package flock 给追加写的记录文件加排他锁。
package flock

import (
	"os"
	"syscall"
)

// Lock 加排他锁（阻塞等待）。
func Lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// Unlock 解锁。
func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
