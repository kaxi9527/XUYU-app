//go:build !unix

package scan

import "os"

func deviceOf(fi os.FileInfo) uint64 { return 0 }
