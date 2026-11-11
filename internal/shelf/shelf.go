// Package shelf 给桌面版首页和项目页出数据：最近谁写了什么、放对没有、项目按工种分栏。
// 全部只读。
package shelf

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// 放置情况。
const (
	PlacedOK      = "已归位"
	PlacedPending = "待批准"
	PlacedLoose   = "项目内未归类"
	PlacedWrong   = "放错位置"
	PlacedOutside = "项目外"
	PlacedControl = "总控"
	PlacedInPlace = "原处未归位" // 在原地登记的项目里，还没搬进总文件夹
)

// 可信度。
const (
	ConfSelf     = "自报"
	ConfPut      = "入库登记"
	ConfSidecar  = "侧边记录"
	ConfInferred = "推断"
	ConfUnknown  = "来源不明"
)

// Item 是首页列表里的一个文件。
type Item struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	ModTime    string `json:"modTime"`
	ModUnix    int64  `json:"modUnix"`
	Size       int64  `json:"size"`
	Agent      string `json:"agent"`
	Confidence string `json:"confidence"`
	Modified   bool   `json:"modified"` // 登记后内容又被改过
	WorkType   string `json:"workType"`
	Project    string `json:"project"`
	Task       string `json:"task"`
	Status     string `json:"status"`
	Placement  string `json:"placement"`
	Note       string `json:"note"`
	ProjectID  string `json:"projectId,omitempty"` // 原地项目的 ID（总文件夹里的项目为空）
	InPlace    bool   `json:"inPlace,omitempty"`
}

// Env 是一次查询要用的环境。
type Env struct {
	Config config.Config
	Home   string
	Now    time.Time
	// 下面几项测试时可以改小。
	MaxDirs  int
	Deadline time.Duration
	// Reg 是原地登记表；为空时自动从总控里读（NoReg=true 时不读，测试用）。
	Reg   *registry.Registry
	NoReg bool
}

func (e *Env) defaults() {
	if e.Now.IsZero() {
		e.Now = time.Now()
	}
	if e.MaxDirs == 0 {
		e.MaxDirs = 30000
	}
	if e.Deadline == 0 {
		e.Deadline = 6 * time.Second
	}
	if e.Reg == nil && !e.NoReg {
		e.Reg, _ = registry.Load(e.Config)
	}
}

type walkRoot struct {
	dir   string
	depth int // 0 = 只看这一层的文件
}

func (e *Env) roots(rootDepth int) []walkRoot {
	h := e.Home
	rs := []walkRoot{{e.Config.Root, rootDepth}}
	if e.Reg != nil { // 原地项目（最近改过的在前），里面往下看 6 层
		for _, p := range e.Reg.Projects {
			for _, d := range p.Dirs() {
				rs = append(rs, walkRoot{d, 6})
			}
		}
	}
	return append(rs,
		walkRoot{filepath.Join(h, "Desktop"), 3},
		walkRoot{filepath.Join(h, "Downloads"), 2},
		walkRoot{filepath.Join(h, "Documents"), 4},
		walkRoot{h, 0},
	)
}

// facts 是一次查询读进来的记录。
type facts struct {
	env      *Env
	recs     map[string]activity.Record // 路径 -> 最新一条活动记录
	idx      map[string]put.Entry       // 路径 -> 最新一条索引
	status   map[string]string          // 路径 -> 最新状态（索引或活动记录里的）
	sidecars map[string]map[string]activity.Record
	notes    map[string]map[string]string // 任务目录 -> 说明.md 字段
	layouts  map[string]map[string]bool
}

func loadFacts(e *Env, since time.Time) *facts {
	f := &facts{env: e, recs: map[string]activity.Record{}, idx: map[string]put.Entry{}, status: map[string]string{},
		sidecars: map[string]map[string]activity.Record{}, notes: map[string]map[string]string{}}
	recs, _ := activity.ReadSince(activity.Dir(e.Config), since)
	for _, r := range recs {
		if r.Path == "" {
			continue
		}
		p := filepath.Clean(r.Path)
		if r.Status != "" {
			f.status[p] = r.Status
		}
		if old, ok := f.recs[p]; ok && r.Source == activity.SourceApprove {
			// 批准记录只改状态和哈希（批准时按当时内容登记），不改「谁写的」。
			old.Status = r.Status
			if r.SHA256 != "" {
				old.SHA256 = r.SHA256
			}
			f.recs[p] = old
			continue
		}
		f.recs[p] = r
	}
	n := e.Config.Names
	for _, en := range put.ReadIndexDir(filepath.Join(e.Config.Root, n.Control, n.IndexShards), e.Config.Root) {
		p := filepath.Clean(en["_abs"])
		if p == "." {
			continue
		}
		if st := en["状态"]; st != "" {
			f.status[p] = st
		}
		if old, ok := f.idx[p]; ok && en["状态"] == "已交" && old["负责智能体"] != "" {
			old["状态"] = "已交"
			// 批准时按当时的内容登记过；以批准时的哈希为准，批准前的改动不再算「写后被改过」。
			for _, k := range []string{"sha256", "大小(字节)"} {
				if en[k] != "" {
					old[k] = en[k]
				}
			}
			continue
		}
		f.idx[p] = en
	}
	return f
}

func (f *facts) sidecar(dir string) map[string]activity.Record {
	if m, ok := f.sidecars[dir]; ok {
		return m
	}
	m := map[string]activity.Record{}
	for _, r := range activity.ReadSidecar(dir) {
		m[filepath.Clean(r.Path)] = r
	}
	f.sidecars[dir] = m
	return m
}

// taskNote 读任务目录里的 说明.md 字段（工种、模板、状态、负责智能体……）。
func (f *facts) taskNote(dir string) map[string]string {
	if m, ok := f.notes[dir]; ok {
		return m
	}
	m := ReadNoteFields(filepath.Join(dir, "说明.md"))
	f.notes[dir] = m
	return m
}

var noteLine = regexp.MustCompile(`^\s*[-*]?\s*([^:：\s|]{1,12})\s*[:：]\s*(.+?)\s*$`)

// ReadNoteFields 读说明文件里「键：值」形式的行；同一个键后出现的覆盖先出现的。
func ReadNoteFields(path string) map[string]string {
	m := map[string]string{}
	fh, err := os.Open(path)
	if err != nil {
		return m
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 0; sc.Scan() && i < 400; i++ {
		if g := noteLine.FindStringSubmatch(sc.Text()); g != nil {
			m[g[1]] = strings.Trim(g[2], "`*")
		}
	}
	return m
}

// where 算文件在总文件夹里的位置：项目、第二层目录、任务。
func (e *Env) where(path string) (project, area, task string, inRoot bool) {
	rel, err := filepath.Rel(e.Config.Root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	project = parts[0]
	if len(parts) > 2 {
		area = parts[1]
	}
	if len(parts) > 3 && (area == e.Config.Names.Tasks || area == e.Config.Names.Outputs) {
		task = parts[2]
	}
	if len(parts) == 1 {
		project = ""
	}
	return project, area, task, true
}

func (f *facts) placement(path string) string {
	e := f.env
	n := e.Config.Names
	project, area, _, in := e.where(path)
	if !in {
		if e.Reg.Owner(path) != nil {
			return PlacedInPlace
		}
		dir := filepath.Dir(path)
		for _, bad := range []string{filepath.Join(e.Home, "Desktop"), filepath.Join(e.Home, "Downloads")} {
			if dir == bad || strings.HasPrefix(dir, bad+string(filepath.Separator)) {
				return PlacedWrong
			}
		}
		if dir == e.Home {
			return PlacedWrong
		}
		return PlacedOutside
	}
	switch {
	case project == "":
		return PlacedWrong // ~/Work 顶层的散文件
	case project == n.Control:
		return PlacedControl
	case area == n.Outputs:
		if f.statusOf(path) == "待批准" {
			return PlacedPending
		}
		return PlacedOK
	case area == n.Tasks || area == n.Materials || area == n.Code || area == n.Archive:
		return PlacedOK
	case area == "" && filepath.Base(path) == n.Readme:
		return PlacedOK
	}
	if f.layouts == nil {
		f.layouts = map[string]map[string]bool{}
	}
	if _, ok := f.layouts[project]; !ok {
		folders := map[string]bool{}
		for _, name := range projectLayoutFolders(filepath.Join(e.Config.Root, project), ProjectWorkTypes(filepath.Join(e.Config.Root, project, n.Readme))) {
			folders[strings.Split(name, "/")[0]] = true
		}
		f.layouts[project] = folders
	}
	if f.layouts[project][area] {
		return PlacedOK
	}
	return PlacedLoose
}

func (f *facts) statusOf(path string) string {
	if s := f.status[path]; s != "" {
		return s
	}
	return ""
}

// describe 补全一个文件的来源、工种、放置情况。checkHash=true 时核对登记时的哈希。
func (f *facts) describe(path string, fi os.FileInfo, checkHash bool) Item {
	e := f.env
	loc := e.Config.Location()
	it := Item{Path: path, Name: filepath.Base(path), Dir: filepath.Dir(path), Size: fi.Size(),
		ModTime: fi.ModTime().In(loc).Format(time.RFC3339), ModUnix: fi.ModTime().Unix()}
	project, _, task, in := e.where(path)
	if project == e.Config.Names.Control {
		project = ""
	}
	if !in {
		if o := e.Reg.Owner(path); o != nil {
			project, it.ProjectID, it.InPlace = o.Name, o.ID, true
		}
	}
	it.Project, it.Task = project, task
	var sum string
	if r, ok := f.recs[path]; ok {
		it.Agent, it.WorkType, it.Note = r.Agent, r.WorkType, r.Note
		it.Confidence = ConfSelf
		if r.Source == activity.SourcePut {
			it.Confidence = ConfPut
		}
		it.Project, it.Task = nz(r.Project, it.Project), nz(r.Task, it.Task)
		sum = r.SHA256
	} else if r, ok := f.sidecar(it.Dir)[path]; ok {
		it.Agent, it.WorkType, it.Note, it.Confidence = r.Agent, r.WorkType, r.Note, ConfSidecar
		it.Project, it.Task = nz(r.Project, it.Project), nz(r.Task, it.Task)
		sum = r.SHA256
	} else if en, ok := f.idx[path]; ok {
		it.Agent, it.WorkType, it.Note, it.Confidence = en["负责智能体"], en["工种"], en["一句话说明"], ConfPut
		it.Project = nz(en["项目"], it.Project)
		sum = en["sha256"]
	} else if task != "" {
		m := f.taskNote(filepath.Join(e.Config.Root, project, e.Config.Names.Tasks, task))
		if a := m["负责智能体"]; a != "" {
			it.Agent, it.Confidence = a, ConfInferred
		}
		it.WorkType = m["工种"]
	}
	if it.Confidence == "" {
		it.Confidence = ConfUnknown
	}
	if it.WorkType == "" && task != "" {
		it.WorkType = f.taskNote(filepath.Join(e.Config.Root, project, e.Config.Names.Tasks, task))["工种"]
	}
	it.Status = f.statusOf(path)
	it.Placement = f.placement(path)
	if checkHash && sum != "" && fi.Size() < 512<<20 {
		if got, err := activity.FileSHA256(path); err == nil && !strings.EqualFold(got, sum) {
			it.Modified = true
		}
	}
	return it
}

func nz(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// walk 在几个根目录下找文件，keep 返回 true 的收下。不跟软链接、跳过点文件和缓存类目录、跳过总控。
func (e *Env) walk(roots []walkRoot, keep func(path string, fi os.FileInfo) bool) {
	start := time.Now()
	seen := map[string]bool{}
	dirs := 0
	control := filepath.Join(e.Config.Root, e.Config.Names.Control)
	var visit func(dir string, depth, max int)
	visit = func(dir string, depth, max int) {
		if dirs >= e.MaxDirs || time.Since(start) > e.Deadline || seen[dir] {
			return
		}
		seen[dir] = true
		dirs++
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, en := range ents {
			name := en.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(dir, name)
			t := en.Type()
			if t&os.ModeSymlink != 0 {
				continue
			}
			if en.IsDir() {
				if depth < max && !scan.SkipDirName(name) && p != control {
					visit(p, depth+1, max)
				}
				continue
			}
			if !t.IsRegular() || scan.IsSensitiveName(name) {
				continue
			}
			fi, err := en.Info()
			if err != nil {
				continue
			}
			keep(p, fi)
		}
	}
	for _, r := range roots {
		if fi, err := os.Stat(r.dir); err == nil && fi.IsDir() {
			visit(r.dir, 0, r.depth)
		}
	}
}

// Recent 列出最近 days 天里改过的文件（新的在前），最多 limit 个。
func Recent(e Env, days, limit int) []Item {
	e.defaults()
	if days <= 0 {
		days = 3
	}
	if limit <= 0 {
		limit = 300
	}
	since := e.Now.AddDate(0, 0, -days)
	f := loadFacts(&e, since)
	type hit struct {
		path string
		fi   os.FileInfo
	}
	var hits []hit
	got := map[string]bool{}
	e.walk(e.roots(7), func(p string, fi os.FileInfo) bool {
		if fi.ModTime().Before(since) {
			return false
		}
		hits = append(hits, hit{p, fi})
		got[p] = true
		return true
	})
	// 记录里提到、但不在常看目录里的文件也算上（比如 ~/Projects 里的）。
	for p, r := range f.recs {
		if got[p] || r.When().Before(since) {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			hits = append(hits, hit{p, fi})
			got[p] = true
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].fi.ModTime().After(hits[j].fi.ModTime()) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Item, 0, len(hits))
	for _, h := range hits {
		out = append(out, f.describe(h.path, h.fi, true))
	}
	return out
}

// Search 按关键词找文件：名字、路径、智能体、工种、项目都能搜；多个词要同时命中。
func Search(e Env, query string, limit int) []Item {
	e.defaults()
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 200
	}
	f := loadFacts(&e, time.Time{})
	match := func(s string) bool {
		s = strings.ToLower(s)
		for _, w := range words {
			if !strings.Contains(s, w) {
				return false
			}
		}
		return true
	}
	type hit struct {
		path string
		fi   os.FileInfo
	}
	var hits []hit
	got := map[string]bool{}
	e.walk(e.roots(9), func(p string, fi os.FileInfo) bool {
		hay := p
		if o := e.Reg.Owner(p); o != nil {
			hay += " " + o.Name + " " + strings.Join(o.Aliases, " ")
		}
		if r, ok := f.recs[p]; ok {
			hay += " " + r.Agent + " " + r.WorkType + " " + r.Project + " " + r.Note
		} else if r, ok := f.sidecar(filepath.Dir(p))[p]; ok {
			hay += " " + r.Agent + " " + r.WorkType + " " + r.Note
		} else if en, ok := f.idx[p]; ok {
			hay += " " + en["负责智能体"] + " " + en["工种"] + " " + en["一句话说明"]
		}
		if match(hay) {
			hits = append(hits, hit{p, fi})
			got[p] = true
		}
		return true
	})
	for p, r := range f.recs {
		if got[p] || !match(p+" "+r.Agent+" "+r.WorkType+" "+r.Project+" "+r.Note) {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			hits = append(hits, hit{p, fi})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].fi.ModTime().After(hits[j].fi.ModTime()) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Item, 0, len(hits))
	for _, h := range hits {
		out = append(out, f.describe(h.path, h.fi, false))
	}
	return out
}
