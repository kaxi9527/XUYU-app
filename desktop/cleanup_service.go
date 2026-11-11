package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/cleanup"
	"github.com/kaxi9527/WorkShelf/internal/registry"
)

// CleanupSuggest 推荐清理：启发式，不确定的进「待你定」且不预勾。
func (s *ShelfService) CleanupSuggest() ([]cleanup.Suggestion, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var reg *registry.Registry
	if r, err := registry.Load(cfg); err == nil {
		reg = r
	}
	return cleanup.Suggest(cleanup.Options{Config: cfg, Home: home, Registry: reg, Limit: 100})
}

// CleanupProbeProject 对某个项目路径做轻量「可能用不到」探测（不宣称死代码分析）。
func (s *ShelfService) CleanupProbeProject(projectPath string) ([]cleanup.Suggestion, error) {
	projectPath = filepath.Clean(projectPath)
	if !filepath.IsAbs(projectPath) {
		return nil, fmt.Errorf("路径必须是绝对路径")
	}
	return cleanup.ProbeProjectLight(projectPath, 40)
}
