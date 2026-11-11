//go:build !darwin || !cgo

package organize

const nativeUnicodeNormalization = false

// Preserve distinct names on platforms without a verified filesystem comparer.
// Exclusive execution still refuses any collision the filesystem reports.
func normalizeFilename(value string) string      { return value }
func filesystemCaseInsensitive(path string) bool { return false }
