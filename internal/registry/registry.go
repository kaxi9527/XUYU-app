// Package registry 是「原地登记」：把扫描找到的、还在原处（没搬进总文件夹）的项目登记下来，
// 让桌面版和 recent/search 也能看到它们。
//
// 只读扫描，不搬、不改、不删任何项目文件；登记表只写在 <总文件夹>/<总控>/<原地登记>/ 下。
// 手工副本、交付包、压缩包、项目里嵌套的子项目都归到所属项目下，不各自成项目。
package registry

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// Status 是原地项目的状态文字。
const Status = "还在原处，未归位"

// FileName 是登记表文件名。
const FileName = "原地项目.json"

// Version 是登记表格式版本。
const Version = 1

// 成员种类。
const (
	KindMain     = "主版本"
	KindWorktree = "worktree"
	KindClone    = "另一个 clone"
	KindCopy     = "手工副本"
	KindHandoff  = "副本/交付包"
	KindArchive  = "压缩包"
	KindFile     = "相关文件"
	KindNested   = "子文件夹"
)

// Member 是一个项目的一个版本、副本或相关文件。
type Member struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	IsDir bool   `json:"isDir"`
	Via   string `json:"via,omitempty"` // 通过哪个软链接发现的
}

// Project 是一个原地项目（一组版本）。
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"` // 主版本
	Category    string    `json:"category"`
	Markers     []string  `json:"markers"`
	Origin      string    `json:"origin,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	Members     []Member  `json:"members"`
	Aliases     []string  `json:"aliases,omitempty"` // 指向它的软链接
	ModTime     time.Time `json:"modTime"`
	Status      string    `json:"status"`
	Reasons     []string  `json:"reasons,omitempty"`
	FromSession bool      `json:"fromSession,omitempty"`
}

// Dirs 返回这个项目的所有文件夹（主版本在前）。
func (p *Project) Dirs() []string {
	out := []string{p.Path}
	for _, m := range p.Members {
		if m.IsDir && m.Path != p.Path {
			out = append(out, m.Path)
		}
	}
	return out
}

// Registry 是整张登记表。
type Registry struct {
	Loose          []scan.LooseFile `json:"loose,omitempty"` // Unassigned scan results, available for existing-root project matching.
	Version        int              `json:"version"`
	GeneratedAt    time.Time        `json:"generatedAt"`
	Roots          []string         `json:"roots"`
	Partial        bool             `json:"partial"`
	PartialReasons []string         `json:"partialReasons,omitempty"`
	Unreadable     int              `json:"unreadable,omitempty"`
	Hidden         int              `json:"hidden"` // 没单独列出的零散图片文件夹（证据截图之类）
	Projects       []Project        `json:"projects"`

	dirs []ownerDir // 查「这个文件属于哪个原地项目」用，最长的在前
}

type ownerDir struct {
	dir string
	idx int
}

// Dir 返回登记表所在目录。
func Dir(cfg config.Config) string {
	return filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.Registry)
}

// ScanOptions 按配置组扫描参数。paths 非空时只扫这些。
func ScanOptions(cfg config.Config, home string, paths []string, volumes, gitStatus bool) (scan.Options, error) {
	opt := scan.Options{
		MaxDepth: cfg.MaxDepth, MaxDirs: cfg.MaxDirs, MaxFiles: cfg.MaxFiles,
		Timeout:        time.Duration(cfg.TimeoutSeconds) * time.Second,
		Exclude:        []string{cfg.Root},
		ExtraSkipNames: cfg.ExtraSkip,
		GitStatus:      gitStatus,
		ShallowRoots:   map[string]int{},
	}
	hc := filepath.Clean(home)
	if len(paths) > 0 {
		for _, p := range paths {
			abs, err := filepath.Abs(config.Expand(p, home))
			if err != nil {
				return opt, err
			}
			if _, err := os.Stat(abs); err != nil {
				return opt, fmt.Errorf("扫描路径不存在：%s", abs)
			}
			opt.Roots = append(opt.Roots, abs)
			if abs == hc {
				opt.Containers = append(opt.Containers, abs)
				opt.ShallowRoots[abs] = cfg.HomeDepth
			}
		}
	} else {
		for _, p := range cfg.ScanPaths {
			p = filepath.Clean(p)
			if _, err := os.Stat(p); err != nil {
				continue // 默认范围里没有的目录（比如没有 ~/Projects）直接跳过
			}
			opt.Roots = append(opt.Roots, p)
			opt.Containers = append(opt.Containers, p)
			if p == hc {
				opt.ShallowRoots[p] = cfg.HomeDepth
			}
		}
	}
	if volumes || cfg.ScanVolumes {
		if entries, err := os.ReadDir(cfg.VolumesDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
					v := filepath.Join(cfg.VolumesDir, e.Name())
					opt.Roots = append(opt.Roots, v)
					opt.Containers = append(opt.Containers, v)
				}
			}
		}
	}
	sort.SliceStable(opt.Roots, func(a, b int) bool { return opt.Roots[a] != hc && opt.Roots[b] == hc })
	return opt, nil
}

func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// 没什么信息量的文件夹名：用「上级/名字」当项目名。
var genericNames = map[string]bool{
	"repository": true, "repo": true, "src": true, "source": true, "core": true, "app": true, "web": true,
	"website": true, "project": true, "code": true, "task": true, "evidence": true, "output": true,
	"assets": true, "images": true, "main": true, "final": true, "qa": true, "render": true,
}

// Build 扫描并生成登记表（只读）。
func Build(cfg config.Config, home string, opt scan.Options) (*Registry, error) {
	res, err := scan.Scan(opt)
	if err != nil {
		return nil, err
	}
	reg := &Registry{Version: Version, GeneratedAt: time.Now(), Roots: opt.Roots, Partial: res.Partial, PartialReasons: res.PartialReasons, Unreadable: res.Unreadable}
	hc := filepath.Clean(home)
	isRoot := map[string]bool{}
	for _, r := range opt.Roots {
		isRoot[filepath.Clean(r)] = true
	}
	ps := res.Projects
	loose := res.Loose

	// 1. 扫描根顶层的软链接：指向扫描范围外的文件夹就补扫一下；指向已知项目的记成别名。
	via := map[string]string{} // 目标真实路径 -> 软链接路径
	covered := func(p string) bool {
		for _, r := range opt.Roots {
			if filepath.Clean(r) != hc && within(p, filepath.Clean(r)) {
				return true
			}
		}
		for _, q := range ps {
			if within(p, q.Path) {
				return true
			}
		}
		return false
	}
	for _, r := range opt.Roots {
		ents, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, en := range ents {
			if en.Type()&os.ModeSymlink == 0 || strings.HasPrefix(en.Name(), ".") {
				continue
			}
			link := filepath.Join(r, en.Name())
			target, err := filepath.EvalSymlinks(link)
			if err != nil {
				continue
			}
			if fi, err := os.Stat(target); err != nil || !fi.IsDir() || within(target, cfg.Root) || target == hc {
				continue
			}
			if _, ok := via[target]; !ok {
				via[target] = link
			}
			if covered(target) {
				continue
			}
			sub := opt
			sub.Roots, sub.Containers, sub.ShallowRoots = []string{target}, nil, map[string]int{}
			if sub.MaxDepth > 4 {
				sub.MaxDepth = 4
			}
			extra, err := scan.Scan(sub)
			if err != nil {
				continue
			}
			off := 0
			for _, q := range ps {
				if q.Group >= off {
					off = q.Group + 1
				}
			}
			for _, q := range extra.Projects {
				q.Group += off
				ps = append(ps, q)
			}
		}
	}

	// 2. 节点：扫描到的项目 + 扫描根顶层「名字以某项目名开头」的副本文件夹和文件 + 扫描时对上组的散落文件。
	type node struct {
		path  string
		isDir bool
		proj  int // ps 下标，-1 表示不是扫描到的项目
		kind  string
		mod   time.Time
	}
	var nodes []node
	for i, q := range ps {
		nodes = append(nodes, node{path: q.Path, isDir: true, proj: i, mod: q.ModTime})
	}
	uf := make([]int, len(nodes))
	find := func(x int) int {
		for uf[x] != x {
			uf[x] = uf[uf[x]]
			x = uf[x]
		}
		return x
	}
	addNode := func(n node) int {
		nodes = append(nodes, n)
		uf = append(uf, len(nodes)-1)
		return len(nodes) - 1
	}
	for i := range uf {
		uf[i] = i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			uf[rb] = ra
		}
	}

	// 用来匹配副本名的项目名（只用代码/媒体/文档类项目，至少 4 个字符）。
	type key struct {
		k string
		i int
	}
	var keys []key
	for i, q := range ps {
		if q.Category == "images" {
			continue
		}
		for _, k := range []string{strings.ToLower(q.BaseName), strings.ToLower(q.Name)} {
			if utf8.RuneCountInString(k) >= 4 && !genericNames[k] {
				keys = append(keys, key{k, i})
			}
		}
	}
	sort.SliceStable(keys, func(a, b int) bool { return len(keys[a].k) > len(keys[b].k) })
	match := func(name string) int {
		l := strings.ToLower(name)
		for _, k := range keys {
			if l == k.k {
				return k.i
			}
			if strings.HasPrefix(l, k.k) {
				r, _ := utf8.DecodeRuneInString(l[len(k.k):])
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
					return k.i
				}
			}
		}
		return -1
	}
	isProjPath := map[string]bool{}
	for _, q := range ps {
		isProjPath[q.Path] = true
	}
	for _, r := range opt.Roots {
		ents, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, en := range ents {
			name := en.Name()
			if strings.HasPrefix(name, ".") || en.Type()&os.ModeSymlink != 0 {
				continue
			}
			p := filepath.Join(r, name)
			if isProjPath[p] || within(p, cfg.Root) || (!en.IsDir() && !en.Type().IsRegular()) || scan.IsSensitiveName(name) {
				continue
			}
			k := name
			if !en.IsDir() {
				k = strings.TrimSuffix(name, filepath.Ext(name))
				for strings.Contains(strings.ToLower(filepath.Ext(k)), "tar") {
					k = strings.TrimSuffix(k, filepath.Ext(k))
				}
			}
			i := match(k)
			if i < 0 {
				i = match(scan.CopyKey(k))
			}
			if i < 0 {
				continue
			}
			kind := KindFile
			if en.IsDir() {
				kind = KindHandoff
			} else if isArchive(name) {
				kind = KindArchive
			}
			var mt time.Time
			if fi, err := en.Info(); err == nil {
				mt = fi.ModTime()
			}
			union(i, addNode(node{path: p, isDir: en.IsDir(), proj: -1, kind: kind, mod: mt}))
		}
	}
	seenPath := map[string]bool{}
	for _, n := range nodes {
		seenPath[n.path] = true
	}
	for _, l := range loose {
		if seenPath[l.Path] {
			continue
		}
		base := filepath.Base(l.Path)
		lead := match(strings.TrimSuffix(base, filepath.Ext(base)))
		if lead < 0 && l.Group >= 0 {
			// 扫描时按名字对上了组：名字本身没信息量（core、assets……）或太短的不算。
			if k := scan.FileKey(base); utf8.RuneCountInString(k) >= 3 && !genericNames[k] {
				for i, q := range ps {
					if q.Group == l.Group {
						lead = i
						break
					}
				}
			}
		}
		if lead < 0 {
			continue
		}
		kind := KindFile
		if isArchive(l.Path) {
			kind = KindArchive
		}
		seenPath[l.Path] = true
		union(lead, addNode(node{path: l.Path, proj: -1, kind: kind, mod: l.ModTime}))
	}

	// 3. 同一扫描组 → 合并；嵌套在别的节点文件夹里的项目 → 并到最近的上级。
	byGroup := map[int]int{}
	for i, q := range ps {
		if j, ok := byGroup[q.Group]; ok {
			union(j, i)
		} else {
			byGroup[q.Group] = i
		}
	}
	order := make([]int, 0, len(nodes))
	for i, n := range nodes {
		if n.isDir {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool { return len(nodes[order[a]].path) < len(nodes[order[b]].path) })
	for x, i := range order {
		best := -1
		for _, j := range order[:x] {
			if within(nodes[i].path, nodes[j].path) && nodes[j].path != nodes[i].path && (best < 0 || len(nodes[j].path) > len(nodes[best].path)) {
				best = j
			}
		}
		if best >= 0 {
			union(best, i)
		}
	}

	// 4. 每组选主版本、起名字、挑掉零散图片文件夹。
	sets := map[int][]int{}
	for i := range nodes {
		sets[find(i)] = append(sets[find(i)], i)
	}
	score := func(q scan.Project) int {
		s := 0
		if q.Category == "images" {
			s += 100
		}
		switch {
		case q.Git != nil && !q.Git.IsWorktree && q.Kind != "copy":
		case q.Git != nil:
			s += 10
		default:
			s += 20
		}
		if q.Kind == "copy" {
			s += 5
		}
		return s
	}
	used := map[string]int{}
	for _, idx := range sets {
		lead := -1
		for _, i := range idx {
			if nodes[i].proj < 0 {
				continue
			}
			if lead < 0 {
				lead = i
				continue
			}
			a, b := ps[nodes[i].proj], ps[nodes[lead].proj]
			sa, sb := score(a), score(b)
			if sa < sb || sa == sb && (strings.Count(a.Path, string(filepath.Separator)) < strings.Count(b.Path, string(filepath.Separator)) ||
				strings.Count(a.Path, string(filepath.Separator)) == strings.Count(b.Path, string(filepath.Separator)) && a.Path < b.Path) {
				lead = i
			}
		}
		if lead < 0 {
			continue
		}
		q := ps[nodes[lead].proj]
		if within(q.Path, cfg.Root) {
			continue
		}
		onlyImages := true
		for _, i := range idx {
			if nodes[i].proj >= 0 && ps[nodes[i].proj].Category != "images" {
				onlyImages = false
			}
		}
		if onlyImages && !isRoot[filepath.Dir(q.Path)] {
			reg.Hidden++
			continue
		}
		p := Project{Path: q.Path, Category: q.Category, Markers: q.Markers, Status: Status, ModTime: q.ModTime}
		if q.Git != nil {
			p.Origin, p.Branch = q.Git.Origin, q.Git.Branch
		}
		name := q.BaseName
		if strings.TrimSpace(name) == "" {
			name = q.Name
		}
		if genericNames[strings.ToLower(name)] && !isRoot[filepath.Dir(q.Path)] {
			name = filepath.Base(filepath.Dir(q.Path)) + "/" + q.Name
		}
		p.Name = name
		if l, ok := via[q.Path]; ok {
			p.Aliases = append(p.Aliases, l)
		}
		sort.Slice(idx, func(a, b int) bool { return nodes[idx[a]].path < nodes[idx[b]].path })
		seen := map[string]bool{q.Path: true}
		inner := func(p string) bool { // 在主版本或别的成员文件夹里面的，不单独列
			for _, j := range idx {
				if nodes[j].isDir && nodes[j].path != p && within(p, nodes[j].path) {
					return true
				}
			}
			return false
		}
		reasons := map[string]bool{}
		p.Members = append(p.Members, Member{Path: q.Path, Kind: KindMain, IsDir: true})
		for _, i := range idx {
			n := nodes[i]
			if seen[n.path] {
				continue
			}
			seen[n.path] = true
			if n.mod.After(p.ModTime) {
				p.ModTime = n.mod
			}
			if inner(n.path) {
				if l, ok := via[n.path]; ok {
					p.Aliases = append(p.Aliases, l)
				}
				continue
			}
			m := Member{Path: n.path, IsDir: n.isDir, Kind: n.kind}
			if n.proj >= 0 {
				o := ps[n.proj]
				switch {
				case within(o.Path, q.Path):
					m.Kind = KindNested
				case o.Kind == "worktree":
					m.Kind, reasons["同一个 git 仓库"] = KindWorktree, true
				case o.Kind == "clone":
					m.Kind, reasons["同一个 git 仓库"] = KindClone, true
				default:
					m.Kind = KindNested
					for _, j := range idx {
						if nodes[j].proj < 0 && nodes[j].isDir && within(o.Path, nodes[j].path) {
							m.Kind = KindHandoff
						}
					}
					if m.Kind == KindNested && !within(o.Path, q.Path) {
						m.Kind, reasons["名字去后缀后相同且顶层文件重合"] = KindCopy, true
					}
				}
				if l, ok := via[o.Path]; ok {
					m.Via = l
					p.Aliases = append(p.Aliases, l)
				}
			} else {
				reasons["名字以项目名开头（副本、交付包、压缩包）"] = true
			}
			if n.mod.After(p.ModTime) {
				p.ModTime = n.mod
			}
			p.Members = append(p.Members, m)
		}
		for t, l := range via { // 指向项目里面某个子文件夹的软链接，也记成别名
			if t != q.Path && within(t, q.Path) {
				p.Aliases = append(p.Aliases, l)
			}
		}
		sort.Strings(p.Aliases)
		for r := range reasons {
			p.Reasons = append(p.Reasons, r)
		}
		sort.Strings(p.Reasons)
		used[strings.ToLower(p.Name)]++
		sum := sha1.Sum([]byte(q.Path))
		p.ID = hex.EncodeToString(sum[:])[:10]
		reg.Projects = append(reg.Projects, p)
	}
	// 重名的加上级目录区分。
	for i := range reg.Projects {
		p := &reg.Projects[i]
		if used[strings.ToLower(p.Name)] > 1 {
			p.Name = p.Name + "（" + shortParent(p.Path, home) + "）"
		}
	}
	sort.Slice(reg.Projects, func(i, j int) bool {
		if !reg.Projects[i].ModTime.Equal(reg.Projects[j].ModTime) {
			return reg.Projects[i].ModTime.After(reg.Projects[j].ModTime)
		}
		return reg.Projects[i].Path < reg.Projects[j].Path
	})
	reg.index()
	for _, file := range loose {
		if reg.Owner(file.Path) == nil {
			reg.Loose = append(reg.Loose, file)
		}
	}
	return reg, nil
}

func shortParent(p, home string) string {
	d := filepath.Dir(p)
	if rel, err := filepath.Rel(home, d); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(rel)
	}
	return d
}

func isArchive(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".tar", ".gz", ".tgz", ".7z", ".rar", ".dmg", ".xz", ".bz2":
		return true
	}
	return false
}

func (r *Registry) index() {
	r.dirs = nil
	for i := range r.Projects {
		for _, d := range r.Projects[i].Dirs() {
			r.dirs = append(r.dirs, ownerDir{d, i})
		}
	}
	sort.SliceStable(r.dirs, func(a, b int) bool { return len(r.dirs[a].dir) > len(r.dirs[b].dir) })
}

// Owner 返回这个路径属于哪个原地项目（最深的那个），没有就返回 nil。
func (r *Registry) Owner(path string) *Project {
	if r == nil {
		return nil
	}
	if r.dirs == nil {
		r.index()
	}
	for _, d := range r.dirs {
		if within(path, d.dir) {
			return &r.Projects[d.idx]
		}
	}
	return nil
}

// ByID 按 ID 找项目。
func (r *Registry) ByID(id string) *Project {
	if r == nil {
		return nil
	}
	for i := range r.Projects {
		if r.Projects[i].ID == id {
			return &r.Projects[i]
		}
	}
	return nil
}

// Load 读登记表。不存在时返回 nil, os.ErrNotExist。
func Load(cfg config.Config) (*Registry, error) {
	b, err := os.ReadFile(filepath.Join(Dir(cfg), FileName))
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("登记表读不了：%w", err)
	}
	if r.Version != Version {
		return nil, errors.New("登记表版本不对，需要重新识别")
	}
	r.index()
	return &r, nil
}

// Save 把登记表写到总控里（JSON + 给人看的 Markdown）。先写临时文件再改名，不会写坏一半。
var saveMu sync.Mutex

func Save(cfg config.Config, r *Registry, home string) (string, error) {
	saveMu.Lock()
	defer saveMu.Unlock()
	if previous, err := Load(cfg); err == nil {
		for _, old := range previous.Projects {
			if !old.FromSession {
				continue
			}
			found := false
			for i := range r.Projects {
				if r.Projects[i].Path == old.Path {
					r.Projects[i].FromSession = true
					found = true
					break
				}
			}
			if !found {
				r.Projects = append(r.Projects, old)
			}
		}
	}
	r.index()
	return save(cfg, r, home)
}

// EnsureSessionProject adds a recognized project without replacing the existing registry.
func EnsureSessionProject(cfg config.Config, home string, candidate *scan.Project) (*Project, error) {
	saveMu.Lock()
	defer saveMu.Unlock()
	r, err := Load(cfg)
	if errors.Is(err, os.ErrNotExist) {
		r = &Registry{Version: Version, GeneratedAt: time.Now()}
	} else if err != nil {
		return nil, err
	}
	if existing := r.Owner(candidate.Path); existing != nil {
		return existing, nil
	}
	sum := sha1.Sum([]byte(candidate.Path))
	name := candidate.BaseName
	if strings.TrimSpace(name) == "" {
		name = candidate.Name
	}
	project := Project{ID: hex.EncodeToString(sum[:])[:10], Name: name, Path: candidate.Path, Category: candidate.Category, Markers: candidate.Markers, ModTime: candidate.ModTime, Status: Status, FromSession: true}
	r.Projects = append(r.Projects, project)
	if _, err = save(cfg, r, home); err != nil {
		return nil, err
	}
	return &project, nil
}

func save(cfg config.Config, r *Registry, home string) (string, error) {
	dir := Dir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, FileName)
	if err := writeAtomic(path, append(b, '\n')); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, "原地项目.md"), []byte(r.Markdown(home))); err != nil {
		return path, err
	}
	return path, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Markdown 是给人看的登记表。
func (r *Registry) Markdown(home string) string {
	var sb strings.Builder
	sh := func(p string) string {
		if within(p, home) {
			return "~" + strings.TrimPrefix(p, home)
		}
		return p
	}
	fmt.Fprintf(&sb, "# 原地项目登记\n\n这些项目还在原处，没有搬进总文件夹。WorkShelf 只是登记，没有搬动、改动任何文件。\n\n")
	fmt.Fprintf(&sb, "- 生成时间：%s\n- 扫描范围：%s\n- 项目数：%d（另有 %d 个零散图片文件夹没单独列）\n\n", r.GeneratedAt.Format(time.RFC3339), strings.Join(r.Roots, "、"), len(r.Projects), r.Hidden)
	sb.WriteString("| 项目 | 主版本位置 | 副本/相关 | 最近改动 |\n|---|---|---|---|\n")
	for _, p := range r.Projects {
		fmt.Fprintf(&sb, "| %s | `%s` | %d | %s |\n", strings.ReplaceAll(p.Name, "|", "\\|"), sh(p.Path), len(p.Members)-1, p.ModTime.Format("2006-01-02 15:04"))
	}
	return sb.String()
}
