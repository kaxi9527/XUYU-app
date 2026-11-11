package shelf

import (
	"fmt"
	"os"

	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ProjectSummary 是项目总览里的一张卡片。
type ProjectSummary struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	WorkTypes  []string `json:"workTypes"`
	Tasks      int      `json:"tasks"`
	Outputs    int      `json:"outputs"`
	Pending    int      `json:"pending"`
	ModTime    string   `json:"modTime"`
	ModUnix    int64    `json:"modUnix"`
	Linked     bool     `json:"linked,omitempty"`
	LinkTarget string   `json:"linkTarget,omitempty"`
	// 原地项目（还在原处，没搬进总文件夹）才有下面几项。
	Source   string   `json:"source"` // 总文件夹 / 原地
	ID       string   `json:"id,omitempty"`
	Status   string   `json:"status,omitempty"`
	Category string   `json:"category,omitempty"`
	Members  int      `json:"members,omitempty"` // 副本、worktree、交付包、相关文件个数（不含主版本）
	Aliases  []string `json:"aliases,omitempty"`
	Origin   string   `json:"origin,omitempty"`
}

// 来源。
const (
	SourceRoot    = "总文件夹"
	SourceInPlace = "原地"
)

// Entry 是项目里的一个文件或文件夹。
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Status  string `json:"status"`
	Agent   string `json:"agent"`
}

// TaskInfo 是一个任务（任务/ 下的一个文件夹）。
type TaskInfo struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	WorkType string  `json:"workType"`
	Template string  `json:"template"`
	Status   string  `json:"status"`
	Agent    string  `json:"agent"`
	Note     string  `json:"note"`
	Files    int     `json:"files"`
	Outputs  []Entry `json:"outputs"`
	ModTime  string  `json:"modTime"`
	ModUnix  int64   `json:"modUnix"`
}

// Column 是项目详情里按工种分的一栏。
type Column struct {
	WorkType string     `json:"workType"`
	Template string     `json:"template"`
	Tasks    []TaskInfo `json:"tasks"`
}

// ProjectDetail 是项目详情页。
type ProjectDetail struct {
	Folders    []ProjectFolder `json:"folders,omitempty"`
	Name       string          `json:"name"`
	Path       string          `json:"path"`
	Linked     bool            `json:"linked,omitempty"`
	LinkTarget string          `json:"linkTarget,omitempty"`
	Readme     string          `json:"readme"`
	WorkTypes  []string        `json:"workTypes"`
	Columns    []Column        `json:"columns"`
	Materials  []Entry         `json:"materials"`
	Code       []Entry         `json:"code"`
	Loose      []Entry         `json:"loose"`
	Sensitive  int             `json:"sensitive"` // 敏感目录只给个数
}

type ProjectFolder struct {
	Name    string  `json:"name"`
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

func layoutFolders(labels []string) []string {
	var folders []string
	seen := map[string]bool{}
	for _, label := range labels {
		if w, e := worktype.Resolve(label); e == nil {
			for _, folder := range worktype.LayoutFor(w.Template).Folders {
				if !seen[folder] {
					folders = append(folders, folder)
					seen[folder] = true
				}
			}
		}
	}
	return folders
}

func projectLayoutFolders(dir string, labels []string) []string {
	if p, err := worktype.ReadProfile(filepath.Join(dir, worktype.ProfileFile)); err == nil {
		return p.Folders
	}
	return layoutFolders(labels)
}

// NoWorkType 是没标工种的任务放的栏。
const NoWorkType = "未标工种"

var splitWT = regexp.MustCompile(`[、,，;；/]+`)

// ProjectWorkTypes 读项目 00-说明.md 里所有「工种：」行。
func ProjectWorkTypes(readme string) []string {
	b, err := os.ReadFile(readme)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		g := noteLine.FindStringSubmatch(line)
		if g == nil || g[1] != "工种" {
			continue
		}
		v := g[2]
		// Catalog labels may contain '/' or parentheses. Protect those labels
		// before accepting the legacy slash-separated/comment notation.
		protected := map[string]string{}
		for i, wt := range worktype.All() {
			if strings.Contains(v, wt.Label()) {
				token := fmt.Sprintf("\x00worktype%d\x00", i)
				v = strings.ReplaceAll(v, wt.Label(), token)
				protected[token] = wt.Label()
			}
		}
		if i := strings.IndexAny(v, "（("); i > 0 {
			v = v[:i]
		}
		for _, w := range splitWT.Split(v, -1) {
			w = strings.TrimSpace(strings.Trim(w, "`*"))
			if original, ok := protected[w]; ok {
				w = original
			}
			if w != "" && !seen[w] && w != "（待补）" {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// Projects 列出总文件夹里的项目（不含总控和点目录），最近改过的在前。
func Projects(e Env) []ProjectSummary {
	e.defaults()
	n := e.Config.Names
	f := loadFacts(&e, time.Time{})
	ents, _ := os.ReadDir(e.Config.Root)
	var out []ProjectSummary
	for _, en := range ents {
		name := en.Name()
		if strings.HasPrefix(name, ".") || name == n.Control {
			continue
		}
		dir := filepath.Join(e.Config.Root, name)
		linked := en.Type()&os.ModeSymlink != 0
		if linked {
			fi, err := os.Stat(dir)
			if err != nil || !fi.IsDir() {
				continue
			}
		} else if !en.IsDir() {
			continue
		}
		ps := ProjectSummary{Source: SourceRoot, Name: name, Path: dir, WorkTypes: ProjectWorkTypes(filepath.Join(dir, n.Readme))}
		if linked {
			ps.Linked = true
			ps.LinkTarget, _ = filepath.EvalSymlinks(dir)
		}
		latest := time.Time{}
		if fi, err := en.Info(); err == nil {
			latest = fi.ModTime()
		}
		if ts, err := os.ReadDir(filepath.Join(dir, n.Tasks)); err == nil {
			for _, t := range ts {
				if t.IsDir() && !strings.HasPrefix(t.Name(), ".") && !strings.HasPrefix(t.Name(), "_") {
					ps.Tasks++
					if fi, err := t.Info(); err == nil && fi.ModTime().After(latest) {
						latest = fi.ModTime()
					}
				}
			}
		}
		outDir := filepath.Join(dir, n.Outputs)
		filepath.WalkDir(outDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != outDir && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
				return nil
			}
			ps.Outputs++
			if f.statusOf(p) == "待批准" {
				ps.Pending++
			}
			if fi, err := d.Info(); err == nil && fi.ModTime().After(latest) {
				latest = fi.ModTime()
			}
			return nil
		})
		if !latest.IsZero() {
			ps.ModTime = latest.In(e.Config.Location()).Format(time.RFC3339)
			ps.ModUnix = latest.Unix()
		}
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModUnix > out[j].ModUnix })
	if e.Reg != nil {
		loc := e.Config.Location()
		for _, p := range e.Reg.Projects {
			out = append(out, ProjectSummary{Source: SourceInPlace, ID: p.ID, Name: p.Name, Path: p.Path, Status: p.Status,
				Category: p.Category, Members: len(p.Members) - 1, Aliases: p.Aliases, Origin: p.Origin,
				ModTime: p.ModTime.In(loc).Format(time.RFC3339), ModUnix: p.ModTime.Unix()})
		}
	}
	return out
}

// PlaceDetail 是原地项目的详情：在哪、有哪些副本、最近改了什么。
type PlaceDetail struct {
	Project    registry.Project `json:"project"`
	Readme     string           `json:"readme"`
	ReadmeName string           `json:"readmeName"`
	Recent     []Item           `json:"recent"`
}

// PlaceProject 读一个原地项目（只读）。
func PlaceProject(e Env, id string) (PlaceDetail, error) {
	e.defaults()
	p := e.Reg.ByID(id)
	if p == nil {
		return PlaceDetail{}, os.ErrNotExist
	}
	d := PlaceDetail{Project: *p}
	for _, n := range []string{e.Config.Names.Readme, "README.md", "readme.md", "AGENTS.md", "CLAUDE.md"} {
		if b, err := os.ReadFile(filepath.Join(p.Path, n)); err == nil {
			if len(b) > 8000 {
				b = b[:8000]
			}
			d.Readme, d.ReadmeName = string(b), n
			break
		}
	}
	f := loadFacts(&e, time.Time{})
	type hit struct {
		path string
		fi   os.FileInfo
	}
	var hits []hit
	var rs []walkRoot
	for _, dir := range p.Dirs() {
		rs = append(rs, walkRoot{dir, 6})
	}
	e.walk(rs, func(path string, fi os.FileInfo) bool {
		hits = append(hits, hit{path, fi})
		return true
	})
	for _, m := range p.Members {
		if !m.IsDir {
			if fi, err := os.Stat(m.Path); err == nil && fi.Mode().IsRegular() {
				hits = append(hits, hit{m.Path, fi})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].fi.ModTime().After(hits[j].fi.ModTime()) })
	if len(hits) > 60 {
		hits = hits[:60]
	}
	for _, h := range hits {
		d.Recent = append(d.Recent, f.describe(h.path, h.fi, false))
	}
	return d, nil
}

func listDir(dir string, loc *time.Location, f *facts) []Entry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, en := range ents {
		name := en.Name()
		if strings.HasPrefix(name, ".") || en.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(dir, name)
		x := Entry{Name: name, Path: p, IsDir: en.IsDir()}
		if fi, err := en.Info(); err == nil {
			x.ModTime = fi.ModTime().In(loc).Format(time.RFC3339)
			if !en.IsDir() {
				x.Size = fi.Size()
			}
		}
		if f != nil && !en.IsDir() {
			x.Status = f.statusOf(p)
			if r, ok := f.recs[p]; ok {
				x.Agent = r.Agent
			} else if en, ok := f.idx[p]; ok {
				x.Agent = en["负责智能体"]
			}
		}
		out = append(out, x)
	}
	return out
}

// Project 读一个项目的详情：任务按工种分栏，成品挂在同名任务下。
func Project(e Env, name string) (ProjectDetail, error) {
	e.defaults()
	n := e.Config.Names
	loc := e.Config.Location()
	dir := filepath.Join(e.Config.Root, name)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || name == n.Control || strings.ContainsAny(name, `/\`) {
		if err == nil {
			err = os.ErrNotExist
		}
		return ProjectDetail{}, err
	}
	f := loadFacts(&e, time.Time{})
	d := ProjectDetail{Name: name, Path: dir}
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		d.Linked = true
		d.LinkTarget, _ = filepath.EvalSymlinks(dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, n.Readme)); err == nil {
		if len(b) > 8000 {
			b = b[:8000]
		}
		d.Readme = string(b)
	}
	d.WorkTypes = ProjectWorkTypes(filepath.Join(dir, n.Readme))
	for _, folder := range projectLayoutFolders(dir, d.WorkTypes) {
		path := filepath.Join(dir, folder)
		if fi, err := os.Lstat(path); err == nil && fi.IsDir() {
			d.Folders = append(d.Folders, ProjectFolder{Name: folder, Path: path, Entries: listDir(path, loc, f)})
		}
	}

	// 成品：成品/<任务目录名>/... 或直接放在 成品/ 下。
	outByTask := map[string][]Entry{}
	var looseOutputs []Entry
	for _, o := range listDir(filepath.Join(dir, n.Outputs), loc, f) {
		if strings.HasPrefix(o.Name, "_") {
			continue
		}
		if o.IsDir {
			outByTask[o.Name] = append(outByTask[o.Name], listDir(o.Path, loc, f)...)
		} else {
			looseOutputs = append(looseOutputs, o)
		}
	}

	cols := map[string]*Column{}
	var order []string
	add := func(t TaskInfo) {
		k := nz(t.WorkType, NoWorkType)
		c, ok := cols[k]
		if !ok {
			c = &Column{WorkType: k, Template: t.Template}
			cols[k] = c
			order = append(order, k)
		}
		if c.Template == "" {
			c.Template = t.Template
		}
		c.Tasks = append(c.Tasks, t)
	}
	for _, wt := range d.WorkTypes { // 说明里登记过的工种，没任务也给一栏
		if _, ok := cols[wt]; !ok {
			cols[wt] = &Column{WorkType: wt}
			order = append(order, wt)
		}
	}
	tasksDir := filepath.Join(dir, n.Tasks)
	for _, t := range listDir(tasksDir, loc, nil) {
		if !t.IsDir || strings.HasPrefix(t.Name, "_") {
			continue
		}
		m := f.taskNote(t.Path)
		ti := TaskInfo{Name: t.Name, Path: t.Path, WorkType: m["工种"], Template: m["模板"], Status: m["状态"],
			Agent: m["负责智能体"], Note: m["一句话说明"], ModTime: t.ModTime, Outputs: outByTask[t.Name]}
		delete(outByTask, t.Name)
		cnt := 0
		var latest time.Time
		filepath.WalkDir(t.Path, func(p string, de os.DirEntry, err error) error {
			if err == nil && !de.IsDir() && !strings.HasPrefix(de.Name(), ".") && de.Name() != "说明.md" {
				cnt++
				if fi, e2 := de.Info(); e2 == nil && fi.ModTime().After(latest) {
					latest = fi.ModTime()
				}
				if ti.WorkType == "" || ti.Agent == "" {
					if en, ok := f.idx[p]; ok {
						ti.WorkType = nz(ti.WorkType, en["工种"])
						ti.Template = nz(ti.Template, en["模板"])
						ti.Agent = nz(ti.Agent, en["负责智能体"])
					} else if r, ok := f.recs[p]; ok {
						ti.WorkType = nz(ti.WorkType, r.WorkType)
						ti.Agent = nz(ti.Agent, r.Agent)
					}
				}
			}
			return nil
		})
		ti.Files = cnt
		if !latest.IsZero() {
			ti.ModUnix = latest.Unix()
			ti.ModTime = latest.In(loc).Format(time.RFC3339)
		}
		ti.Status = deriveStatus(ti)
		add(ti)
	}
	// 成品里有、任务里没有的，单独成卡。
	var orphan []string
	for k := range outByTask {
		orphan = append(orphan, k)
	}
	sort.Strings(orphan)
	for _, k := range orphan {
		ti := TaskInfo{Name: k, Path: filepath.Join(dir, n.Outputs, k), Outputs: outByTask[k], Note: "只有成品，没有对应任务文件夹"}
		ti.Status = deriveStatus(ti)
		add(ti)
	}
	if len(looseOutputs) > 0 {
		add(TaskInfo{Name: "（直接放在成品下）", Path: filepath.Join(dir, n.Outputs), Outputs: looseOutputs, Status: deriveStatus(TaskInfo{Outputs: looseOutputs})})
	}
	for _, k := range order {
		c := cols[k]
		sort.SliceStable(c.Tasks, func(i, j int) bool { return c.Tasks[i].Name > c.Tasks[j].Name })
		d.Columns = append(d.Columns, *c)
	}
	sort.SliceStable(d.Columns, func(i, j int) bool { // 未标工种放最后
		return d.Columns[i].WorkType != NoWorkType && d.Columns[j].WorkType == NoWorkType
	})

	matDir := filepath.Join(dir, n.Materials)
	for _, m := range listDir(matDir, loc, f) {
		if m.Name == n.Sensitive {
			filepath.WalkDir(m.Path, func(p string, de os.DirEntry, err error) error {
				if err == nil && !de.IsDir() {
					d.Sensitive++
				}
				return nil
			})
			continue
		}
		d.Materials = append(d.Materials, m)
	}
	d.Code = listDir(filepath.Join(dir, n.Code), loc, nil)
	for _, x := range listDir(dir, loc, f) {
		if x.IsDir || x.Name == n.Readme {
			continue
		}
		d.Loose = append(d.Loose, x)
	}
	return d, nil
}

func deriveStatus(t TaskInfo) string {
	pending, done := 0, 0
	for _, o := range t.Outputs {
		switch o.Status {
		case "待批准":
			pending++
		case "已交":
			done++
		}
	}
	switch {
	case pending > 0:
		return "待批准"
	case t.Status != "":
		if i := strings.Index(t.Status, "（"); i > 0 {
			return t.Status[:i] // 「已交（批准于 …）」只取「已交」
		}
		return t.Status
	case done > 0:
		return "已交"
	case len(t.Outputs) > 0:
		return "有成品"
	}
	return "进行中"
}
