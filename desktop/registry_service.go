package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

var regMu sync.Mutex
var regRunning bool
var regLastErr string

// RegistryStatus 是原地登记的状态。
type RegistryStatus struct {
	Exists         bool     `json:"exists"`
	Path           string   `json:"path"`
	GeneratedAt    string   `json:"generatedAt"`
	Projects       int      `json:"projects"`
	Hidden         int      `json:"hidden"`
	Running        bool     `json:"running"`
	Error          string   `json:"error,omitempty"`
	Unreadable     int      `json:"unreadable,omitempty"`
	Partial        bool     `json:"partial,omitempty"`
	PartialReasons []string `json:"partialReasons,omitempty"`
}

// RegistryStatus 返回登记表在不在、什么时候生成的、有多少项目。
func (s *ShelfService) RegistryStatus() (RegistryStatus, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return RegistryStatus{}, err
	}
	regMu.Lock()
	st := RegistryStatus{Path: filepath.Join(registry.Dir(cfg), registry.FileName), Running: regRunning, Error: regLastErr}
	regMu.Unlock()
	if r, err := registry.Load(cfg); err == nil {
		st.Exists, st.Projects, st.Hidden = true, len(r.Projects), r.Hidden
		st.Unreadable, st.Partial, st.PartialReasons = r.Unreadable, r.Partial, r.PartialReasons
		st.GeneratedAt = r.GeneratedAt.In(cfg.Location()).Format(time.RFC3339)
	}
	return st, nil
}

// RegistryRefresh 重新识别本机项目（只读扫描），把结果写进 总控/原地登记/。不搬任何文件。
func (s *ShelfService) RegistryRefresh() (RegistryStatus, error) {
	regMu.Lock()
	if regRunning {
		regMu.Unlock()
		return s.RegistryStatus()
	}
	regRunning = true
	regMu.Unlock()
	err := refreshRegistry()
	regMu.Lock()
	regRunning = false
	regLastErr = ""
	if err != nil {
		regLastErr = err.Error()
	}
	regMu.Unlock()
	st, e2 := s.RegistryStatus()
	if err != nil {
		return st, err
	}
	return st, e2
}

func refreshRegistry() error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	opt, err := registry.ScanOptions(cfg, home, nil, false, false)
	if err != nil {
		return err
	}
	reg, err := registry.Build(cfg, home, opt)
	if err != nil {
		return err
	}
	_, err = registry.Save(cfg, reg, home)
	return err
}

// A foreground scan starts from the UI after the access explanation;
// an explicitly enabled background launch can keep an existing index fresh.
func autoRefreshRegistry(s *ShelfService) {
	cfg, _, err := loadConfig()
	if err != nil {
		return
	}
	r, err := registry.Load(cfg)
	if err != nil || time.Since(r.GeneratedAt) < 12*time.Hour {
		return
	}
	go s.RegistryRefresh()
}

// PlaceProject 是原地项目详情。
func (s *ShelfService) PlaceProject(id string) (shelf.PlaceDetail, error) {
	e, err := s.env()
	if err != nil {
		return shelf.PlaceDetail{}, err
	}
	return shelf.PlaceProject(e, id)
}

// Settings 是设置页显示的内容。
type Settings struct {
	ConfigPath   string   `json:"configPath"`
	ConfigExists bool     `json:"configExists"`
	Root         string   `json:"root"`
	ScanPaths    []string `json:"scanPaths"`
	Defaults     []string `json:"defaults"`
	HomeDepth    int      `json:"homeDepth"`
	RegistryDir  string   `json:"registryDir"`
}

// Settings 读设置。没有配置文件时显示默认值。
func (s *ShelfService) Settings() (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}
	path := config.DefaultPath(home)
	cfg, found, err := config.Load(path, home)
	st := Settings{ConfigPath: path, ConfigExists: found, Root: cfg.Root, ScanPaths: cfg.ScanPaths,
		Defaults: config.Default(home).ScanPaths, HomeDepth: cfg.HomeDepth, RegistryDir: registry.Dir(cfg)}
	return st, err
}

// SaveSettings 保存扫描范围。只改 scan_paths 和 home_depth 两项，其他配置原样保留；
// 原配置文件先另存一份 .bak-时间 再写。
func (s *ShelfService) SaveSettings(paths []string, homeDepth int) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}
	var clean []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs := filepath.Clean(config.Expand(p, home))
		if !filepath.IsAbs(abs) {
			return Settings{}, fmt.Errorf("「%s」不是完整路径（用 / 或 ~/ 开头）", p)
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return Settings{}, fmt.Errorf("「%s」不存在或不是文件夹", p)
		}
		if !seen[abs] {
			seen[abs] = true
			clean = append(clean, abs)
		}
	}
	if len(clean) == 0 {
		return Settings{}, errors.New("至少要留一个扫描位置")
	}
	if homeDepth < 0 || homeDepth > 3 {
		return Settings{}, errors.New("主目录层数只能是 0 到 3")
	}
	path := config.DefaultPath(home)
	var show []string
	for _, p := range clean {
		show = append(show, config.RelHome(p, home))
	}
	if _, err := config.PatchJSONFile(path, map[string]any{"scan_paths": show, "home_depth": homeDepth}); err != nil {
		return Settings{}, err
	}
	if _, _, err := config.Load(path, home); err != nil {
		return Settings{}, fmt.Errorf("保存后读回出错：%w", err)
	}
	return s.Settings()
}

// SetRoot 改总文件夹路径。目录必须已经存在；不能和旧路径互相嵌套。
// 只改 root 一项，其他配置原样保留；先 bak 再原子写。
func (s *ShelfService) SetRoot(newRoot string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}
	path := config.DefaultPath(home)
	cfg, _, err := config.Load(path, home)
	if err != nil {
		return Settings{}, err
	}
	abs, err := config.CheckNewRoot(newRoot, cfg.Root, home)
	if err != nil {
		return Settings{}, err
	}
	if abs == filepath.Clean(cfg.Root) {
		return s.Settings() // 没变
	}
	show := config.RelHome(abs, home)
	if _, err := config.PatchJSONFile(path, map[string]any{"root": show}); err != nil {
		return Settings{}, err
	}
	if _, _, err := config.Load(path, home); err != nil {
		return Settings{}, fmt.Errorf("保存后读回出错：%w", err)
	}
	return s.Settings()
}
