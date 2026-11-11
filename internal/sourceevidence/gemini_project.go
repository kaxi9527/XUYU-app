package sourceevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Gemini identifies its project root by SHA-256 of the path. Match only
// directories visible within the user's selected source roots; do not attempt
// to reverse an unknown hash or inspect conversation text.
func GeminiProjectDirectories(roots []string) map[string]string {
	paths := map[string]string{}
	add := func(path string) {
		sum := sha256.Sum256([]byte(path))
		paths[hex.EncodeToString(sum[:])] = path
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		add(root)
		children, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			path := filepath.Join(root, child.Name())
			add(path)
			switch strings.ToLower(child.Name()) {
			case "projects", "desktop", "downloads", "documents", "work", "workspace":
				entries, err := os.ReadDir(path)
				if err != nil {
					continue
				}
				for _, entry := range entries {
					if entry.IsDir() {
						add(filepath.Join(path, entry.Name()))
					}
				}
			}
		}
	}
	return paths
}
