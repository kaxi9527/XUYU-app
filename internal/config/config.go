// Package config 读取 WorkShelf 的配置文件（JSON），没有配置文件时用默认值。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// 内置时区数据库，保证在没有系统时区数据的机器上也能用 America/Los_Angeles。
	_ "time/tzdata"
)

// Names 是 ~/Work 里用到的目录名和文件名。默认中文，可以在配置里改。
type Names struct {
	Control     string `json:"control"`      // 总控目录，~/Work 顶层唯一的非项目目录
	Overview    string `json:"overview"`     // 总控里的总说明
	IndexShards string `json:"index_shards"` // 资产索引分片目录
	IndexFile   string `json:"index_file"`   // 分片文件名前缀，例如 资产索引 -> 资产索引.<智能体>.csv
	Logs        string `json:"logs"`         // 整理日志
	Pending     string `json:"pending"`      // 待分类清单
	Archive     string `json:"archive"`      // 归档
	Readme      string `json:"readme"`       // 每个项目的说明文件
	Tasks       string `json:"tasks"`        // 任务（过程文件）
	Outputs     string `json:"outputs"`      // 成品（给人看的定稿）
	Materials   string `json:"materials"`    // 资料
	Sensitive   string `json:"sensitive"`    // 资料下的敏感子目录
	Code        string `json:"code"`         // 代码
	Loose       string `json:"loose"`        // 资料下放散落文件的子目录
	Activity    string `json:"activity"`     // 总控里的活动记录（谁写了哪个文件）
	AgentBackup string `json:"agent_backup"` // 总控里接入 AI 时的规则文件备份
	Registry    string `json:"registry"`     // 总控里的「原地登记」（还没搬进总文件夹的项目）
}

// ZhNames 是默认的中文目录名。
func ZhNames() Names {
	return Names{
		Control:     "_总控",
		Overview:    "00-总说明.md",
		IndexShards: "资产索引分片",
		IndexFile:   "资产索引",
		Logs:        "整理日志",
		Pending:     "待分类清单.md",
		Archive:     "归档",
		Readme:      "00-说明.md",
		Tasks:       "任务",
		Outputs:     "成品",
		Materials:   "资料",
		Sensitive:   "敏感",
		Code:        "代码",
		Loose:       "散落文件",
		Activity:    "活动记录",
		AgentBackup: "接入备份",
		Registry:    "原地登记",
	}
}

// EnNames 是可选的英文目录名（配置里 "names_preset": "en"）。
func EnNames() Names {
	return Names{
		Control:     "_control",
		Overview:    "00-README.md",
		IndexShards: "asset-index-shards",
		IndexFile:   "asset-index",
		Logs:        "logs",
		Pending:     "unsorted.md",
		Archive:     "archive",
		Readme:      "00-README.md",
		Tasks:       "tasks",
		Outputs:     "outputs",
		Materials:   "materials",
		Sensitive:   "sensitive",
		Code:        "code",
		Loose:       "loose-files",
		Activity:    "activity",
		AgentBackup: "agent-backups",
		Registry:    "in-place",
	}
}

// Config 是全部配置。
type Config struct {
	Root           string   `json:"root"`            // 总文件夹，默认 ~/Work
	Timezone       string   `json:"timezone"`        // 日期用的时区，默认 America/Los_Angeles
	ScanPaths      []string `json:"scan_paths"`      // 默认扫描范围
	ScanVolumes    bool     `json:"scan_volumes"`    // 是否默认扫 /Volumes 下的外置盘
	VolumesDir     string   `json:"volumes_dir"`     // 外置盘挂载目录，默认 /Volumes
	HomeDepth      int      `json:"home_depth"`      // 主目录本身只往下扫几层
	MaxDepth       int      `json:"max_depth"`       // 其他扫描根往下扫几层
	MaxDirs        int      `json:"max_dirs"`        // 最多看多少个文件夹
	MaxFiles       int      `json:"max_files"`       // 最多记录多少个散落文件
	TimeoutSeconds int      `json:"timeout_seconds"` // 扫描最长时间
	ActiveDays     int      `json:"active_days"`     // 多少天内改过算「活跃」
	ExtraSkip      []string `json:"extra_skip"`      // 额外跳过的目录名或绝对路径
	GitStatus      bool     `json:"git_status"`      // 是否用 git status（只读）查未提交改动
	NamesPreset    string   `json:"names_preset"`    // zh（默认）或 en
	Names          Names    `json:"names"`
}

// Default 返回默认配置。home 是用户主目录。
func Default(home string) Config {
	return Config{
		Root:     filepath.Join(home, "Work"),
		Timezone: "America/Los_Angeles",
		ScanPaths: []string{
			filepath.Join(home, "Desktop"),
			filepath.Join(home, "Downloads"),
			filepath.Join(home, "Documents"),
			filepath.Join(home, "Projects"),
			home,
		},
		VolumesDir:     "/Volumes",
		HomeDepth:      1,
		MaxDepth:       6,
		MaxDirs:        40000,
		MaxFiles:       20000,
		TimeoutSeconds: 60,
		ActiveDays:     14,
		GitStatus:      true,
		NamesPreset:    "zh",
		Names:          ZhNames(),
	}
}

// DefaultPath 返回默认配置文件路径：$WORKSHELF_CONFIG，否则 ~/.config/workshelf/config.json。
func DefaultPath(home string) string {
	if p := os.Getenv("WORKSHELF_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(home, ".config", "workshelf", "config.json")
}

// Load 读取配置。path 为空时用 DefaultPath；文件不存在就返回默认配置（found=false）。
func Load(path, home string) (cfg Config, found bool, err error) {
	if path == "" {
		path = DefaultPath(home)
	}
	cfg = Default(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, false, err
	}
	// 先读预设，决定目录名的默认值，再整体覆盖。
	var probe struct {
		NamesPreset string `json:"names_preset"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return cfg, true, fmt.Errorf("配置文件 %s 不是合法 JSON：%w", path, err)
	}
	if strings.EqualFold(probe.NamesPreset, "en") {
		cfg.Names = EnNames()
		cfg.NamesPreset = "en"
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, true, fmt.Errorf("配置文件 %s 读取失败：%w", path, err)
	}
	cfg.Root = Expand(cfg.Root, home)
	cfg.VolumesDir = Expand(cfg.VolumesDir, home)
	for i, p := range cfg.ScanPaths {
		cfg.ScanPaths[i] = Expand(p, home)
	}
	for i, p := range cfg.ExtraSkip {
		cfg.ExtraSkip[i] = Expand(p, home)
	}
	fillEmptyNames(&cfg.Names, cfg.NamesPreset)
	return cfg, true, cfg.Validate()
}

func fillEmptyNames(n *Names, preset string) {
	def := ZhNames()
	if strings.EqualFold(preset, "en") {
		def = EnNames()
	}
	pairs := []struct {
		v *string
		d string
	}{
		{&n.Control, def.Control}, {&n.Overview, def.Overview}, {&n.IndexShards, def.IndexShards},
		{&n.IndexFile, def.IndexFile}, {&n.Logs, def.Logs}, {&n.Pending, def.Pending},
		{&n.Archive, def.Archive}, {&n.Readme, def.Readme}, {&n.Tasks, def.Tasks},
		{&n.Outputs, def.Outputs}, {&n.Materials, def.Materials}, {&n.Sensitive, def.Sensitive},
		{&n.Code, def.Code}, {&n.Loose, def.Loose}, {&n.Activity, def.Activity}, {&n.AgentBackup, def.AgentBackup}, {&n.Registry, def.Registry},
	}
	for _, p := range pairs {
		if strings.TrimSpace(*p.v) == "" {
			*p.v = p.d
		}
	}
}

// Validate 检查配置是否可用。
func (c Config) Validate() error {
	if c.Root == "" || !filepath.IsAbs(c.Root) {
		return fmt.Errorf("root 必须是绝对路径（可以用 ~/ 开头），现在是 %q", c.Root)
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("时区 %q 不认识：%w", c.Timezone, err)
	}
	for _, n := range []string{c.Names.Control, c.Names.Tasks, c.Names.Outputs, c.Names.Materials, c.Names.Code, c.Names.Readme} {
		if strings.ContainsAny(n, `/\`) || n == "." || n == ".." {
			return fmt.Errorf("目录名 %q 不能包含斜杠", n)
		}
	}
	return nil
}

// Location 返回配置的时区。
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc, _ = time.LoadLocation("America/Los_Angeles")
	}
	return loc
}

// Expand 把开头的 ~ 换成主目录。
func Expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
