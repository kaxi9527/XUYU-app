//go:build !unix

package plan

import "os"

func deviceOf(fi os.FileInfo) uint64 { return 0 }
