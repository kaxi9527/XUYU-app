// Package plan 根据扫描结果生成整理计划（Markdown + JSON）。只出计划，不执行任何移动。
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// 动作
const (
	ActionMove    = "移动"
	ActionKeep    = "只登记不动"
	ActionPending = "待分类（原地不动）"
)

// Item 是计划里的一行：一个东西从哪到哪。
type Item struct {
	Source      string   `json:"source"`
	Target      string   `json:"target,omitempty"`
	Project     string   `json:"project,omitempty"`
	Kind        string   `json:"kind"`
	Action      string   `json:"action"`
	Reason      string   `json:"reason"`
	Risks       []string `json:"risks,omitempty"`
	CrossVolume bool     `json:"cross_volume,omitempty"`
}

// ProjectPlan 是一个项目的计划。
type ProjectPlan struct {
	Name   string   `json:"name"`
	Target string   `json:"target"`
	Create []string `json:"create"` // 要新建的骨架（目录以 / 结尾）
	Items  []Item   `json:"items"`
}

// Plan 是整份计划。
type Plan struct {
	Version         int           `json:"version"`
	GeneratedAt     string        `json:"generated_at"`
	Timezone        string        `json:"timezone"`
	Root            string        `json:"root"`
	ScanGeneratedAt string        `json:"scan_generated_at"`
	ScanPartial     bool          `json:"scan_partial"`
	PartialReasons  []string      `json:"partial_reasons,omitempty"`
	Control         []string      `json:"control_create"`
	Projects        []ProjectPlan `json:"projects"`
	Pending         []Item        `json:"pending"`
	SensitiveCount  int           `json:"sensitive_skipped"`
	Counts          Counts        `json:"counts"`
	Notes           []string      `json:"notes"`
}

// Counts 汇总数字。
type Counts struct {
	Projects    int `json:"projects"`
	Move        int `json:"move"`
	Keep        int `json:"keep"`
	Pending     int `json:"pending"`
	Dirty       int `json:"dirty"`
	Worktrees   int `json:"worktrees"`
	CrossVolume int `json:"cross_volume"`
}

// Options 控制计划生成。
type Options struct {
	Config config.Config
	Now    time.Time
	// RootDevice 是 ~/Work（或它最近的已存在上级目录）所在设备号；0 表示自动取。
	RootDevice uint64
}

// Build 由扫描结果生成计划。不碰文件系统里的任何东西（只 Stat 目标是否已存在）。
func Build(res *scan.Result, opt Options) *Plan {
	cfg := opt.Config
	n := cfg.Names
	loc := cfg.Location()
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	rootDev := opt.RootDevice
	if rootDev == 0 {
		rootDev = nearestDevice(cfg.Root)
	}
	activeCut := now.AddDate(0, 0, -cfg.ActiveDays)

	p := &Plan{
		Version:         1,
		GeneratedAt:     now.In(loc).Format(time.RFC3339),
		Timezone:        cfg.Timezone,
		Root:            cfg.Root,
		ScanGeneratedAt: res.GeneratedAt.In(loc).Format(time.RFC3339),
		ScanPartial:     res.Partial,
		PartialReasons:  res.PartialReasons,
		SensitiveCount:  res.SensitiveSkipped,
	}
	ctl := filepath.Join(cfg.Root, n.Control)
	p.Control = []string{
		dir(cfg.Root), dir(ctl), filepath.Join(ctl, n.Overview), dir(filepath.Join(ctl, n.IndexShards)),
		dir(filepath.Join(ctl, n.Logs)), filepath.Join(ctl, n.Pending), dir(filepath.Join(ctl, n.Archive)),
	}

	// 项目名去重：大小写不敏感；和已存在于 ~/Work 的项目同名时直接并进去（不覆盖，目标冲突会标出来）。
	used := map[string]bool{strings.ToLower(n.Control): true}
	projByGroup := map[int]*ProjectPlan{}
	for _, g := range res.Groups {
		name := safeName(g.Name)
		base := name
		for i := 2; used[strings.ToLower(name)]; i++ {
			name = fmt.Sprintf("%s-%02d", base, i)
		}
		used[strings.ToLower(name)] = true
		target := filepath.Join(cfg.Root, name)
		pp := &ProjectPlan{Name: name, Target: target}
		pp.Create = []string{dir(target), filepath.Join(target, n.Readme), dir(filepath.Join(target, n.Tasks)), dir(filepath.Join(target, n.Outputs)), dir(filepath.Join(target, n.Materials))}
		projByGroup[g.ID] = pp
	}

	projects := append([]scan.Project(nil), res.Projects...)
	sort.Slice(projects, func(a, b int) bool { return projects[a].Path < projects[b].Path })
	hasCode := map[int]bool{}
	takenTargets := map[string]bool{}
	uniqueTarget := func(t string, isFile bool) string {
		out := t
		ext := ""
		stem := t
		if fi := filepath.Ext(t); isFile && fi != "" && fi != t {
			ext = fi
			stem = strings.TrimSuffix(t, fi)
		}
		for i := 2; takenTargets[strings.ToLower(out)]; i++ {
			out = fmt.Sprintf("%s-%02d%s", stem, i, ext)
		}
		takenTargets[strings.ToLower(out)] = true
		return out
	}

	for _, pr := range projects {
		pp := projByGroup[pr.Group]
		if pp == nil {
			continue
		}
		it := Item{Source: pr.Path, Project: pp.Name, Kind: kindLabel(pr)}
		switch pr.Category {
		case "images", "media":
			date := pr.ModTime.In(loc).Format("2006-01-02")
			it.Target = uniqueTarget(filepath.Join(pp.Target, n.Tasks, date+"-"+safeName(pr.Name)), false)
			it.Reason = "认出是" + map[string]string{"images": "图片组（≥10 张图）", "media": "剪辑工程"}[pr.Category] + "，整组放进任务目录"
		case "doc":
			it.Target = uniqueTarget(filepath.Join(pp.Target, n.Materials, safeName(pr.Name)), false)
			it.Reason = "有 " + n.Readme
		default:
			hasCode[pr.Group] = true
			it.Target = uniqueTarget(filepath.Join(pp.Target, n.Code, safeName(pr.Name)), false)
			it.Reason = "项目标志：" + strings.Join(pr.Markers, "、")
		}
		var block []string
		if g := pr.Git; g != nil {
			if g.IsWorktree {
				block = append(block, "worktree（第一版不搬；以后只能用 git worktree move）")
				p.Counts.Worktrees++
			}
			if g.Locked {
				block = append(block, "worktree 被锁定")
			}
			if g.IsSubmodule {
				block = append(block, "子模块")
			}
			if len(g.Worktrees) > 0 {
				block = append(block, fmt.Sprintf("主仓带 %d 个 worktree（先搬主仓会让 worktree 失联，需 git worktree repair）", len(g.Worktrees)))
			}
			switch {
			case g.StatusChecked && g.Dirty:
				block = append(block, fmt.Sprintf("有未提交改动（%d 项）", g.DirtyCount))
				p.Counts.Dirty++
			case !g.StatusChecked:
				why := g.StatusError
				if why == "" {
					why = "没查"
				}
				block = append(block, "git 状态未知："+why)
			}
		}
		if !pr.ModTime.IsZero() && pr.ModTime.After(activeCut) {
			block = append(block, fmt.Sprintf("活跃（%d 天内改过）", cfg.ActiveDays))
		}
		if exists(it.Target) {
			block = append(block, "目标已存在")
		}
		it.Risks = block
		if rootDev != 0 && pr.Device != 0 && pr.Device != rootDev {
			it.CrossVolume = true
			it.Risks = append(it.Risks, "跨卷（执行时先复制、核对 sha256，再暂存原件，不删）")
			p.Counts.CrossVolume++
		}
		if len(block) > 0 {
			it.Action = ActionKeep
			p.Counts.Keep++
		} else {
			it.Action = ActionMove
			p.Counts.Move++
		}
		pp.Items = append(pp.Items, it)
	}

	loose := append([]scan.LooseFile(nil), res.Loose...)
	sort.Slice(loose, func(a, b int) bool { return loose[a].Path < loose[b].Path })
	for _, lf := range loose {
		it := Item{Source: lf.Path, Kind: "散落文件·" + lf.Category}
		if rootDev != 0 && lf.Device != 0 && lf.Device != rootDev {
			it.CrossVolume = true
		}
		pp := projByGroup[lf.Group]
		if lf.Group < 0 || pp == nil {
			it.Action = ActionPending
			it.Reason = "认不出属于哪个项目"
			p.Pending = append(p.Pending, it)
			p.Counts.Pending++
			continue
		}
		it.Project = pp.Name
		it.Target = uniqueTarget(filepath.Join(pp.Target, n.Materials, n.Loose, filepath.Base(lf.Path)), true)
		it.Reason = "文件名去版本后缀后和项目同名"
		if lf.Category == "压缩包" {
			it.Kind = "交付包"
		}
		if lf.ModTime.After(activeCut) {
			it.Risks = append(it.Risks, fmt.Sprintf("活跃（%d 天内改过）", cfg.ActiveDays))
		}
		if exists(it.Target) {
			it.Risks = append(it.Risks, "目标已存在")
		}
		if len(it.Risks) > 0 {
			it.Action = ActionKeep
			p.Counts.Keep++
		} else {
			it.Action = ActionMove
			p.Counts.Move++
		}
		if it.CrossVolume {
			it.Risks = append(it.Risks, "跨卷（执行时先复制、核对 sha256，再暂存原件，不删）")
			p.Counts.CrossVolume++
		}
		pp.Items = append(pp.Items, it)
	}

	ids := make([]int, 0, len(projByGroup))
	for id := range projByGroup {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return projByGroup[ids[a]].Name < projByGroup[ids[b]].Name })
	for _, id := range ids {
		pp := projByGroup[id]
		if hasCode[id] {
			pp.Create = append(pp.Create, dir(filepath.Join(pp.Target, n.Code)))
		}
		p.Projects = append(p.Projects, *pp)
	}
	p.Counts.Projects = len(p.Projects)
	p.Notes = []string{
		"这只是计划：什么都没挪、没删、没改。第一版没有执行命令。",
		"带风险标记的条目默认只登记不动。",
		fmt.Sprintf("排除了 %d 个疑似密钥/凭据文件（只计数，不列名字，不读内容）。", res.SensitiveSkipped),
		"没查进程占用和端口（第一版不做）；执行前还要再查。",
	}
	if res.Partial {
		p.Notes = append(p.Notes, "扫描没扫完（"+strings.Join(res.PartialReasons, "、")+"），计划不完整。")
	}
	return p
}

func dir(p string) string { return p + string(filepath.Separator) }

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func kindLabel(pr scan.Project) string {
	switch {
	case pr.Category == "images":
		return "图片组"
	case pr.Category == "media":
		return "剪辑工程"
	case pr.Git != nil && pr.Git.IsWorktree:
		return "worktree"
	case pr.Git != nil && pr.Kind == "clone":
		return "git 另一个 clone"
	case pr.Git != nil:
		return "git 主仓"
	case pr.Kind == "copy":
		return "手工副本"
	}
	return "项目文件夹"
}

// safeName 把名字里不适合当文件夹名的字符换掉。
func safeName(s string) string {
	s = strings.TrimSpace(s)
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\x00", "")
	s = r.Replace(s)
	s = strings.TrimLeft(s, "._")
	if s == "" {
		s = "未命名"
	}
	return s
}

// nearestDevice 返回 p 或它最近的已存在上级目录所在的设备号。
func nearestDevice(p string) uint64 {
	for cur := p; ; cur = filepath.Dir(cur) {
		if fi, err := os.Stat(cur); err == nil {
			return deviceOf(fi)
		}
		if filepath.Dir(cur) == cur {
			return 0
		}
	}
}

// JSON 返回计划的 JSON 字节和它的 sha12。
func (p *Plan) JSON() ([]byte, string, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, "", err
	}
	b = append(b, '\n')
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])[:12], nil
}

// Markdown 返回给人看的计划。
func (p *Plan) Markdown(sha12 string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# WorkShelf 整理计划\n\n")
	w("- 生成时间：%s（%s）\n", p.GeneratedAt, p.Timezone)
	w("- 扫描时间：%s\n", p.ScanGeneratedAt)
	w("- 总文件夹：`%s`\n", p.Root)
	w("- 计划 JSON 的 sha12：`%s`（以后执行时要对上这个值）\n", sha12)
	if p.ScanPartial {
		w("- ⚠️ 扫描没扫完：%s\n", strings.Join(p.PartialReasons, "、"))
	}
	w("\n**这只是计划，什么都没挪。**\n\n")
	w("## 汇总\n\n| 项目 | 可移动 | 只登记不动 | 待分类 | 有未提交改动 | worktree | 跨卷 |\n|---|---|---|---|---|---|---|\n")
	c := p.Counts
	w("| %d | %d | %d | %d | %d | %d | %d |\n\n", c.Projects, c.Move, c.Keep, c.Pending, c.Dirty, c.Worktrees, c.CrossVolume)
	w("## 要新建的总控骨架\n\n")
	for _, s := range p.Control {
		w("- `%s`\n", s)
	}
	w("\n## 按项目\n\n")
	for _, pp := range p.Projects {
		w("### %s\n\n目标：`%s`\n\n新建：", pp.Name, pp.Target)
		var cr []string
		for _, s := range pp.Create {
			cr = append(cr, "`"+filepath.Base(strings.TrimSuffix(s, string(filepath.Separator)))+"`")
		}
		w("%s\n\n", strings.Join(cr, "、"))
		w("| 动作 | 类型 | 原路径 | 新路径 | 理由 | 风险 |\n|---|---|---|---|---|---|\n")
		for _, it := range pp.Items {
			w("| %s | %s | `%s` | `%s` | %s | %s |\n", it.Action, it.Kind, md(it.Source), md(it.Target), md(it.Reason), md(strings.Join(it.Risks, "；")))
		}
		w("\n")
	}
	w("## 待分类清单（原地不动）\n\n")
	if len(p.Pending) == 0 {
		w("没有。\n\n")
	} else {
		w("| 类型 | 路径 |\n|---|---|\n")
		for _, it := range p.Pending {
			w("| %s | `%s` |\n", it.Kind, md(it.Source))
		}
		w("\n")
	}
	w("## 说明\n\n")
	for _, s := range p.Notes {
		w("- %s\n", s)
	}
	return b.String()
}

func md(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// WriteFiles 把计划写成 <dir>/workshelf-plan-<时间>.json 和 .md。已存在就加 -02，绝不覆盖。
func (p *Plan) WriteFiles(outDir string, now time.Time, loc *time.Location) (jsonPath, mdPath, sha12 string, err error) {
	data, sha12, err := p.JSON()
	if err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", "", err
	}
	stem := "workshelf-plan-" + now.In(loc).Format("20060102-150405")
	for i := 1; ; i++ {
		s := stem
		if i > 1 {
			s = fmt.Sprintf("%s-%02d", stem, i)
		}
		jp, mp := filepath.Join(outDir, s+".json"), filepath.Join(outDir, s+".md")
		if exists(jp) || exists(mp) {
			continue
		}
		if err := writeNew(jp, data); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", "", "", err
		}
		if err := writeNew(mp, []byte(p.Markdown(sha12))); err != nil {
			return jp, "", sha12, err
		}
		return jp, mp, sha12, nil
	}
}

// writeNew 只新建文件，已存在就报错（O_EXCL），绝不覆盖。
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
