//go:build !unix

package home

import "os"

func deviceOf(fi os.FileInfo) uint64 { return 0 }
