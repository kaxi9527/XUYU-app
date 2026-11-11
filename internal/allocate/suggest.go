package allocate

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// Suggestion 是一条「文件该进哪」的建议。
type Suggestion struct {
	Source     string `json:"source"`
	Project    string `json:"project"`
	Area       string `json:"area"` // 任务/成品/资料/代码/散落/敏感
	Task       string `json:"task,omitempty"`
	WorkType   string `json:"workType,omitempty"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"` // 较有把握 / 大致判断 / 待你定
	PreCheck   bool   `json:"preCheck"`
	Skip       bool   `json:"skip,omitempty"`
	SkipReason string `json:"skipReason,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// SuggestOptions 控制按工种整理的范围。
type SuggestOptions struct {
	Config      config.Config
	Project     string // 总文件夹下的项目名
	ProjectDir  string // 可空：默认 Root/Project；原地项目可直接给绝对路径
	WorkTypes   []string
	MaxFiles    int
	IncludeRoot bool // 扫项目根部散落文件（默认 true）
}

// SuggestProject 给出项目内（散落、未归类、资料/散落文件）的分配预览。
func SuggestProject(opt SuggestOptions) ([]Suggestion, error) {
	cfg := opt.Config
	n := cfg.Names
	proj := strings.TrimSpace(opt.Project)
	dir := strings.TrimSpace(opt.ProjectDir)
	if dir == "" {
		if proj == "" {
			return nil, nil
		}
		dir = filepath.Join(cfg.Root, proj)
	}
	if proj == "" {
		proj = filepath.Base(dir)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return nil, err
	}
	max := opt.MaxFiles
	if max <= 0 {
		max = 200
	}
	wts := opt.WorkTypes
	if len(wts) == 0 {
		wts = shelf.ProjectWorkTypes(filepath.Join(dir, n.Readme))
	}
	// 推断补全（只用来匹配文件，不写说明）
	inferred := InferProject(InferOptions{ProjectDir: dir, ReadmeName: n.Readme})
	for _, in := range inferred {
		if in.Source == SrcInfer && in.Confidence != ConfLow {
			wts = appendUnique(wts, in.Label)
		}
	}

	var paths []string
	seen := map[string]bool{}
	addPath := func(p string) {
		p = filepath.Clean(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}

	// 1) 资料/散落文件/*
	looseDir := filepath.Join(dir, n.Materials, n.Loose)
	_ = filepath.WalkDir(looseDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || len(paths) >= max {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		addPath(p)
		return nil
	})

	// 2) 项目根部的散落文件（非目录、非说明）
	if opt.IncludeRoot || true {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if len(paths) >= max {
				break
			}
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == n.Readme {
				continue
			}
			addPath(filepath.Join(dir, e.Name()))
		}
	}

	// 3) 资料/ 下一层文件（不含敏感/散落子目录本身内容已扫）
	matDir := filepath.Join(dir, n.Materials)
	if ents, err := os.ReadDir(matDir); err == nil {
		for _, e := range ents {
			if len(paths) >= max {
				break
			}
			name := e.Name()
			if name == n.Sensitive || name == n.Loose || strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				continue // 资料下已有子文件夹先不动，避免误搬「品牌规范」等整理好的
			}
			addPath(filepath.Join(matDir, name))
		}
	}

	var out []Suggestion
	for _, p := range paths {
		s := ClassifyFile(ClassifyOptions{
			Path: p, Project: proj, ProjectDir: dir, WorkTypes: wts, Names: n,
		})
		out = append(out, s)
	}
	return out, nil
}

// ClassifyOptions 单文件分类。
type ClassifyOptions struct {
	Path       string
	Project    string
	ProjectDir string
	WorkTypes  []string
	Names      config.Names
}

// ClassifyFile 按工种规则给出一条建议（不搬文件）。
func ClassifyFile(opt ClassifyOptions) Suggestion {
	p := filepath.Clean(opt.Path)
	s := Suggestion{Source: p, Project: opt.Project, Kind: KindOfPath(p)}
	base := filepath.Base(p)
	if scan.IsSensitiveName(base) {
		s.Skip = true
		s.SkipReason = "文件名像密钥/凭据，拒绝归位"
		s.Confidence = ConfHigh
		s.Reason = s.SkipReason
		s.PreCheck = false
		return s
	}

	// 已在正确栏位？
	if opt.ProjectDir != "" {
		if area, ok := alreadyPlaced(p, opt.ProjectDir, opt.Names); ok {
			s.Skip = true
			s.SkipReason = "已在「" + area + "」里，跳过"
			s.Area = area
			s.Confidence = ConfHigh
			s.Reason = s.SkipReason
			s.PreCheck = false
			return s
		}
	}

	wts := resolvedWTs(opt.WorkTypes)
	kind := s.Kind

	// 目录：看是不是代码树
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		if dirLooksLikeCode(p) {
			s.Area = home.AreaCode
			s.WorkType = firstLabel(wts, "T09")
			s.Reason = "文件夹里有代码标志，进「代码」"
			s.Confidence = ConfHigh
			s.PreCheck = true
			return s
		}
		// 不确定的文件夹 → 散落待你定
		s.Area = home.AreaLoose
		s.Reason = "文件夹用途拿不准，先放「资料/散落文件」，请你定"
		s.Confidence = ConfLow
		s.PreCheck = false
		return s
	}

	match := matchWT(wts, kind)
	sensWT := match.ok && match.wt.Sensitive

	switch kind {
	case KindCode:
		s.Area = home.AreaCode
		s.WorkType = labelOr(match, "开发·功能开发")
		s.Reason = "代码文件，进「代码」"
		s.Confidence = ConfHigh
		s.PreCheck = true
	case KindImage:
		if match.ok {
			s.WorkType = match.wt.Label()
			// 成品感：文件名含「主图/成片/交付/定稿/final」→ 成品；否则任务
			if nameLooksFinal(base) {
				s.Area = home.AreaOutputs
				s.Task = taskName(match.wt)
				s.Reason = "图片，项目有「" + s.WorkType + "」，文件名像定稿，进「成品」"
				s.Confidence = ConfMid
			} else {
				s.Area = home.AreaTasks
				s.Task = taskName(match.wt)
				s.Reason = "图片，项目有「" + s.WorkType + "」，进该工种「任务」"
				s.Confidence = ConfMid
			}
			s.PreCheck = true
		} else {
			s.Area = home.AreaMaterials
			s.Reason = "图片，但项目没标主图/生图/设计类工种，先放「资料」"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	case KindVideo:
		if match.ok {
			s.WorkType = match.wt.Label()
			if nameLooksFinal(base) {
				s.Area = home.AreaOutputs
				s.Reason = "视频，项目有「" + s.WorkType + "」，文件名像成片，进「成品」"
			} else {
				s.Area = home.AreaTasks
				s.Reason = "视频，项目有「" + s.WorkType + "」，进「任务」"
			}
			s.Task = taskName(match.wt)
			s.Confidence = ConfMid
			s.PreCheck = true
		} else {
			s.Area = home.AreaMaterials
			s.Reason = "视频，项目没标剪辑类工种，先放「资料」"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	case KindAudio:
		if match.ok {
			s.WorkType = match.wt.Label()
			s.Area = home.AreaTasks
			s.Task = taskName(match.wt)
			s.Reason = "音频，按「" + s.WorkType + "」进「任务」"
			s.Confidence = ConfMid
			s.PreCheck = true
		} else {
			s.Area = home.AreaMaterials
			s.Reason = "音频，先放「资料」"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	case KindDoc, KindSheet:
		if match.ok {
			s.WorkType = match.wt.Label()
			if nameLooksFinal(base) {
				s.Area = home.AreaMaterials
				s.Reason = "文档/表格，像参考资料，进「资料」（工种 " + s.WorkType + "）"
			} else {
				s.Area = home.AreaTasks
				s.Task = taskName(match.wt)
				s.Reason = "文档/表格，按「" + s.WorkType + "」进「任务」（过程稿）"
			}
			s.Confidence = ConfMid
			s.PreCheck = !match.wt.Sensitive
			if match.wt.Sensitive {
				s.Area = home.AreaSensitive
				s.Task = ""
				s.Reason = "对应工种敏感，进「资料/敏感」"
				s.Confidence = ConfMid
				s.PreCheck = true
			}
		} else {
			s.Area = home.AreaLoose
			s.Reason = "文档/表格，对不上已有工种，先放散落，请你定"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	case KindWeb:
		if match.ok {
			s.WorkType = match.wt.Label()
			s.Area = home.AreaTasks
			s.Task = taskName(match.wt)
			s.Reason = "网页相关，按「" + s.WorkType + "」进「任务」"
			s.Confidence = ConfMid
			s.PreCheck = true
		} else {
			s.Area = home.AreaMaterials
			s.Reason = "网页文件，先放「资料」"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	default:
		if sensWT {
			s.Area = home.AreaSensitive
			s.WorkType = match.wt.Label()
			s.Reason = "工种「" + s.WorkType + "」敏感，进「资料/敏感」"
			s.Confidence = ConfMid
			s.PreCheck = true
		} else {
			s.Area = home.AreaLoose
			s.Reason = "类型拿不准，先放「资料/散落文件」，请你定"
			s.Confidence = ConfLow
			s.PreCheck = false
		}
	}

	if s.Confidence == ConfLow {
		s.PreCheck = false
	}
	return s
}

// ToJobs 把勾选的建议收成 home.Job（跳过 Skip）。
func ToJobs(ss []Suggestion) []home.Job {
	var jobs []home.Job
	for _, s := range ss {
		if s.Skip || s.Source == "" || s.Project == "" || s.Area == "" {
			continue
		}
		jobs = append(jobs, home.Job{
			Source: s.Source, Project: s.Project, Area: s.Area, Task: s.Task,
		})
	}
	return jobs
}

// JobsForItems 给一键归位用：有项目上下文时按工种分，不再默认全塞散落。
func JobsForItems(cfg config.Config, items []shelf.Item) (jobs []home.Job, skipped []string) {
	wtCache := map[string][]string{}
	dirCache := map[string]string{}
	n := cfg.Names
	for _, it := range items {
		switch it.Placement {
		case shelf.PlacedWrong, shelf.PlacedOutside, shelf.PlacedLoose, shelf.PlacedInPlace:
		default:
			skipped = append(skipped, it.Path+"（已在规定位置："+it.Placement+"）")
			continue
		}
		if it.Project == "" {
			skipped = append(skipped, it.Path+"（不知道属于哪个项目，请先指定）")
			continue
		}
		projDir, ok := dirCache[it.Project]
		if !ok {
			projDir = filepath.Join(cfg.Root, it.Project)
			dirCache[it.Project] = projDir
		}
		wts, ok := wtCache[it.Project]
		if !ok {
			wts = shelf.ProjectWorkTypes(filepath.Join(projDir, n.Readme))
			// 原地项目：路径不在 Root 下时，尝试用 item 的项目路径
			if len(wts) == 0 && it.InPlace && it.Dir != "" {
				// climb to project root heuristically: Owner path not here; use Root anyway
			}
			inferred := InferProject(InferOptions{ProjectDir: projDir, ReadmeName: n.Readme, MaxEntries: 120})
			for _, in := range inferred {
				if in.Confidence != ConfLow {
					wts = appendUnique(wts, in.Label)
				}
			}
			wtCache[it.Project] = wts
		}
		s := ClassifyFile(ClassifyOptions{
			Path: it.Path, Project: it.Project, ProjectDir: projDir, WorkTypes: wts, Names: n,
		})
		if s.Skip {
			skipped = append(skipped, it.Path+"（"+s.SkipReason+"）")
			continue
		}
		jobs = append(jobs, home.Job{Source: s.Source, Project: s.Project, Area: s.Area, Task: s.Task})
	}
	return jobs, skipped
}

type wtHit struct {
	ok bool
	wt worktype.WorkType
}

func resolvedWTs(labels []string) []worktype.WorkType {
	var out []worktype.WorkType
	seen := map[string]bool{}
	for _, lab := range labels {
		w, err := worktype.Resolve(lab)
		if err != nil {
			continue
		}
		if seen[w.Label()] {
			continue
		}
		seen[w.Label()] = true
		out = append(out, w)
	}
	return out
}

func matchWT(wts []worktype.WorkType, kind string) wtHit {
	prefs := TemplateForKind(kind)
	if len(prefs) == 0 || len(wts) == 0 {
		return wtHit{}
	}
	for _, t := range prefs {
		for _, w := range wts {
			if strings.EqualFold(w.Template, t) {
				return wtHit{ok: true, wt: w}
			}
			for _, a := range w.Aux {
				if strings.EqualFold(a, t) {
					return wtHit{ok: true, wt: w}
				}
			}
		}
	}
	return wtHit{}
}

func firstLabel(wts []worktype.WorkType, tmpl string) string {
	for _, w := range wts {
		if strings.EqualFold(w.Template, tmpl) {
			return w.Label()
		}
	}
	if w, err := worktype.Resolve("开发·功能开发"); err == nil {
		return w.Label()
	}
	return ""
}

func labelOr(h wtHit, fallback string) string {
	if h.ok {
		return h.wt.Label()
	}
	if w, err := worktype.Resolve(fallback); err == nil {
		return w.Label()
	}
	return fallback
}

func taskName(w worktype.WorkType) string {
	// 子目录名：按工种整理-工种名（CheckName 友好）
	name := "按工种整理-" + w.Name
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, name)
	if len([]rune(name)) > 60 {
		rs := []rune(name)
		name = string(rs[:60])
	}
	return name
}

func nameLooksFinal(base string) bool {
	n := strings.ToLower(base)
	for _, k := range []string{"交付", "定稿", "成片", "final", "交付版", "主图", "封面", "hero", "发布"} {
		if strings.Contains(n, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func alreadyPlaced(path, projDir string, n config.Names) (string, bool) {
	rel, err := filepath.Rel(projDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) == 0 {
		return "", false
	}
	top := parts[0]
	switch top {
	case n.Tasks:
		return home.AreaTasks, true
	case n.Outputs:
		return home.AreaOutputs, true
	case n.Code:
		return home.AreaCode, true
	case n.Materials:
		if len(parts) >= 2 && parts[1] == n.Sensitive {
			return home.AreaSensitive, true
		}
		// 资料/散落文件 与 资料/ 下一层散文件都是整理对象，不算「已放对」
		return "", false
	}
	return "", false
}

func dirLooksLikeCode(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if isCodeMarkerName(e.Name()) {
			return true
		}
	}
	return false
}

func appendUnique(ss []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ss
	}
	for _, s := range ss {
		if s == v {
			return ss
		}
	}
	return append(ss, v)
}
