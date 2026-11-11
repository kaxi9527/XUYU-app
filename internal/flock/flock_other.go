//go:build !unix

// Package flock 给追加写的记录文件加排他锁。
package flock

import "os"

// Lock 在非 Unix 系统上暂时不加锁（Windows 版另做）。
func Lock(f *os.File) error { return nil }

// Unlock 同上。
func Unlock(f *os.File) error { return nil }
