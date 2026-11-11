package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/trash"
)

// PreviewDelete 预览删除（不挪文件）。允许总文件夹内，以及主目录下已展示的路径。
func (s *ShelfService) PreviewDelete(paths []string) (DeletePreview, error) {
	opt, err := s.trashOpt(paths, true)
	if err != nil {
		return DeletePreview{}, err
	}
	return s.deletions.preview(opt)
}

// ApplyDelete 执行删除（挪进废纸篓或删除暂存）。前端必须先 Preview 并让用户确认。
func (s *ShelfService) ApplyDelete(paths []string) (trash.Summary, error) {
	return s.ApplyDeleteConfirmed(paths, false)
}

func hiddenPaths(paths []string) bool {
	for _, p := range paths {
		for _, part := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") && part != "." && part != ".." {
				return true
			}
		}
	}
	return false
}

// Legacy path-only execution cannot prove which preview the user confirmed.
func (s *ShelfService) ApplyDeleteConfirmed(paths []string, confirmedHidden bool) (trash.Summary, error) {
	return trash.Summary{}, fmt.Errorf("请先预览删除，再使用本次预览确认；路径或确认标志不能单独授权删除")
}

func (s *ShelfService) ApplyDeletePlan(token string, confirmedHidden bool) (trash.Summary, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return trash.Summary{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return trash.Summary{}, err
	}
	return s.deletions.apply(token, confirmedHidden, cfg, home)
}

// UndoDelete 按撤销清单把文件搬回原处。
func (s *ShelfService) UndoDelete(undoPath string) (trash.UndoResult, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return trash.UndoResult{}, err
	}
	ur, err := trash.Undo(cfg, undoPath)
	if err != nil {
		return trash.UndoResult{}, err
	}
	return *ur, nil
}

func (s *ShelfService) trashOpt(paths []string, dry bool) (trash.Options, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return trash.Options{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return trash.Options{}, err
	}
	if len(paths) == 0 {
		return trash.Options{}, fmt.Errorf("没有要删除的路径")
	}
	var clean []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		if !filepath.IsAbs(p) {
			return trash.Options{}, fmt.Errorf("路径必须是绝对路径：%s", p)
		}
		seen[p] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return trash.Options{}, fmt.Errorf("没有要删除的路径")
	}
	return trash.Options{Config: cfg, Home: home, Paths: clean, DryRun: dry, AllowOutsideRoot: true}, nil
}
