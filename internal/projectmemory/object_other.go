//go:build !darwin

package projectmemory

import "os"

// inode alone can be reused. Until birth identity is available, do not infer moves.
func objectKey(os.FileInfo) string { return "" }
