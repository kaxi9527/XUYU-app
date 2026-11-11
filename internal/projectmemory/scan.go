package projectmemory

import (
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"path/filepath"
	"runtime"
	"strings"
)

type ScanOptions struct {
	// Exclude is supplied by the trusted host, never by model-produced facts.
	Exclude      []string
	MaxFiles     int
	MaxHashBytes int64
}

func platformPath(path string) string {
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/var", "/tmp"} {
			if within(alias, path) {
				return "/private" + path
			}
		}
	}
	return path
}

func metadataOnlyPath(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") || scan.IsSensitiveName(part) {
			return true
		}
		switch strings.ToLower(part) {
		case "node_modules", "venv", "vendor", "__pycache__", "library":
			return true
		}
	}
	return false
}
