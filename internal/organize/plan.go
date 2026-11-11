// Package organize connects project sources, confirmed work types and real folders.
// Planning is read-only. Execution consumes only the server-retained preview.
package organize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type Request struct {
	Project   string            `json:"project"`
	Sources   []string          `json:"sources"`
	WorkTypes []string          `json:"workTypes"`
	Folders   map[string]string `json:"folders,omitempty"` // User corrections, restricted to the chosen layouts.
	Profile   *worktype.Profile `json:"profile,omitempty"`
	Merge     bool              `json:"merge,omitempty"`
	PreviewID string            `json:"previewId,omitempty"`
}
type Row struct {
	ID             string `json:"id"`
	Source         string `json:"source"`
	Target         string `json:"target,omitempty"`
	Folder         string `json:"folder,omitempty"`
	Reason         string `json:"reason"`
	Selected       bool   `json:"selected"`
	Blocked        bool   `json:"blocked"`
	Directory      bool   `json:"directory"`
	CrossDevice    bool   `json:"crossDevice"`
	KeepOriginal   bool   `json:"keepOriginal,omitempty"`
	Existing       bool   `json:"existing,omitempty"` // Already inside this project; never a move candidate.
	Operation      string `json:"operation,omitempty"`
	VersionGroup   string `json:"versionGroup,omitempty"`
	Representative string `json:"representative,omitempty"`
}
type Plan struct {
	Token         string            `json:"token,omitempty"`
	Project       string            `json:"project"`
	Target        string            `json:"target"`
	WorkTypes     []string          `json:"workTypes"`
	Folders       []string          `json:"folders"`
	Rows          []Row             `json:"rows"`
	Warnings      []string          `json:"warnings"`
	Profile       *worktype.Profile `json:"profile,omitempty"`
	Created       time.Time         `json:"-"`
	moves         map[string]home.ExactMove
	Merge         bool `json:"merge,omitempty"`
	MergedFolders int  `json:"mergedFolders,omitempty"`
	duplicates    map[string]home.ExactMove
	targetRoot    string
}

func Build(cfg config.Config, userHome string, req Request) (*Plan, error) {
	return BuildContext(context.Background(), cfg, userHome, req)
}

func BuildContext(ctx context.Context, cfg config.Config, userHome string, req Request) (*Plan, error) {
	return buildContext(ctx, cfg, userHome, req, "")
}

// BuildAtContext places files in a registered, already existing project.
func BuildAtContext(ctx context.Context, cfg config.Config, userHome string, req Request, projectPath string) (*Plan, error) {
	return buildContext(ctx, cfg, userHome, req, projectPath)
}

func buildContext(ctx context.Context, cfg config.Config, userHome string, req Request, projectPath string) (*Plan, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if projectPath == "" {
		if err := put.CheckName("项目名", req.Project); err != nil {
			return nil, err
		}
		if strings.EqualFold(req.Project, cfg.Names.Control) {
			return nil, errors.New("总控不能作为项目")
		}
	} else if strings.TrimSpace(req.Project) == "" || len([]rune(req.Project)) > 300 {
		return nil, errors.New("项目名称无效")
	}
	if len(req.Sources) == 0 || len(req.Sources) > 64 {
		return nil, errors.New("请选择 1–64 个项目来源")
	}
	if len(req.WorkTypes) == 0 && req.Profile == nil {
		return nil, errors.New("请先确认至少一个工种")
	}
	p := &Plan{Project: req.Project, Target: filepath.Join(cfg.Root, req.Project), Created: time.Now(), moves: map[string]home.ExactMove{}, Rows: []Row{}, Warnings: []string{}}
	if projectPath != "" {
		info, err := os.Stat(projectPath)
		if err != nil || !info.IsDir() {
			return nil, errors.New("原地项目目录不可用")
		}
		p.Target, p.targetRoot = filepath.Clean(projectPath), filepath.Clean(projectPath)
	}
	var layouts []worktype.Layout
	folders := map[string]bool{}
	for _, label := range req.WorkTypes {
		w, err := worktype.Resolve(label)
		if err != nil {
			return nil, err
		}
		if contains(p.WorkTypes, w.Label()) {
			continue
		}
		p.WorkTypes = append(p.WorkTypes, w.Label())
		l := worktype.LayoutFor(w.Template)
		layouts = append(layouts, l)
		for _, f := range l.Folders {
			if !folders[f] {
				p.Folders = append(p.Folders, f)
				folders[f] = true
			}
		}
	}
	if req.Profile != nil {
		if err := req.Profile.Validate(); err != nil {
			return nil, err
		}
		p.Profile = req.Profile
		p.Folders = append([]string(nil), req.Profile.Folders...)
		folders = map[string]bool{}
		for _, f := range p.Folders {
			folders[f] = true
		}
		name := "自定义"
		if len(layouts) > 0 {
			name = layouts[0].Name
		}
		layouts = []worktype.Layout{{Name: name, Folders: p.Folders, Roles: req.Profile.Roles}}
	} else if len(layouts) > 0 {
		roles := map[string]string{}
		for _, l := range layouts {
			for k, v := range l.Roles {
				if roles[k] == "" {
					roles[k] = v
				}
			}
		}
		p.Profile = &worktype.Profile{Name: layouts[0].Name, Folders: p.Folders, Roles: roles, Rules: []worktype.Rule{}}
	}
	for _, f := range req.Folders {
		if !folders[f] {
			return nil, fmt.Errorf("目标目录不属于所选工种：%s", f)
		}
	}
	checkRoot := cfg.Root
	if p.targetRoot != "" {
		checkRoot = p.targetRoot
	}
	if err := home.CheckPath(checkRoot, p.Target); err != nil {
		return nil, err
	}
	var sources []string
	for _, raw := range req.Sources {
		s := filepath.Clean(config.Expand(strings.TrimSpace(raw), userHome))
		base := userHome
		if in(s, cfg.Root) {
			base = cfg.Root
		}
		if s == userHome || s == cfg.Root || in(s, filepath.Join(cfg.Root, cfg.Names.Control)) {
			return nil, errors.New("请选择项目或具体文件，不要选择主目录、总目录或总控")
		}
		if err := home.CheckPath(base, s); err != nil {
			return nil, err
		}
		if !contains(sources, s) {
			sources = append(sources, s)
		}
	}
	sourceOrder := append([]string(nil), sources...)
	sort.SliceStable(sources, func(i, j int) bool { return len(sources[i]) < len(sources[j]) })
	var roots []string
	for _, s := range sources {
		covered := false
		for _, r := range roots {
			if in(s, r) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, s)
		}
	}
	reserved := map[string]bool{}
	deadline := time.Now().Add(15 * time.Second)
	scope, err := inspectPlacementScope(ctx, roots, req, p.Target, cfg.Names.Readme, deadline)
	if err != nil {
		return nil, err
	}
	// When a folder is split for one explicit match, its other children inherit
	// the folder's original destination instead of being flattened by file type.
	preservedFolders := map[string]string{}
	projectBoundaryChecks := map[string]mergeGuardState{}
	add := func(source, base string, directory bool, blocked string, role string, confident bool) {
		h := sha256.Sum256([]byte(source))
		row := Row{ID: hex.EncodeToString(h[:12]), Source: source, Directory: directory}
		if blocked != "" {
			row.Blocked = true
			row.Reason = blocked
			p.Rows = append(p.Rows, row)
			return
		}
		folder, reason := automaticFolder(layouts, source, role, confident)
		if inherited := preservedFolders[filepath.Dir(source)]; inherited != "" {
			folder, reason, confident = inherited, "未命中调整规则，保持原有目录层级", true
		}
		row.KeepOriginal = role == "runtime-log" || role == "backup"
		if p.Profile != nil {
			if f, r := p.Profile.Match(source); f != "" {
				folder, reason, confident = f, r, true
				row.KeepOriginal = false
			}
		}
		if corrected := req.Folders[source]; corrected != "" {
			folder = corrected
			confident = true
			reason = "使用你确认的目录"
			row.KeepOriginal = false
		}
		if folder == "" {
			row.Reason = reason
			if row.KeepOriginal {
				row.Reason = "日志、备份或临时文件默认保留原处；如需归档，可选择目录后重新预览"
			}
			p.Rows = append(p.Rows, row)
			return
		}
		row.Folder = folder
		row.Reason = reason
		target := filepath.Join(p.Target, folder, filepath.Base(source))
		if target == source {
			row.Blocked = true
			row.Reason = "已经位于对应工种目录"
			row.Target = source
			p.Rows = append(p.Rows, row)
			return
		}
		if in(target, source) {
			row.Blocked = true
			row.Reason = "目标位于源工程内部，保留原结构"
			p.Rows = append(p.Rows, row)
			return
		}
		if !req.Merge {
			move := home.ExactMove{Source: source, Target: target, SourceRoot: base}
			if err := validateMergeParents(move, p.Target, p.templateStages(), projectBoundaryChecks); err != nil {
				row.Blocked = true
				row.Reason = "保持完整工程的内部结构，不单独调整其中的文件：" + err.Error()
				p.Rows = append(p.Rows, row)
				return
			}
		}
		if req.Merge {
			row.Target = target
			row.Selected = !row.KeepOriginal
			p.moves[row.ID] = home.ExactMove{Source: source, Target: target, SourceRoot: base}
			p.Rows = append(p.Rows, row)
			return
		}
		var err error
		target, err = unique(target, reserved)
		if err != nil {
			row.Blocked = true
			row.Reason = err.Error()
			p.Rows = append(p.Rows, row)
			return
		}
		snapshot, err := home.Snapshot(source)
		if err != nil {
			row.Blocked = true
			row.Reason = err.Error()
			p.Rows = append(p.Rows, row)
			return
		}
		row.Target = target
		row.Selected = !row.KeepOriginal
		row.CrossDevice = home.IsCrossDevice(source, target)
		if row.CrossDevice {
			row.Reason += "；跨磁盘搬迁"
		}
		if directory {
			row.Reason += "；整个文件夹一起归档，内部结构不变"
		}
		p.moves[row.ID] = home.ExactMove{Source: source, Target: target, SourceRoot: base, Snapshot: snapshot, CrossDevice: row.CrossDevice}
		reserved[target] = true
		p.Rows = append(p.Rows, row)
	}
	addExisting := func(path string, directory bool) {
		h := sha256.Sum256([]byte(path))
		folder, _ := filepath.Rel(p.Target, path)
		if folder == "." {
			folder = ""
		}
		p.Rows = append(p.Rows, Row{ID: hex.EncodeToString(h[:12]), Source: path, Target: path, Folder: filepath.ToSlash(folder), Directory: directory, Blocked: true, Existing: true, Reason: "已在项目中，保留现有目录和内部文件，不重复整理"})
	}
	entries := 0
	for _, root := range roots {
		base := userHome
		if in(root, cfg.Root) {
			base = cfg.Root
		}
		if reason := scope.blocked[root]; reason != "" {
			info, _ := os.Lstat(root)
			add(root, base, info != nil && info.IsDir(), reason, "", false)
			p.Warnings = append(p.Warnings, reason)
			continue
		}
		hidden := false
		rel, _ := filepath.Rel(base, root)
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				hidden = true
			}
		}
		if hidden {
			add(root, base, false, "隐藏配置保留原位；可在文件浏览中查看，删除需单独确认", "", false)
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e != nil {
				add(path, base, false, "无法读取，保留原位："+e.Error(), "", false)
				return nil
			}
			entries++
			if entries > 5000 || time.Now().After(deadline) {
				p.Warnings = append(p.Warnings, "本次预览达到 5000 条目或 15 秒上限；其余内容尚未检查，请缩小来源范围")
				return filepath.SkipAll
			}
			if d.Type()&os.ModeSymlink != 0 {
				add(path, base, false, "软链接保留原位", "", false)
				return nil
			}
			if d.IsDir() {
				if blocked := protected(path); blocked != "" {
					add(path, base, true, blocked, "", false)
					return filepath.SkipDir
				}
			}
			if path != root && (strings.HasPrefix(d.Name(), ".") || scan.SkipDirName(d.Name())) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if path == p.Target {
					if protected(path) != "" || bundleKind(path) != "" {
						addExisting(path, true)
						return filepath.SkipDir
					}
					return nil
				}
				for _, folder := range p.Folders {
					if path == filepath.Join(p.Target, folder) {
						addExisting(path, true)
						return filepath.SkipDir
					}
				}
				if in(path, p.Target) {
					addExisting(path, true)
					return filepath.SkipDir
				}
				bundle := bundleKind(path)
				if req.Merge && bundle == "" {
					bundle = mergeBundleKind(path)
				}
				if bundle != "" {
					add(path, base, true, "", bundle, true)
					return filepath.SkipDir
				}
				if path != root {
					role, confident := fileRole(path)
					if role == "" {
						role = "source"
					}
					if !scope.expand[path] {
						add(path, base, true, "", role, confident)
						return filepath.SkipDir
					}
					folder, _ := automaticFolder(layouts, path, role, confident)
					if inherited := preservedFolders[filepath.Dir(path)]; inherited != "" {
						folder = inherited
					}
					if p.Profile != nil {
						if matched, _ := p.Profile.Match(path); matched != "" {
							folder = matched
						}
					}
					if corrected := req.Folders[path]; corrected != "" {
						folder = corrected
					}
					if folder != "" {
						preservedFolders[path] = filepath.ToSlash(filepath.Join(folder, filepath.Base(path)))
					}
				}
				return nil
			}
			if scan.IsSensitiveName(d.Name()) {
				add(path, base, false, "疑似密钥或凭据，保留原位", "", false)
				return nil
			}
			if d.Name() == cfg.Names.Readme || d.Name() == "AGENTS.md" || d.Name() == "CLAUDE.md" {
				return nil
			}
			role, confident := fileRole(path)
			add(path, base, false, "", role, confident)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if entries > 5000 || time.Now().After(deadline) {
			break
		}
	}
	if req.Merge {
		if err := p.resolveMerge(ctx, cfg, sourceOrder); err != nil {
			return nil, err
		}
	}
	if err := scope.verify(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Plan) Apply(cfg config.Config, ids []string) (*home.Summary, error) {
	return p.ApplyContext(context.Background(), cfg, ids)
}

func (p *Plan) ApplyContext(ctx context.Context, cfg config.Config, ids []string) (*home.Summary, error) {
	return p.ApplyContextWithUndoReady(ctx, cfg, ids, nil)
}

func (p *Plan) ApplyContextWithUndoReady(ctx context.Context, cfg config.Config, ids []string, ready func(string) error) (*home.Summary, error) {
	if time.Since(p.Created) > 30*time.Minute {
		return nil, errors.New("预览超过 30 分钟，请重新生成")
	}
	if p.targetRoot == "" && p.Target != filepath.Join(cfg.Root, p.Project) {
		return nil, errors.New("总文件夹配置已变化，请重新预览")
	}
	seen := map[string]bool{}
	var moves []home.ExactMove
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		m, ok := p.moves[id]
		if !ok {
			return nil, errors.New("勾选项不属于当前可执行预览")
		}
		moves = append(moves, m)
	}
	selectedTargets := map[string]bool{}
	for _, m := range moves {
		selectedTargets[m.Target] = true
	}
	for _, row := range p.Rows {
		if m, ok := p.duplicates[row.ID]; ok {
			if row.Representative == m.Target || selectedTargets[m.Target] {
				moves = append(moves, m)
			}
		}
	}
	if len(moves) == 0 {
		return nil, errors.New("没有勾选可整理的文件")
	}
	// Every placement mode protects project boundaries, including plain moves.
	// Invalidate cached checks when directory entries change during execution.
	guards := map[string]mergeGuardState{}
	stages := p.templateStages()
	for i := range moves {
		m := moves[i]
		moves[i].TargetRoot = p.targetRoot
		moves[i].ValidateContext = func() error { return validateMergeParents(m, p.Target, stages, guards) }
	}
	return home.ApplyExactContextWithUndoReady(ctx, cfg, moves, ready)
}

func (p *Plan) templateStages() map[string]bool {
	out := map[string]bool{}
	if p.targetRoot != "" {
		out[p.Target] = true
	}
	for _, folder := range p.Folders {
		out[filepath.Join(p.Target, folder)] = true
	}
	return out
}

func unique(want string, reserved map[string]bool) (string, error) {
	for i := 1; i <= 9999; i++ {
		p := want
		if i > 1 {
			ext := filepath.Ext(want)
			p = fmt.Sprintf("%s-%02d%s", strings.TrimSuffix(want, ext), i, ext)
		}
		if reserved[p] {
			continue
		}
		_, e := os.Lstat(p)
		if os.IsNotExist(e) {
			return p, nil
		}
		if e != nil {
			return "", e
		}
	}
	return "", errors.New("同名目标过多，请调整项目或文件名")
}
func in(p, base string) bool {
	r, e := filepath.Rel(base, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
