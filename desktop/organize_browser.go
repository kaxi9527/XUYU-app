package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	filehome "github.com/kaxi9527/WorkShelf/internal/home"
)

type BrowseEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Hidden    bool   `json:"hidden"`
	Link      bool   `json:"link"`
	Size      int64  `json:"size"`
}
type BrowseResult struct {
	Path        string        `json:"path"`
	Parent      string        `json:"parent"`
	Entries     []BrowseEntry `json:"entries"`
	HiddenCount int           `json:"hiddenCount"`
	Truncated   bool          `json:"truncated"`
}

// Listing exposes names and metadata only, including dotfiles on explicit request.
func browseDirectory(base, path string, showHidden bool) (BrowseResult, error) {
	var out BrowseResult
	if err := filehome.CheckPath(base, path); err != nil {
		return out, err
	}
	dir, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1001)
	if err != nil && err != io.EOF {
		return out, err
	}
	out = BrowseResult{Path: path, Entries: []BrowseEntry{}}
	if path != base {
		out.Parent = filepath.Dir(path)
	}
	if len(entries) > 1000 {
		out.Truncated = true
		entries = entries[:1000]
	}
	for _, en := range entries {
		hidden := strings.HasPrefix(en.Name(), ".")
		if hidden {
			out.HiddenCount++
			if !showHidden {
				continue
			}
		}
		item := BrowseEntry{Name: en.Name(), Path: filepath.Join(path, en.Name()), Directory: en.IsDir(), Hidden: hidden, Link: en.Type()&os.ModeSymlink != 0}
		if fi, e := en.Info(); e == nil {
			item.Size = fi.Size()
		}
		out.Entries = append(out.Entries, item)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Directory != b.Directory {
			return a.Directory
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}
func (s *ShelfService) OrganizeBrowse(path string, hidden bool) (BrowseResult, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return BrowseResult{}, err
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return BrowseResult{}, err
	}
	if path == "" {
		path = base
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return BrowseResult{}, errors.New("请输入绝对路径")
	}
	if rel, err := filepath.Rel(cfg.Root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		base = cfg.Root
	}
	return browseDirectory(base, path, hidden)
}
