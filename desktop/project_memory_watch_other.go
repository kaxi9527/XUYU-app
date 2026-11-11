//go:build !darwin || !cgo

package main

import "github.com/kaxi9527/WorkShelf/internal/projectmemory"

func newMemoryNativeWatch(scope projectmemory.Scope, exclude string) (memoryNativeWatch, error) {
	return nil, errMemoryWatchUnavailable
}
