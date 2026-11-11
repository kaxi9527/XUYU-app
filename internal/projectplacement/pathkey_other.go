//go:build !darwin || !cgo

package projectplacement

import "strings"

// Linux names need no Unicode normalization. Case folding is conservative;
// actual membership and all filesystem operations retain the original name.
func pathKey(path string) string { return strings.ToLower(path) }
