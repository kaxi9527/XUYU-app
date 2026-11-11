package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// InferWorkTypes 认出项目所属工种（说明已标 + 启发式推断）。拿不准的标「待你定」。
func (s *ShelfService) InferWorkTypes(project string) ([]allocate.Inferred, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("要给项目名")
	}
	if err := put.CheckName("项目名", project); err != nil {
		return nil, err
	}
	dir := filepath.Join(cfg.Root, project)
	return allocate.InferProject(allocate.InferOptions{
		ProjectDir: dir, ReadmeName: cfg.Names.Readme,
	}), nil
}

// ConfirmWorkTypes 用户确认后，把工种追加进项目 00-说明.md（只追加不改原文）。
func (s *ShelfService) ConfirmWorkTypes(project string, labels []string) ([]string, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	project = strings.TrimSpace(project)
	if err := put.CheckName("项目名", project); err != nil {
		return nil, err
	}
	readme := filepath.Join(cfg.Root, project, cfg.Names.Readme)
	var added []string
	for _, lab := range labels {
		lab = strings.TrimSpace(lab)
		if lab == "" {
			continue
		}
		if err := put.EnsureProjectWorkType(readme, lab); err != nil {
			return added, err
		}
		added = append(added, lab)
	}
	return shelf.ProjectWorkTypes(readme), nil
}

// SuggestAllocate 按工种给出项目内文件分配预览（不搬）。
func (s *ShelfService) SuggestAllocate(project string) ([]allocate.Suggestion, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	project = strings.TrimSpace(project)
	if err := put.CheckName("项目名", project); err != nil {
		return nil, err
	}
	return allocate.SuggestProject(allocate.SuggestOptions{
		Config: cfg, Project: project,
	})
}

// PreviewAllocate 对勾选的分配建议做归位预览（复用 home.Preview）。
func (s *ShelfService) PreviewAllocate(jobs []HomeJob) (home.Summary, error) {
	return s.PreviewHome(jobs)
}

// ApplyAllocate 执行勾选的分配建议（复用 home.Apply）。
func (s *ShelfService) ApplyAllocate(jobs []HomeJob) (home.Summary, error) {
	return s.ApplyHome(jobs)
}
