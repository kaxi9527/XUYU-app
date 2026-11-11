package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// Version 是桌面版版本号（发包时用 -ldflags 覆盖）。
var Version = "0.9.8-local"

// ShelfService 是前端调用的后端服务。只读的方法随便调；会改文件的方法都要前端先让人确认。
type ShelfService struct {
	organizer    organizePlans
	deletions    deletePlans
	versions     versionPreviews
	connections  modelConnections
	analyses     analysisConnection
	discoveries  agentDiscoveryConnection
	memories     memoryObservationConnection
	placements   placementConnection
	background   backgroundConnection
	autoOrganize autoOrganizeConnection
}

// Info 是关于本机和配置的基本信息。
type Info struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Root     string `json:"root"`
	Home     string `json:"home"`
	Config   string `json:"config"`
	HasRoot  bool   `json:"hasRoot"`
	Timezone string `json:"timezone"`
}

func loadConfig() (config.Config, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Config{}, "", err
	}
	path := config.DefaultPath(home)
	cfg, _, err := config.Load(path, home)
	return cfg, path, err
}

// Info 返回版本、系统和总文件夹位置。
func (s *ShelfService) Info() (Info, error) {
	cfg, path, err := loadConfig()
	in := Info{Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Root: cfg.Root, Config: path, Timezone: cfg.Timezone}
	in.Home, _ = os.UserHomeDir()
	if fi, e := os.Stat(cfg.Root); e == nil && fi.IsDir() {
		in.HasRoot = true
	}
	return in, err
}

func (s *ShelfService) env() (shelf.Env, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return shelf.Env{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return shelf.Env{}, err
	}
	reg, err := registry.Load(cfg)
	if err != nil && !os.IsNotExist(err) {
		return shelf.Env{}, err
	}
	return shelf.Env{Config: cfg, Home: home, Reg: reg}, nil
}

// Recent 是首页「AI 动态」：最近 days 天改过的文件，带谁写的、放对没有。
func (s *ShelfService) Recent(days, limit int) ([]shelf.Item, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	return shelf.Recent(e, days, limit), nil
}

// Search 按名字、路径、智能体、工种、项目搜文件。
func (s *ShelfService) Search(query string) ([]shelf.Item, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	return shelf.Search(e, query, 200), nil
}

// Projects 是项目总览。
func (s *ShelfService) Projects() ([]shelf.ProjectSummary, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	return shelf.Projects(e), nil
}

// Project 是项目详情（按工种分栏）。
func (s *ShelfService) Project(name string) (shelf.ProjectDetail, error) {
	e, err := s.env()
	if err != nil {
		return shelf.ProjectDetail{}, err
	}
	return shelf.Project(e, name)
}

// Reveal 在系统文件管理器里显示这个文件（只在用户点按钮时调用）。
func (s *ShelfService) Reveal(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	p := filepath.Clean(path)
	if !filepath.IsAbs(p) || !strings.HasPrefix(p, home+string(filepath.Separator)) {
		return errors.New("只能显示主目录里的文件")
	}
	if _, err := os.Stat(p); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", p)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+p)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(p))
	}
	return cmd.Start()
}
