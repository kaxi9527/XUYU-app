// Package trash 把文件/文件夹「删掉」：优先挪进系统废纸篓，不行就挪进总控下的删除暂存。
// 用户内容一律挪走，不用 os.Remove / RemoveAll。
package trash

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/flock"
	filehome "github.com/kaxi9527/WorkShelf/internal/home"
)

// PreviewItem 是预览里的一行。
type PreviewItem struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsDir     bool   `json:"isDir"`
	FileCount int    `json:"fileCount"` // 文件夹内普通文件数（文件本身为 1）
	ByteSize  int64  `json:"byteSize"`
	Action    string `json:"action"` // 进废纸篓 / 进删除暂存 / 跳过
	Skip      bool   `json:"skip,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Label     string `json:"label"` // 给人看的一句，例如「将删除文件夹 X（内含 N 个文件）」
}

// ResultItem 是执行结果。
type ResultItem struct {
	Path     string `json:"path"`
	Dest     string `json:"dest,omitempty"` // 废纸篓或暂存里的新位置
	Action   string `json:"action"`         // 进废纸篓 / 进删除暂存 / 跳过 / 失败
	Undoable bool   `json:"undoable,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Summary 一次预览或删除的汇总。
type Summary struct {
	DryRun     bool          `json:"dryRun"`
	Root       string        `json:"root"`
	LogID      string        `json:"logId,omitempty"`
	LogPath    string        `json:"logPath,omitempty"`
	UndoPath   string        `json:"undoPath,omitempty"`
	Preview    []PreviewItem `json:"preview,omitempty"`
	Items      []ResultItem  `json:"items,omitempty"`
	Trashed    int           `json:"trashed"`
	Staged     int           `json:"staged"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	TotalFiles int           `json:"totalFiles"`
	TotalBytes int64         `json:"totalBytes"`
	MethodHint string        `json:"methodHint,omitempty"`
	Warnings   []string      `json:"warnings,omitempty"`
}

// Options 控制一次删除。
type Options struct {
	Config config.Config
	Home   string // 用户主目录；空则 os.UserHomeDir
	Paths  []string
	DryRun bool
	Now    time.Time
	// AllowOutsideRoot：允许删总文件夹外、但仍在主目录下的路径（首页展示的桌面/下载等）。
	// false 时只许总文件夹内（且不碰总控）。
	AllowOutsideRoot bool
	// Approved is a server-owned snapshot of the explicitly reviewed paths.
	Approved map[string]ApprovalSnapshot
}

type undoEntry struct {
	From string `json:"from"` // 删除前路径
	To   string `json:"to"`   // 废纸篓/暂存路径
	Mode string `json:"mode"` // trash | stage
}

type undoLog struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"` // delete
	CreatedAt string      `json:"createdAt"`
	Root      string      `json:"root"`
	Home      string      `json:"home,omitempty"`
	Entries   []undoEntry `json:"entries"`
}

// Preview 只统计、校验，不挪文件。
func Preview(opt Options) (*Summary, error) {
	s, err := plan(opt)
	if err != nil {
		return nil, err
	}
	s.DryRun = true
	return s, nil
}

// Apply 执行删除（挪走）。DryRun=true 时等同 Preview。
func Apply(opt Options) (*Summary, error) {
	if opt.DryRun {
		return Preview(opt)
	}
	s, err := plan(opt)
	if err != nil {
		return nil, err
	}
	s.DryRun = false
	if err := filehome.CheckPath(opt.Config.Root, filepath.Join(opt.Config.Root, opt.Config.Names.Control, "整理撤销")); err != nil {
		return nil, err
	}
	control := filepath.Join(opt.Config.Root, opt.Config.Names.Control)
	if err := os.MkdirAll(control, 0755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(control, "整理执行.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = flock.Lock(lock); err != nil {
		return nil, err
	}
	defer flock.Unlock(lock)
	if err := validateApproval(opt, s); err != nil {
		return nil, err
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(opt.Config.Location())
	s.LogID = now.Format("20060102-150405")

	home := opt.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	stageRoot := filepath.Join(opt.Config.Root, opt.Config.Names.Control, "整理撤销", "删除暂存", s.LogID)
	var u undoLog
	u.ID, u.Kind, u.CreatedAt, u.Root = s.LogID, "delete", now.Format(time.RFC3339), opt.Config.Root
	u.Home = home

	out := make([]ResultItem, 0, len(s.Preview))
	for _, p := range s.Preview {
		if p.Skip {
			out = append(out, ResultItem{Path: p.Path, Action: "跳过", Error: p.Reason})
			s.Skipped++
			continue
		}
		if approved, ok := opt.Approved[p.Path]; ok {
			if err := approved.matches(p.Path); err != nil {
				s.Failed++
				out = append(out, ResultItem{Path: p.Path, Action: "失败", Error: err.Error()})
				continue
			}
		}
		ri, err := doOneLogged(p.Path, home, stageRoot, opt.Config, s, &u)
		if err != nil {
			ri.Action = "失败"
			ri.Error = err.Error()
			s.Failed++
			out = append(out, ri)
			continue
		}
		out = append(out, ri)
		if ri.Action == "进废纸篓" {
			s.Trashed++
		} else {
			s.Staged++
		}
	}
	s.Items = out
	s.Preview = nil
	if err := writeLogs(opt.Config, s, &u); err != nil {
		s.Warnings = append(s.Warnings, "文件已移出，最终结果日志未保存；可使用执行前保存的恢复清单："+err.Error())
	}
	return s, nil
}

func plan(opt Options) (*Summary, error) {
	cfg := opt.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	home := opt.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	if len(opt.Paths) == 0 {
		return nil, errors.New("没有要删除的路径")
	}
	s := &Summary{Root: cfg.Root, MethodHint: "优先挪进系统废纸篓；废纸篓用不了就挪进总控「删除暂存」，都可以撤销。"}
	seen := map[string]bool{}
	for _, raw := range opt.Paths {
		p := filepath.Clean(strings.TrimSpace(raw))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		it := inspect(cfg, home, p, opt.AllowOutsideRoot)
		s.Preview = append(s.Preview, it)
		if it.Skip {
			s.Skipped++
			continue
		}
		s.TotalFiles += it.FileCount
		s.TotalBytes += it.ByteSize
	}
	if len(s.Preview) == 0 {
		return nil, errors.New("没有要删除的路径")
	}
	return s, nil
}

func inspect(cfg config.Config, home, path string, allowOutside bool) PreviewItem {
	it := PreviewItem{Path: path, Name: filepath.Base(path)}
	if !filepath.IsAbs(path) {
		it.Skip, it.Action, it.Reason, it.Label = true, "跳过", "必须是绝对路径", "跳过（不是绝对路径）"
		return it
	}
	if err := checkAllowed(cfg, home, path, allowOutside); err != nil {
		it.Skip, it.Action, it.Reason, it.Label = true, "跳过", err.Error(), "跳过："+err.Error()
		return it
	}
	fi, err := os.Lstat(path)
	if err != nil {
		it.Skip, it.Action, it.Reason, it.Label = true, "跳过", "不存在："+err.Error(), "跳过（不存在）"
		return it
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		it.Skip, it.Action, it.Reason, it.Label = true, "跳过", "是软链接，不删", "跳过（软链接）"
		return it
	}
	it.IsDir = fi.IsDir()
	if it.IsDir {
		n, sz, err := countTree(path)
		if err != nil {
			it.Skip, it.Action, it.Reason, it.Label = true, "跳过", "读文件夹失败："+err.Error(), "跳过（读不了）"
			return it
		}
		it.FileCount, it.ByteSize = n, sz
		it.Label = fmt.Sprintf("将删除文件夹 %s（内含 %d 个文件，%s）", it.Name, n, humanSize(sz))
	} else {
		it.FileCount, it.ByteSize = 1, fi.Size()
		it.Label = fmt.Sprintf("将删除文件 %s（%s）", it.Name, humanSize(fi.Size()))
	}
	it.Action = "进废纸篓" // 预览乐观；执行时可能退化为暂存
	return it
}

// checkAllowed：禁删 root 本身、禁删总控整棵；路径须在总文件夹内，或（允许时）在主目录下。
func checkAllowed(cfg config.Config, home, path string, allowOutside bool) error {
	root := filepath.Clean(cfg.Root)
	control := filepath.Clean(filepath.Join(root, cfg.Names.Control))
	p := filepath.Clean(path)
	if p == root {
		return fmt.Errorf("不能删除总文件夹本身：%s", root)
	}
	if p == control || under(p, control) {
		return fmt.Errorf("不能删除总控（%s）里的东西，那里放着索引和日志", cfg.Names.Control)
	}
	if under(p, root) || p == root {
		return nil
	}
	if allowOutside {
		h := filepath.Clean(home)
		if p == h {
			return fmt.Errorf("不能删除主目录本身")
		}
		if under(p, h) {
			// 再挡一下废纸篓自己
			trash := filepath.Join(h, ".Trash")
			if p == trash || under(p, trash) {
				return fmt.Errorf("不能删废纸篓里的东西")
			}
			return nil
		}
	}
	return fmt.Errorf("路径不在允许范围（总文件夹或主目录下）：%s", p)
}

func under(path, parent string) bool {
	path, parent = filepath.Clean(path), filepath.Clean(parent)
	sep := string(filepath.Separator)
	return strings.HasPrefix(path+sep, parent+sep) || strings.HasPrefix(path, parent+sep)
}

func countTree(dir string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		files++
		if fi, e2 := d.Info(); e2 == nil {
			bytes += fi.Size()
		}
		return nil
	})
	return
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
}

func doOne(src, home, stageRoot string) (ResultItem, *undoEntry, error) {
	ri := ResultItem{Path: src}
	// 1) 试系统废纸篓
	if dest, err := moveToSystemTrash(src, home); err == nil {
		ri.Dest, ri.Action, ri.Undoable = dest, "进废纸篓", true
		return ri, &undoEntry{From: src, To: dest, Mode: "trash"}, nil
	}
	// 2) 退化为删除暂存
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		return ri, nil, fmt.Errorf("废纸篓用不了，建删除暂存也失败：%w", err)
	}
	dest, err := moveInto(src, stageRoot)
	if err != nil {
		return ri, nil, fmt.Errorf("废纸篓用不了，挪进删除暂存也失败：%w", err)
	}
	ri.Dest, ri.Action, ri.Undoable = dest, "进删除暂存", true
	return ri, &undoEntry{From: src, To: dest, Mode: "stage"}, nil
}

// moveToSystemTrash 把路径 rename 进 ~/.Trash（同名加时间戳），不弹前台、不用 osascript。
func moveToSystemTrash(src, home string) (string, error) {
	trashDir := filepath.Join(home, ".Trash")
	if fi, err := os.Stat(trashDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("废纸篓目录不可用：%w", err)
	}
	return moveInto(src, trashDir)
}

func moveInto(src, dir string) (string, error) {
	base := filepath.Base(src)
	for i := 0; i < 1000; i++ {
		dest := filepath.Join(dir, base)
		if i > 0 {
			dest = filepath.Join(dir, fmt.Sprintf("%s-%03d%s", strings.TrimSuffix(base, filepath.Ext(base)), i, filepath.Ext(base)))
		}
		if err := filehome.RenameNoReplace(src, dest); err == nil {
			return dest, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", errors.New("同名目标过多")
}

// Every attempted destination is journaled before the move. Failed intents are
// harmless during undo: their source still exists or destination is absent.
func doOneLogged(src, home, stage string, cfg config.Config, s *Summary, u *undoLog) (ResultItem, error) {
	ri := ResultItem{Path: src}
	for i, dir := range []string{filepath.Join(home, ".Trash"), stage} {
		if err := filehome.CheckPath(home, src); err != nil {
			if err = filehome.CheckPath(cfg.Root, src); err != nil {
				return ri, err
			}
		}
		base := home
		if i == 1 {
			base = cfg.Root
		}
		if err := filehome.CheckPath(base, dir); err != nil {
			return ri, err
		}
		if i == 1 {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return ri, err
			}
		} else if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		// Reserve a private container so a colliding file cannot become our undo target.
		container, err := os.MkdirTemp(dir, "WorkShelf-")
		if err != nil {
			continue
		}
		dest := filepath.Join(container, filepath.Base(src))
		mode, action := "trash", "进废纸篓"
		if i == 1 {
			mode, action = "stage", "进删除暂存"
		}
		u.Entries = append(u.Entries, undoEntry{From: src, To: dest, Mode: mode})
		if err := writeLogs(cfg, s, u); err != nil {
			return ri, fmt.Errorf("恢复记录保存失败，未删除：%w", err)
		}
		if err := filehome.RenameNoReplace(src, dest); err == nil {
			ri.Dest, ri.Action, ri.Undoable = dest, action, true
			return ri, nil
		}
	}
	return ri, errors.New("无法移动到废纸篓或删除暂存，源文件保留原位")
}

func writeLogs(cfg config.Config, s *Summary, u *undoLog) error {
	logDir := filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.Logs)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{
		"id": s.LogID, "kind": "delete", "root": s.Root,
		"trashed": s.Trashed, "staged": s.Staged, "skipped": s.Skipped, "failed": s.Failed,
		"totalFiles": s.TotalFiles, "totalBytes": s.TotalBytes, "items": s.Items,
	}, "", "  ")
	lp, err := writeNewNumbered(filepath.Join(logDir, "删除-"+s.LogID+".json"), append(b, '\n'))
	if err != nil {
		return err
	}
	s.LogPath = lp
	if u == nil || len(u.Entries) == 0 {
		return nil
	}
	ud := filepath.Join(cfg.Root, cfg.Names.Control, "整理撤销")
	if err := os.MkdirAll(ud, 0o755); err != nil {
		return err
	}
	ub, _ := json.MarshalIndent(u, "", "  ")
	up, err := writeNewNumbered(filepath.Join(ud, "undo-delete-"+s.LogID+".json"), append(ub, '\n'))
	if err != nil {
		return err
	}
	s.UndoPath = up
	return nil
}

func writeNewNumbered(path string, data []byte) (string, error) {
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	for i := 1; i < 1000; i++ {
		p := path
		if i > 1 {
			p = fmt.Sprintf("%s-%02d%s", stem, i, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return p, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return p, err
		}
		return p, f.Close()
	}
	return "", fmt.Errorf("同名日志太多：%s", path)
}

// UndoResult 撤销结果。
type UndoResult struct {
	LogID    string   `json:"logId"`
	Restored int      `json:"restored"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Notes    []string `json:"notes,omitempty"`
}

// Undo 按撤销清单把文件从废纸篓/删除暂存搬回原处（原处必须空着）。
func Undo(cfg config.Config, undoPath string) (*UndoResult, error) {
	if err := filehome.CheckPath(filepath.Join(cfg.Root, cfg.Names.Control, "整理撤销"), undoPath); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(undoPath)
	if err != nil {
		return nil, err
	}
	var u undoLog
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("撤销清单读不了：%w", err)
	}
	res := &UndoResult{LogID: u.ID}
	if filepath.Clean(u.Root) != filepath.Clean(cfg.Root) {
		return nil, errors.New("恢复清单属于另一个总文件夹")
	}
	userHome, _ := os.UserHomeDir()
	if u.Home != "" {
		userHome = u.Home
	}
	for _, e := range u.Entries {
		// Fixture homes and real homes are both represented by the recorded trash
		// location; stage restores still require an allowed original location.
		base := userHome
		if e.Mode == "trash" {
			parts := strings.Split(e.To, string(filepath.Separator)+".Trash"+string(filepath.Separator))
			if len(parts) != 2 {
				res.Failed++
				continue
			}
			base = parts[0]
		}
		if err := checkAllowed(cfg, base, e.From, true); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, err.Error())
			continue
		}
		targetBase := filepath.Join(cfg.Root, cfg.Names.Control, "整理撤销", "删除暂存")
		if e.Mode == "trash" {
			targetBase = filepath.Join(base, ".Trash")
		}
		if err := filehome.CheckPath(targetBase, e.To); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, err.Error())
			continue
		}
		if _, err := os.Stat(e.From); err == nil {
			res.Skipped++
			res.Notes = append(res.Notes, "原处已有东西，没搬回："+e.From)
			continue
		}
		if _, err := os.Stat(e.To); err != nil {
			res.Skipped++
			res.Notes = append(res.Notes, "废纸篓/暂存里找不到："+e.To)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(e.From), 0o755); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, err.Error())
			continue
		}
		if err := filehome.RenameNoReplace(e.To, e.From); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, "搬回失败："+err.Error())
			continue
		}
		res.Restored++
	}
	return res, nil
}
