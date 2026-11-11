package autoorganize

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Capture only the new project's original template. Later user changes keep
// the directory intact when an automatic move is reversed.
func scaffold(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			result[rel] = "dir"
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("项目目录包含非普通文件")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return e
		}
		result[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return result, err
}
func removeScaffold(root string, original map[string]string) error {
	current, err := scaffold(root)
	if err != nil {
		return err
	}
	if len(original) == 0 || len(current) != len(original) {
		return errors.New("项目内有新增或缺失内容")
	}
	for path, digest := range original {
		if current[path] != digest {
			return errors.New("项目内内容已变化")
		}
	}
	paths := make([]string, 0, len(original))
	for path := range original {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, path := range paths {
		if err = os.Remove(filepath.Join(root, path)); err != nil {
			return err
		}
	}
	return os.Remove(root)
}
