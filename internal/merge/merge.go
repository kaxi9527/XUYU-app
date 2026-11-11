// Package merge 给出项目合并建议，并把多个项目/路径归到同一个总文件夹项目下。
// 建议只读；执行同盘搬走可撤销，跨盘复制保留原件；不覆盖（重名 -02）；不用 os.Remove。
package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

type hit struct {
	name, path, source, id, origin string
}

// Candidate 是一条「可能是同一项目」的建议。
type Candidate struct {
	Key     string   `json:"key"`     // CopyKey
	Name    string   `json:"name"`    // 建议的目标项目名（展示用）
	Paths   []string `json:"paths"`   // 参与合并的路径
	Sources []string `json:"sources"` // 总文件夹 / 原地
	IDs     []string `json:"ids"`     // 原地项目 ID（可空）
	Reasons []string `json:"reasons"` // 为什么觉得是同一项目
	Members int      `json:"members"` // 路径数
	Level   string   `json:"level"`   // recommended / possible / conflict; never execution authority
}

// Suggest 根据总文件夹项目 + 原地登记，按名字去后缀（CopyKey）找出可能该合并的组。
// 只建议，不执行。一组至少 2 个不同路径才出。
func Suggest(e shelf.Env) []Candidate {
	e.Config.Validate()
	list := shelf.Projects(e)
	by := map[string][]hit{}
	for _, p := range list {
		if p.Linked {
			continue
		}
		key := scan.CopyKey(p.Name)
		if key == "" {
			continue
		}
		by[key] = append(by[key], hit{name: p.Name, path: p.Path, source: p.Source, id: p.ID, origin: p.Origin})
	}
	// Evidence work is globally bounded. A budget limit weakens a recommendation,
	// never upgrades incomplete checks into certainty.
	budget := newEvidenceBudget(e.Home, e.Config.Root)
	var out []Candidate
	keys := make([]string, 0, len(by))
	for key := range by {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hs := by[key]
		// 去重路径
		seen := map[string]bool{}
		var uniq []hit
		for _, h := range hs {
			p := filepath.Clean(h.path)
			if seen[p] {
				continue
			}
			seen[p] = true
			uniq = append(uniq, h)
		}
		if len(uniq) < 2 {
			continue
		}
		sort.Slice(uniq, func(i, j int) bool { return uniq[i].path < uniq[j].path })
		c := Candidate{Key: key, Name: preferName(uniq), Members: len(uniq)}
		c.Level, c.Reasons = recommend(uniq, budget)
		reasons := map[string]bool{}
		for _, reason := range c.Reasons {
			reasons[reason] = true
		}
		hasRoot, hasPlace := false, false
		for _, h := range uniq {
			c.Paths = append(c.Paths, h.path)
			c.Sources = append(c.Sources, h.source)
			if h.id != "" {
				c.IDs = append(c.IDs, h.id)
			}
			if h.source == shelf.SourceRoot {
				hasRoot = true
			}
			if h.source == shelf.SourceInPlace {
				hasPlace = true
			}
		}
		if hasRoot && hasPlace {
			reasons["一个在总文件夹、一个还在原处"] = true
		}
		c.Reasons = nil // Registration prose is not independent cross-project evidence.
		for r := range reasons {
			c.Reasons = append(c.Reasons, r)
		}
		sort.Strings(c.Reasons)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Members != out[j].Members {
			return out[i].Members > out[j].Members
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func preferName(hs []hit) string {
	// 优先总文件夹里的名字，其次最短的
	var rootName string
	best := hs[0].name
	for _, h := range hs {
		if h.source == shelf.SourceRoot && rootName == "" {
			rootName = h.name
		}
		if len(h.name) < len(best) {
			best = h.name
		}
	}
	if rootName != "" {
		return rootName
	}
	return scan.StripVersion(best)
}

// Options 控制一次合并。
type Options struct {
	Config      config.Config
	Target      string   // 目标项目名（总文件夹下）
	Sources     []string // 要并进来的路径（文件夹或文件）
	DryRun      bool
	Now         time.Time
	KeepPrimary bool // 若目标不存在：把第一个源目录整项目搬成目标；否则先建骨架再往里收
}

// Preview / Apply 复用 home：目标项目下，整目录进「资料/散落文件/<名>/」，文件进散落文件。
func Preview(opt Options) (*home.Summary, error) {
	jobs, err := buildJobs(opt)
	if err != nil {
		return nil, err
	}
	return home.Preview(home.Options{Config: opt.Config, Jobs: jobs, DryRun: true, Now: opt.Now})
}

func Apply(opt Options) (*home.Summary, error) {
	if opt.DryRun {
		return Preview(opt)
	}
	jobs, err := buildJobs(opt)
	if err != nil {
		return nil, err
	}
	return home.Apply(home.Options{Config: opt.Config, Jobs: jobs, DryRun: false, Now: opt.Now, Agent: "workshelf-merge"})
}

func buildJobs(opt Options) ([]home.Job, error) {
	if err := put.CheckName("项目名", opt.Target); err != nil {
		return nil, err
	}
	n := opt.Config.Names
	if strings.EqualFold(opt.Target, n.Control) {
		return nil, fmt.Errorf("%s 是总控，不能当合并目标", n.Control)
	}
	if len(opt.Sources) == 0 {
		return nil, fmt.Errorf("没有要合并的路径")
	}
	root := filepath.Clean(opt.Config.Root)
	targetDir := filepath.Join(root, opt.Target)
	var jobs []home.Job
	sources := uniqueAbs(opt.Sources)

	// 目标不存在：第一个目录源整项目归位成目标
	needWhole := false
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		needWhole = true
	}
	for _, src := range sources {
		if filepath.Clean(src) == targetDir {
			continue // 已经是目标
		}
		// 禁止把总控并进去
		control := filepath.Join(root, n.Control)
		if src == control || strings.HasPrefix(src, control+string(filepath.Separator)) {
			return nil, fmt.Errorf("不能合并总控：%s", src)
		}
		if src == root {
			return nil, fmt.Errorf("不能合并总文件夹本身")
		}
		fi, err := os.Lstat(src)
		if err != nil {
			return nil, fmt.Errorf("源不存在：%s", src)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if needWhole && fi.IsDir() {
			jobs = append(jobs, home.Job{Source: src, Project: opt.Target, Whole: true})
			needWhole = false
			continue
		}
		if fi.IsDir() || fi.Mode().IsRegular() {
			jobs = append(jobs, home.Job{Source: src, Project: opt.Target, Area: home.AreaLoose})
		}
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("没有可合并的条目（是不是都已经在目标里了？）")
	}
	return jobs, nil
}

func uniqueAbs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || !filepath.IsAbs(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// SuggestFromRegistry 是 Suggest 的薄封装，方便桌面只传 cfg+reg。
func SuggestFromRegistry(cfg config.Config, homeDir string, reg *registry.Registry) []Candidate {
	return Suggest(shelf.Env{Config: cfg, Home: homeDir, Reg: reg})
}
