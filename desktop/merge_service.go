package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/merge"
)

// MergeSuggest 返回「可能是同一项目」的合并建议（只读）。
func (s *ShelfService) MergeSuggest() ([]merge.Candidate, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	return merge.Suggest(e), nil
}

// PreviewMerge 预览把 sources 合并进目标项目名（总文件夹下）。
func (s *ShelfService) PreviewMerge(target string, sources []string) (home.Summary, error) {
	sum, err := s.mergeRun(target, sources, true)
	if err != nil {
		return home.Summary{}, err
	}
	return *sum, nil
}

// ApplyMerge 执行合并（前端必须先 Preview 并确认）。
func (s *ShelfService) ApplyMerge(target string, sources []string) (home.Summary, error) {
	sum, err := s.mergeRun(target, sources, false)
	if err != nil {
		return home.Summary{}, err
	}
	return *sum, nil
}

func (s *ShelfService) mergeRun(target string, sources []string, dry bool) (*home.Summary, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("要给目标项目名")
	}
	var clean []string
	seen := map[string]bool{}
	for _, p := range sources {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("路径必须是绝对路径：%s", p)
		}
		seen[p] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("没有要合并的路径")
	}
	opt := merge.Options{Config: cfg, Target: target, Sources: clean, DryRun: dry}
	if dry {
		return merge.Preview(opt)
	}
	return merge.Apply(opt)
}
