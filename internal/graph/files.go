package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// Files 构建某个项目的文件关系网（只读）。
// 默认从指定项目切入；限节点/哈希/边数，超时跳过并注明。
func Files(e shelf.Env, opt FileOpts) FileGraphResult {
	e.Config.Validate()
	opt.defaults()
	deadline := time.Now().Add(4 * time.Second)
	if e.Deadline > 0 {
		deadline = time.Now().Add(e.Deadline)
	}

	projName, projDir, note, projID := resolveProject(e, opt)
	res := FileGraphResult{Project: projName, ProjectID: projID, Note: note}
	if projDir == "" {
		if opt.Project != "" || opt.ProjectID != "" {
			res.Skipped = "找不到这个项目（总文件夹里没有同名目录，登记表里也没有）"
		} else {
			res.Skipped = "没指定项目，也没有可切入的最近文件"
		}
		return res
	}

	nNames := e.Config.Names
	// The project may mix standard folders with a profession/custom template.
	// Inspect the real root once; hidden/generated paths retain existing skips.
	roots := []string{projDir}
	res.ProjectKey = "root:" + projName
	if projID != "" {
		res.ProjectKey = "place:" + projID
	}

	type fnode struct {
		path, dir, name string
		size            int64
		isDir           bool
		attach          bool // 项目外挂靠（桌面/下载等）
		via             string
	}

	// 先收挂靠候选：展开时要预留名额，否则项目内文件会先占满 MaxFiles，外面的永远进不来。
	type attachCand struct {
		path, via string
		isDir     bool
	}
	var attachCands []attachCand
	if e.Reg != nil {
		for _, rp := range e.Reg.Projects {
			if filepath.Clean(rp.Path) != filepath.Clean(projDir) && rp.Name != projName {
				continue
			}
			for _, m := range rp.Members {
				if !isAttachMember(rp.Path, m) {
					continue
				}
				attachCands = append(attachCands, attachCand{path: filepath.Clean(m.Path), via: m.Via, isDir: m.IsDir})
			}
		}
	}
	attachN := len(attachCands)
	res.AttachCount = attachN
	reserve := 0
	if opt.ExpandAttach && attachN > 0 {
		reserve = attachN
		if reserve > opt.MaxFiles/3 {
			reserve = opt.MaxFiles / 3
		}
		if reserve > 40 {
			reserve = 40
		}
		if reserve < 1 {
			reserve = 1
		}
	}
	walkLimit := opt.MaxFiles - reserve
	if walkLimit < 8 {
		walkLimit = opt.MaxFiles / 2
		if walkLimit < 8 {
			walkLimit = 8
		}
	}

	var files []fnode
	seenPath := map[string]bool{}
	walkDone := false
	depthSkipped, policySkipped, unreadable := 0, 0, 0
	for _, root := range roots {
		if time.Now().After(deadline) || len(files) >= walkLimit {
			walkDone = true
			break
		}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				unreadable++
				return nil
			}
			if time.Now().After(deadline) || len(files) >= walkLimit {
				walkDone = true
				return filepath.SkipAll
			}
			name := d.Name()
			if d.IsDir() && p != root && scan.SkipDirName(name) {
				policySkipped++
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				if d.IsDir() && p != root {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			// Scan custom templates within an explicit depth/time/node budget.
			rel, _ := filepath.Rel(root, p)
			depth := 0
			if rel != "." {
				depth = strings.Count(rel, string(os.PathSeparator)) + 1
			}
			if d.IsDir() {
				if p == root {
					return nil
				}
				if depth > 6 {
					depthSkipped++
					return filepath.SkipDir
				}
				// 记录任务/成品子目录
				parent := filepath.Base(filepath.Dir(p))
				if (parent == nNames.Tasks || parent == nNames.Outputs) && depth == 2 {
					cp := filepath.Clean(p)
					if !seenPath[cp] {
						seenPath[cp] = true
						files = append(files, fnode{path: cp, dir: filepath.Dir(cp), name: name, isDir: true})
					}
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if scan.IsSensitiveName(name) {
				return nil
			}
			cp := filepath.Clean(p)
			if seenPath[cp] {
				return nil
			}
			seenPath[cp] = true
			var sz int64
			if fi, err := d.Info(); err == nil {
				sz = fi.Size()
			}
			files = append(files, fnode{path: cp, dir: filepath.Dir(cp), name: name, size: sz})
			return nil
		})
	}

	// 挂靠：展开时塞进预留名额；关掉则只计数。
	attachShown := 0
	attachSkippedCap := 0
	attachDirSkipped := 0
	if opt.ExpandAttach {
		for _, c := range attachCands {
			cp := c.path
			if seenPath[cp] {
				continue
			}
			var sz int64
			isDir := c.isDir
			if fi, err := os.Stat(cp); err == nil {
				sz = fi.Size()
				isDir = fi.IsDir()
			}
			// 文件关系网默认不画文件夹点：挂靠目录先跳过，避免占名额又看不见
			if isDir && !opt.IncludeParent {
				attachDirSkipped++
				continue
			}
			if len(files) >= opt.MaxFiles {
				attachSkippedCap++
				continue
			}
			seenPath[cp] = true
			files = append(files, fnode{
				path: cp, dir: filepath.Dir(cp), name: filepath.Base(cp),
				size: sz, isDir: isDir, attach: true, via: c.via,
			})
			attachShown++
		}
	}

	nodes := make([]Node, 0, len(files))
	idOf := map[string]string{}
	attachPath := map[string]bool{}
	folderHidden := 0
	for _, f := range files {
		kind := "file"
		if f.isDir {
			kind = "folder"
			// 文件夹节点默认不画：以前「文件夹 ↔ 每个文件」是伪一对多噪声；
			// 同目录关系改由文件之间的兄弟交叉边表达。IncludeParent 时才画文件夹 + parent 边。
			if !opt.IncludeParent {
				folderHidden++
				continue
			}
		}
		id := nodeID(kind, f.path)
		idOf[f.path] = id
		extra := ""
		if f.attach {
			attachPath[f.path] = true
			extra = "项目外相关文件"
			if f.via != "" {
				extra = "项目外相关文件（经 " + f.via + "）"
			}
		}
		nodes = append(nodes, Node{
			ID: id, Kind: kind, Name: f.name, Path: f.path,
			Project: projName, ProjectID: projID, Size: f.size, Extra: extra,
		})
	}
	// 以实际进图的挂靠节点为准
	attachShown = 0
	for _, n := range nodes {
		if n.Extra != "" && strings.Contains(n.Extra, "项目外") {
			attachShown++
		}
	}
	res.AttachShown = attachShown
	if !opt.ExpandAttach {
		res.AttachHidden = attachN
	} else {
		res.AttachHidden = attachSkippedCap + attachDirSkipped
		// 目录挂靠不算「用户关掉所以看不见」，但若全是目录可在备注里提
	}

	seenEdge := map[string]bool{}
	var edges []Edge
	pathOfID := map[string]string{}
	for path, id := range idOf {
		pathOfID[id] = path
	}
	annotateAttach := func(e Edge) Edge {
		pa, pb := pathOfID[e.From], pathOfID[e.To]
		if !attachPath[pa] && !attachPath[pb] {
			return e
		}
		if strings.HasPrefix(e.Reason, "在项目外的相关文件") {
			return e
		}
		e.Reason = "在项目外的相关文件：" + e.Reason
		return e
	}
	add := func(e Edge) {
		if e.From == "" || e.To == "" || e.Type == "" {
			return
		}
		if len(edges) >= opt.MaxEdges {
			return
		}
		addEdge(&edges, seenEdge, annotateAttach(e))
	}
	// 一组相关文件 → 成对交叉边（小团两两相连；大团环+弦抽样），不再星形。
	sampledGroups := 0
	mesh := func(paths []string, mk func(a, b string) Edge) {
		ids := make([]string, 0, len(paths))
		for _, p := range paths {
			if id := idOf[p]; id != "" {
				ids = append(ids, id)
			}
		}
		pairs, sampled := meshPairs(ids, CliqueMax, opt.MaxEdges-len(edges))
		if sampled {
			sampledGroups++
		}
		for _, pr := range pairs {
			add(mk(pr[0], pr[1]))
		}
	}

	// 1) 同任务 / 同成品目录：兄弟边
	byDir := map[string][]string{}
	for _, f := range files {
		if f.isDir {
			continue
		}
		byDir[f.dir] = append(byDir[f.dir], f.path)
	}
	for _, dir := range sortedKeys(byDir) {
		ps := byDir[dir]
		if len(ps) < 2 {
			continue
		}
		base := filepath.Base(filepath.Dir(dir)) // 任务 or 成品
		parentName := filepath.Base(dir)
		label := "同目录"
		conf := ConfMid
		w := 0.7
		if base == nNames.Tasks {
			label = "同任务目录「" + parentName + "」"
			conf = ConfHigh
			w = 1
		} else if base == nNames.Outputs {
			label = "同成品目录「" + parentName + "」"
			conf = ConfHigh
			w = 1
		}
		mesh(ps, func(a, b string) Edge {
			return Edge{Type: EdgeSameDir, From: a, To: b, Reason: label, Weight: w, Confidence: conf}
		})
		// 文件夹 ↔ 文件：仅 IncludeParent 时画（parent 类型，不混进兄弟关系）
		if opt.IncludeParent {
			if dirID, ok := idOf[dir]; ok {
				for _, p := range ps {
					add(Edge{
						Type: EdgeParent, From: dirID, To: idOf[p],
						Reason: "在「" + parentName + "」里", Weight: 0.5, Confidence: ConfHigh,
					})
				}
			}
		}
	}

	// 2) 同 sha256（限数量）
	hashSkipped := false
	type cand struct {
		path string
		size int64
	}
	var hashCands []cand
	for _, f := range files {
		if f.isDir || f.size <= 0 || f.size > 8<<20 { // 跳过 >8MB
			continue
		}
		hashCands = append(hashCands, cand{f.path, f.size})
	}
	sort.Slice(hashCands, func(i, j int) bool {
		if hashCands[i].size != hashCands[j].size {
			return hashCands[i].size < hashCands[j].size
		}
		return hashCands[i].path < hashCands[j].path
	})
	if len(hashCands) > opt.MaxHash {
		hashCands = hashCands[:opt.MaxHash]
		hashSkipped = true
	}
	byHash := map[string][]string{}
	for _, c := range hashCands {
		if time.Now().After(deadline) {
			hashSkipped = true
			break
		}
		sum, err := put.FileSHA256(c.path)
		if err != nil || sum == "" {
			continue
		}
		byHash[sum] = append(byHash[sum], c.path)
	}
	dupPairs := 0
	for _, sum := range sortedKeys(byHash) {
		ps := byHash[sum]
		if len(ps) < 2 || dupPairs >= opt.MaxDupPairs {
			continue
		}
		reason := fmt.Sprintf("内容相同（sha256 %s…）", sum[:12])
		mesh(ps, func(a, b string) Edge {
			if dupPairs >= opt.MaxDupPairs {
				return Edge{}
			}
			dupPairs++
			return Edge{Type: EdgeDuplicate, From: a, To: b, Reason: reason, Weight: 1.0, Confidence: ConfHigh}
		})
	}

	// 3) activity / put 索引：同 agent、同 task、同 workType
	type meta struct {
		agent, task, wt string
	}
	metaOf := map[string]meta{}
	sources := newSourceRecords()
	// put index
	idxDir := filepath.Join(e.Config.Root, nNames.Control, nNames.IndexShards)
	for _, en := range put.ReadIndexDir(idxDir, e.Config.Root) {
		abs := en["_abs"]
		if abs == "" || idOf[filepath.Clean(abs)] == "" {
			continue
		}
		abs = filepath.Clean(abs)
		m := metaOf[abs]
		if a := en["负责智能体"]; a != "" {
			m.agent = a
			sources.add(abs, a, "资产索引")
		}
		if t := en["任务"]; t != "" {
			m.task = t
		}
		if w := firstWT(en); w != "" {
			m.wt = w
		}
		metaOf[abs] = m
	}
	// activity recent
	actDir := activity.Dir(e.Config)
	since := time.Now().Add(-30 * 24 * time.Hour)
	if recs, err := activity.ReadSince(actDir, since); err == nil {
		for _, r := range recs {
			p := filepath.Clean(r.Path)
			if idOf[p] == "" {
				continue
			}
			m := metaOf[p]
			if r.Agent != "" {
				m.agent = r.Agent
				sources.add(p, r.Agent, "活动记录")
			}
			if r.Task != "" {
				m.task = r.Task
			}
			if r.WorkType != "" {
				m.wt = r.WorkType
			}
			metaOf[p] = m
		}
	}
	// sidecar near files
	readDirs := map[string]bool{}
	for _, f := range files {
		if f.isDir || readDirs[f.dir] {
			continue
		}
		readDirs[f.dir] = true
		for _, r := range activity.ReadSidecar(f.dir) {
			name := r.File
			if name == "" {
				continue
			}
			p := filepath.Clean(filepath.Join(f.dir, name))
			if idOf[p] == "" {
				continue
			}
			m := metaOf[p]
			if r.Agent != "" {
				m.agent = r.Agent
				sources.add(p, r.Agent, "侧边标记")
			}
			if r.Task != "" {
				m.task = r.Task
			}
			if r.WorkType != "" {
				m.wt = r.WorkType
			}
			metaOf[p] = m
		}
	}
	linkMeta := func(typ, reason string, weight float64, conf string, ps []string) {
		if len(ps) < 2 {
			return
		}
		sort.Strings(ps)
		mesh(ps, func(a, b string) Edge {
			return Edge{Type: typ, From: a, To: b, Reason: reason, Weight: weight, Confidence: conf}
		})
	}
	byAgent, byTask, byWT := map[string][]string{}, map[string][]string{}, map[string][]string{}
	for p, m := range metaOf {
		if m.agent != "" {
			byAgent[m.agent] = append(byAgent[m.agent], p)
		}
		if m.task != "" {
			byTask[m.task] = append(byTask[m.task], p)
		}
		if m.wt != "" {
			byWT[m.wt] = append(byWT[m.wt], p)
		}
	}
	// 先任务（最有把握），再智能体、工种；同一对文件同类型只留第一条理由
	for _, t := range sortedKeys(byTask) {
		linkMeta(EdgeActivity, "同一任务「"+t+"」", 0.85, ConfHigh, byTask[t])
	}
	for _, a := range sortedKeys(byAgent) {
		linkMeta(EdgeActivity, "同一智能体「"+a+"」留下的记录", 0.6, ConfMid, byAgent[a])
	}
	for _, w := range sortedKeys(byWT) {
		linkMeta(EdgeActivity, "同一工种「"+w+"」", 0.55, ConfMid, byWT[w])
	}

	// 4) StripVersion 后同名
	byKey := map[string][]string{}
	for _, f := range files {
		if f.isDir {
			continue
		}
		k := scan.FileKey(f.name)
		if k == "" || len([]rune(k)) < 2 {
			continue
		}
		byKey[k] = append(byKey[k], f.path)
	}
	for _, k := range sortedKeys(byKey) {
		ps := byKey[k]
		if len(ps) < 2 {
			continue
		}
		reason := "文件名去版本后缀后相同（" + k + "）"
		mesh(ps, func(a, b string) Edge {
			return Edge{Type: EdgeCopyName, From: a, To: b, Reason: reason, Weight: 0.75, Confidence: ConfMid}
		})
	}

	// Source relationships take priority over sampled similarity links. Their
	// labels remain historical record evidence, never verified authorship.
	nodes, edges, sourceClipped := sources.project(nodes, edges, idOf, opt.MaxEdges)
	sortNodes(nodes)
	sortEdges(edges)
	var skipped []string
	if depthSkipped > 0 {
		skipped = append(skipped, fmt.Sprintf("%d 个目录超过本次六层展示深度，未展开", depthSkipped))
	}
	if policySkipped > 0 {
		skipped = append(skipped, fmt.Sprintf("按扫描规则跳过 %d 个隐藏、依赖或生成目录", policySkipped))
	}
	if unreadable > 0 {
		skipped = append(skipped, fmt.Sprintf("有 %d 处路径无法读取，关系可能不完整", unreadable))
	}
	if sourceClipped {
		skipped = append(skipped, "来源关系或其他关系达到展示预算（最多 16 个来源工具）；请缩小项目范围查看")
	}
	if walkDone {
		skipped = append(skipped, fmt.Sprintf("文件节点已封顶或超时（最多 %d）", opt.MaxFiles))
	}
	if hashSkipped {
		skipped = append(skipped, fmt.Sprintf("内容哈希只抽查了最多 %d 个文件", opt.MaxHash))
	}
	if attachN > 0 && !opt.ExpandAttach {
		skipped = append(skipped, fmt.Sprintf("另有 %d 个在其他文件夹里的相关文件未展开（打开「含其他文件夹里的相关文件」可看）", attachN))
	}
	if attachSkippedCap > 0 {
		skipped = append(skipped, fmt.Sprintf("还有 %d 个挂靠文件因节点上限（%d）没画进来", attachSkippedCap, opt.MaxFiles))
	}
	if attachDirSkipped > 0 && opt.ExpandAttach {
		skipped = append(skipped, fmt.Sprintf("%d 个挂靠项是文件夹，文件关系网里不单独画点", attachDirSkipped))
	}
	if attachShown > 0 {
		if res.Note == "" {
			res.Note = fmt.Sprintf("已展开 %d 个项目外相关文件", attachShown)
		} else {
			res.Note = res.Note + fmt.Sprintf("；已展开 %d 个项目外相关文件", attachShown)
		}
	}
	if sampledGroups > 0 {
		skipped = append(skipped, fmt.Sprintf("有 %d 组大团（超过 %d 个文件）已抽样成环+弦交叉，完整两两交叉会太密", sampledGroups, CliqueMax))
	}
	if folderHidden > 0 && !opt.IncludeParent {
		skipped = append(skipped, fmt.Sprintf("%d 个任务/成品文件夹不单独画点，同目录关系用文件之间的兄弟连线表示", folderHidden))
	}
	if len(edges) >= opt.MaxEdges {
		skipped = append(skipped, fmt.Sprintf("边数已封顶（%d）", opt.MaxEdges))
	}
	res.Nodes = nodes
	res.Edges = edges
	res.Skipped = strings.Join(skipped, "；")
	if res.Note == "" && projName != "" {
		res.Note = "从项目「" + projName + "」切入"
	}
	return res
}

func resolveProject(e shelf.Env, opt FileOpts) (name, dir, note, id string) {
	cfg := e.Config
	if opt.ProjectID != "" && e.Reg != nil {
		if p := e.Reg.ByID(opt.ProjectID); p != nil {
			return p.Name, p.Path, "从原地项目切入", p.ID
		}
		// ID 对不上时：若同时给了名字，继续按名找；否则明确失败
		if opt.Project == "" {
			return "", "", "找不到原地项目（ID 对不上）", opt.ProjectID
		}
	}
	if opt.Project != "" {
		name = strings.TrimSpace(opt.Project)
		dir = filepath.Join(cfg.Root, name)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return name, dir, "从总文件夹项目切入", ""
		}
		// 也许是绝对路径
		if filepath.IsAbs(name) {
			if st, err := os.Stat(name); err == nil && st.IsDir() {
				base := filepath.Base(name)
				foundID := ""
				if e.Reg != nil {
					for _, p := range e.Reg.Projects {
						if filepath.Clean(p.Path) == filepath.Clean(name) {
							foundID = p.ID
							base = p.Name
							break
						}
					}
				}
				return base, name, "从给定路径切入", foundID
			}
		}
		// 原地登记按名找（总文件夹没有该目录时）
		if e.Reg != nil {
			var matches []registry.Project
			for _, p := range e.Reg.Projects {
				if p.Name == name {
					matches = append(matches, p)
				}
			}
			if len(matches) == 1 {
				p := matches[0]
				return p.Name, p.Path, "从原地登记同名项目切入", p.ID
			}
			if len(matches) > 1 {
				// 歧义：优先已给的 ProjectID（上面已处理）；否则取路径存在的第一个并注明
				for _, p := range matches {
					if st, err := os.Stat(p.Path); err == nil && st.IsDir() {
						return p.Name, p.Path, "从原地登记同名项目切入（同名多个，已选其中一个；建议传项目 ID）", p.ID
					}
				}
				p := matches[0]
				return p.Name, p.Path, "从原地登记同名项目切入（同名多个，建议传项目 ID）", p.ID
			}
		}
		return name, "", "找不到项目「" + name + "」", ""
	}
	// 最近活跃文件 → 所属项目
	items := shelf.Recent(e, 14, 30)
	for _, it := range items {
		if it.Project != "" {
			d := filepath.Join(cfg.Root, it.Project)
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				return it.Project, d, "从最近活跃文件所属项目切入", ""
			}
		}
		if it.ProjectID != "" && e.Reg != nil {
			if p := e.Reg.ByID(it.ProjectID); p != nil {
				return p.Name, p.Path, "从最近活跃文件所属原地项目切入", p.ID
			}
		}
	}
	// 退：总文件夹里第一个项目
	for _, p := range shelf.Projects(e) {
		if p.Source == shelf.SourceRoot && !p.Linked {
			return p.Name, p.Path, "未指定项目，默认第一个总文件夹项目", p.ID
		}
	}
	for _, p := range shelf.Projects(e) {
		if p.Linked {
			continue
		}
		return p.Name, p.Path, "未指定项目，默认列表第一个", p.ID
	}
	return "", "", "", ""
}

func firstWT(en put.Entry) string {
	for _, k := range []string{"工种", "行业·工种", "worktype", "WorkType"} {
		if v := strings.TrimSpace(en[k]); v != "" {
			return v
		}
	}
	return ""
}
