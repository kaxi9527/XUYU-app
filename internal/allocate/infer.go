package allocate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/shelf"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

const (
	SrcReadme = "说明"
	SrcInfer  = "推断"

	ConfHigh = "较有把握"
	ConfMid  = "大致判断"
	ConfLow  = "待你定"
)

// Inferred 是认出的一个工种（已标或推断）。
type Inferred struct {
	Label           string `json:"label"`
	Source          string `json:"source"`     // 说明 / 推断
	Confidence      string `json:"confidence"` // 较有把握 / 大致判断 / 待你定
	Reason          string `json:"reason"`
	Template        string `json:"template,omitempty"`
	Sensitive       bool   `json:"sensitive,omitempty"`
	AlreadyInReadme bool   `json:"alreadyInReadme"`
	PreConfirm      bool   `json:"preConfirm"` // 是否建议写入说明；待你定一律 false
}

// InferOptions 控制推断范围。
type InferOptions struct {
	ProjectDir string
	ReadmeName string // 默认 00-说明.md
	MaxEntries int    // 最多扫多少条目录项，默认 400
}

// InferProject 读说明里的工种，再按路径/文件名/扩展名/清单文件启发式推断。
// 拿不准的标「待你定」，PreConfirm=false，调用方不要自动写入说明。
func InferProject(opt InferOptions) []Inferred {
	if opt.ReadmeName == "" {
		opt.ReadmeName = "00-说明.md"
	}
	if opt.MaxEntries <= 0 {
		opt.MaxEntries = 400
	}
	dir := filepath.Clean(opt.ProjectDir)
	readme := filepath.Join(dir, opt.ReadmeName)

	var out []Inferred
	seen := map[string]bool{}
	add := func(in Inferred) {
		lab := strings.TrimSpace(in.Label)
		if lab == "" || seen[lab] {
			return
		}
		seen[lab] = true
		if in.Confidence == ConfLow {
			in.PreConfirm = false
		}
		out = append(out, in)
	}

	for _, lab := range shelf.ProjectWorkTypes(readme) {
		wt, err := worktype.Resolve(lab)
		in := Inferred{
			Label: lab, Source: SrcReadme, Confidence: ConfHigh,
			Reason: "写在项目说明里", AlreadyInReadme: true, PreConfirm: false,
		}
		if err == nil {
			in.Label = wt.Label()
			in.Template = wt.Template
			in.Sensitive = wt.Sensitive
			in.Reason = "写在项目说明里（" + wt.Industry + "·" + wt.Name + "）"
		}
		add(in)
	}

	// 收集名字线索：项目文件夹名 + 浅层子项名
	baseName := filepath.Base(dir)
	var names []string
	names = append(names, baseName)
	kindHits := map[string]int{} // template → count
	codeHit := false
	uiHit, videoHit, modelHit := false, false, false
	entries := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || entries >= opt.MaxEntries {
			if entries >= opt.MaxEntries {
				return filepath.SkipAll
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		// 跳过深层与常见噪音
		depth := strings.Count(rel, string(os.PathSeparator))
		name := d.Name()
		if strings.HasPrefix(name, ".") && name != ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		low := strings.ToLower(name)
		if d.IsDir() && (low == "node_modules" || low == "dist" || low == "build" || low == ".git" || low == "__pycache__" || low == "vendor") {
			if low == ".git" {
				codeHit = true
			}
			return filepath.SkipDir
		}
		if depth > 3 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		names = append(names, name)
		if isCodeMarkerName(name) {
			codeHit = true
		}
		if !d.IsDir() {
			switch strings.ToLower(filepath.Ext(name)) {
			case ".fig", ".sketch":
				uiHit = true
			case ".prproj", ".aep", ".drp", ".fcpxml":
				videoHit = true
			case ".blend", ".c4d", ".max":
				modelHit = true
			}
			k := KindOfPath(p)
			for _, t := range TemplateForKind(k) {
				kindHits[t]++
			}
		}
		return nil
	})

	hay := strings.ToLower(strings.Join(names, " "))
	hayCN := strings.Join(names, " ")

	// 工程与可编辑源文件比样例文件名更可靠；项目说明仍然拥有最高优先级。
	for _, hint := range []struct {
		hit           bool
		label, reason string
	}{{codeHit, "开发·功能开发", "项目里有 go.mod / package.json / .git 等代码标志"}, {uiHit, "设计·UI原型", "包含 Figma/Sketch 原型源文件"}, {videoHit, "视频剪辑·剪辑成片", "包含剪辑工程文件"}, {modelHit, "设计·3D模型渲染", "包含三维建模工程文件"}} {
		if hint.hit {
			if w, err := worktype.Resolve(hint.label); err == nil {
				add(Inferred{Label: w.Label(), Source: SrcInfer, Confidence: ConfHigh, Reason: hint.reason, Template: w.Template, PreConfirm: true})
			}
		}
	}

	// 工种名 / 合并名 / 行业 直接撞上
	for _, w := range worktype.All() {
		label := w.Label()
		if seen[label] {
			continue
		}
		score := 0
		reason := ""
		if strings.Contains(hayCN, w.Name) || strings.Contains(hay, strings.ToLower(w.Name)) {
			score += 3
			reason = "路径/文件夹名里出现工种「" + w.Name + "」"
		}
		if w.Merged != "" && w.Merged != w.Name && (strings.Contains(hayCN, w.Merged) || strings.Contains(hay, strings.ToLower(w.Merged))) {
			score += 2
			if reason == "" {
				reason = "路径里出现合并名「" + w.Merged + "」"
			}
		}
		if strings.Contains(hayCN, w.Industry) {
			score += 1
			if reason == "" {
				reason = "路径里出现行业「" + w.Industry + "」"
			}
		}
		if score >= 3 {
			conf := ConfMid
			pre := true
			if score >= 4 {
				conf = ConfHigh
			}
			if w.Sensitive && score < 4 {
				conf = ConfLow
				pre = false
			}
			add(Inferred{
				Label: label, Source: SrcInfer, Confidence: conf, Reason: reason,
				Template: w.Template, Sensitive: w.Sensitive, PreConfirm: pre,
			})
		}
	}

	// 扩展名多数派 → 对应模板的最贴工种（优先已有行业线索，否则表内第一个）
	bestT, bestN := "", 0
	var templates []string
	for t := range kindHits {
		templates = append(templates, t)
	}
	sort.Strings(templates)
	for _, t := range templates {
		n := kindHits[t]
		if n > bestN {
			bestT, bestN = t, n
		}
	}
	if bestT != "" && bestN >= 2 {
		cand := pickByTemplate(bestT, hayCN)
		if cand.Name != "" && !seen[cand.Label()] {
			// File format alone is not evidence of a business role.
			conf, pre := ConfLow, false
			add(Inferred{
				Label: cand.Label(), Source: SrcInfer, Confidence: conf,
				Reason:   "多数文件像「" + worktype.KindOf(bestT) + "」（模板 " + bestT + "，约 " + itoa(bestN) + " 个），贴近「" + cand.Label() + "」",
				Template: cand.Template, Sensitive: cand.Sensitive, PreConfirm: pre && !cand.Sensitive,
			})
		}
	}

	return out
}

func pickByTemplate(tmpl, hay string) worktype.WorkType {
	var hits []worktype.WorkType
	for _, w := range worktype.All() {
		if strings.EqualFold(w.Template, tmpl) {
			hits = append(hits, w)
		}
	}
	if len(hits) == 0 {
		return worktype.WorkType{}
	}
	// 行业名撞上优先
	for _, w := range hits {
		if strings.Contains(hay, w.Industry) {
			return w
		}
	}
	// Do not pick the first catalog entry (e.g. images ≠ e-commerce main images).
	return worktype.WorkType{}
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
