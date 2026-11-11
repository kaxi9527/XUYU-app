package appdir

import (
	"os"
	"path/filepath"
	"strings"
)

// UXQARoot confines this source-integrated build to the owning product's state.
func UXQARoot() string {
	p := os.Getenv("WORKSHELF_MODEL_HOME")
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return ""
}

func UXQAOwn(path string) bool {
	root := UXQARoot()
	if root == "" {
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absolute = uxqaCanonical(absolute)
	root = uxqaCanonical(root)
	rel, err := filepath.Rel(root, absolute)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve an existing ancestor too: macOS /var is a symlink, while a new
// account's child directory may not exist yet. Never accept a foreign symlink.
func uxqaCanonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(uxqaCanonical(parent), filepath.Base(path))
}
