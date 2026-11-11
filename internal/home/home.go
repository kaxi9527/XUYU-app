// Package home 把不在规定位置的文件/项目归进总文件夹。
//
// 同盘和跨盘均搬迁，保留撤销记录。
package home

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// 放到哪（工种子目录）。
const (
	AreaTasks     = "任务"
	AreaOutputs   = "成品"
	AreaMaterials = "资料"
	AreaCode      = "代码"
	AreaLoose     = "散落" // → 资料/散落文件
	AreaSensitive = "敏感" // → 资料/敏感
)

// Job 是一条归位任务。
type Job struct {
	Source  string `json:"source"`          // 源路径（文件或文件夹）
	Project string `json:"project"`         // 目标项目名（总文件夹下的文件夹名）
	Area    string `json:"area"`            // 任务/成品/资料/代码/散落/敏感；整项目归位时为空
	Task    string `json:"task,omitempty"`  // 任务或成品下的子目录名（可空）
	Whole   bool   `json:"whole,omitempty"` // true = 把整个源文件夹当作项目搬进总文件夹
}

// PreviewItem 是预览里的一行。
type PreviewItem struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Action      string `json:"action"` // 搬走 / 复制 / 跳过
	CrossDevice bool   `json:"crossDevice,omitempty"`
	Skip        bool   `json:"skip,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Whole       bool   `json:"whole,omitempty"`
}

// ResultItem 是执行结果里的一行。
type ResultItem struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Action      string `json:"action"` // 搬走 / 复制 / 跳过 / 失败
	CrossDevice bool   `json:"crossDevice,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Undoable    bool   `json:"undoable,omitempty"` // 同盘搬走可撤销
	Error       string `json:"error,omitempty"`
	KeptSource  bool   `json:"keptSource,omitempty"` // 跨盘复制后原件仍在
}

// Summary 是一次预览或执行的汇总。
type Summary struct {
	Warnings   []string      `json:"warnings,omitempty"`
	DryRun     bool          `json:"dryRun"`
	Root       string        `json:"root"`
	LogID      string        `json:"logId,omitempty"`
	LogPath    string        `json:"logPath,omitempty"`
	UndoPath   string        `json:"undoPath,omitempty"`
	Items      []ResultItem  `json:"items,omitempty"`
	Preview    []PreviewItem `json:"preview,omitempty"`
	Moved      int           `json:"moved"`
	Copied     int           `json:"copied"`
	Skipped    int           `json:"skipped"`
	Duplicates int           `json:"duplicates,omitempty"`
	Failed     int           `json:"failed"`
	Stopped    int           `json:"stopped,omitempty"`
	CrossHint  string        `json:"crossHint,omitempty"`
}

// Options 控制一次归位。
type Options struct {
	Config config.Config
	Jobs   []Job
	DryRun bool
	Now    time.Time
	// Agent 写进日志；空则 workshelf。
	Agent string
}

// Preview 只算目标、判同盘/跨盘，不改任何文件。
func Preview(opt Options) (*Summary, error) {
	s, err := plan(opt)
	if err != nil {
		return nil, err
	}
	s.DryRun = true
	return s, nil
}

// Apply 真正执行；DryRun=true 时等同 Preview。
func Apply(opt Options) (*Summary, error) {
	if opt.DryRun {
		return Preview(opt)
	}
	s, err := plan(opt)
	if err != nil {
		return nil, err
	}
	s.DryRun = false
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(opt.Config.Location())
	logID := now.Format("20060102-150405")
	s.LogID = logID

	var undo undoLog
	undo.ID = logID
	undo.CreatedAt = now.Format(time.RFC3339)
	undo.Root = opt.Config.Root
	undo.Agent = agentName(opt.Agent)

	out := make([]ResultItem, 0, len(s.Preview))
	for _, p := range s.Preview {
		if p.Skip {
			out = append(out, skipItem(p))
			s.Skipped++
			continue
		}
		ri, u, err := doOne(opt.Config, p, now)
		if err != nil {
			ri.Action = "失败"
			ri.Error = err.Error()
			s.Failed++
			out = append(out, ri)
			continue
		}
		out = append(out, ri)
		if ri.Action == "搬走" {
			s.Moved++
		} else if ri.Action == "复制" {
			s.Copied++
		}
		if u != nil {
			undo.Entries = append(undo.Entries, *u)
		}
	}
	s.Items = out
	s.Preview = nil

	if err := writeLogs(opt.Config, s, &undo, now); err != nil {
		return s, fmt.Errorf("归位做完了，但写日志失败：%w", err)
	}
	return s, nil
}

func skipItem(p PreviewItem) ResultItem {
	return ResultItem{Source: p.Source, Target: p.Target, Action: "跳过", Error: p.Reason, CrossDevice: p.CrossDevice}
}

func agentName(a string) string {
	if strings.TrimSpace(a) == "" {
		return "workshelf"
	}
	return a
}

func plan(opt Options) (*Summary, error) {
	cfg := opt.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(cfg.Root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("总文件夹不存在或不是文件夹：%s", cfg.Root)
	}
	if len(opt.Jobs) == 0 {
		return nil, errors.New("没有要归位的文件")
	}
	rootDev := deviceOfPath(cfg.Root)
	s := &Summary{Root: cfg.Root}
	cross := 0
	for _, j := range opt.Jobs {
		items, err := planJob(cfg, j, rootDev)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.CrossDevice && !it.Skip {
				cross++
			}
			s.Preview = append(s.Preview, it)
			if it.Skip {
				s.Skipped++
			}
		}
	}
	if cross > 0 {
		s.CrossHint = fmt.Sprintf("有 %d 条需要跨磁盘搬迁，完成后文件将位于目标位置。", cross)
	}
	// 预览阶段 Skipped 只是预览统计；Apply 会重算
	if opt.DryRun {
		// keep
	} else {
		s.Skipped = 0
	}
	return s, nil
}

func planJob(cfg config.Config, j Job, rootDev uint64) ([]PreviewItem, error) {
	src := filepath.Clean(j.Source)
	if !filepath.IsAbs(src) {
		return nil, fmt.Errorf("源路径必须是绝对路径：%s", j.Source)
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: "源不存在：" + err.Error()}}, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: "是软链接，不跟、不搬"}}, nil
	}
	if scan.IsSensitiveName(filepath.Base(src)) {
		return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: "文件名像密钥/凭据，拒绝归位"}}, nil
	}
	if j.Whole {
		if err := put.CheckName("项目名", j.Project); err != nil {
			return nil, err
		}
		if strings.EqualFold(j.Project, cfg.Names.Control) {
			return nil, fmt.Errorf("%s 是总控目录，不能当项目", cfg.Names.Control)
		}
		if !fi.IsDir() {
			return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: "整项目归位只收文件夹"}}, nil
		}
		target, err := uniqueDir(filepath.Join(cfg.Root, j.Project))
		if err != nil {
			return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: err.Error()}}, nil
		}
		cross := rootDev != 0 && deviceOf(fi) != 0 && deviceOf(fi) != rootDev
		return []PreviewItem{{Source: src, Target: target, Action: "搬走", CrossDevice: cross, Whole: true}}, nil
	}
	if err := put.CheckName("项目名", j.Project); err != nil {
		return nil, err
	}
	if strings.EqualFold(j.Project, cfg.Names.Control) {
		return nil, fmt.Errorf("%s 是总控目录，不能当项目", cfg.Names.Control)
	}
	destDir, err := destDirFor(cfg, j)
	if err != nil {
		return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: err.Error()}}, nil
	}
	if fi.IsDir() {
		// 文件夹：整棵树归到 destDir/<名字>/
		target, err := uniqueDir(filepath.Join(destDir, filepath.Base(src)))
		if err != nil {
			return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: err.Error()}}, nil
		}
		cross := rootDev != 0 && deviceOf(fi) != 0 && deviceOf(fi) != rootDev
		return []PreviewItem{{Source: src, Target: target, Action: "搬走", CrossDevice: cross}}, nil
	}
	if !fi.Mode().IsRegular() {
		return []PreviewItem{{Source: src, Action: "跳过", Skip: true, Reason: "不是普通文件或文件夹"}}, nil
	}
	target := filepath.Join(destDir, filepath.Base(src))
	cross := rootDev != 0 && deviceOf(fi) != 0 && deviceOf(fi) != rootDev
	// 目标重名在执行时再加 -02；预览先显示理想名
	return []PreviewItem{{Source: src, Target: target, Action: "搬走", CrossDevice: cross}}, nil
}

func destDirFor(cfg config.Config, j Job) (string, error) {
	n := cfg.Names
	area := strings.TrimSpace(j.Area)
	if area == "" {
		area = AreaLoose
	}
	proj := filepath.Join(cfg.Root, j.Project)
	switch area {
	case AreaTasks, "tasks":
		base := filepath.Join(proj, n.Tasks)
		if t := strings.TrimSpace(j.Task); t != "" {
			if err := put.CheckName("任务名", t); err != nil {
				return "", err
			}
			return filepath.Join(base, t), nil
		}
		return base, nil
	case AreaOutputs, "outputs":
		base := filepath.Join(proj, n.Outputs)
		if t := strings.TrimSpace(j.Task); t != "" {
			if err := put.CheckName("任务名", t); err != nil {
				return "", err
			}
			return filepath.Join(base, t), nil
		}
		return base, nil
	case AreaMaterials, "materials":
		return filepath.Join(proj, n.Materials), nil
	case AreaCode, "code":
		return filepath.Join(proj, n.Code), nil
	case AreaLoose, "loose", "散落文件":
		return filepath.Join(proj, n.Materials, n.Loose), nil
	case AreaSensitive, "sensitive":
		return filepath.Join(proj, n.Materials, n.Sensitive), nil
	default:
		return "", fmt.Errorf("不认识的位置 %q（认：任务/成品/资料/代码/散落/敏感）", j.Area)
	}
}

func uniqueDir(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}
	for i := 2; i <= 99; i++ {
		cand := fmt.Sprintf("%s-%02d", path, i)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("目标已存在且 -02…-99 都占了：%s", path)
}

func deviceOfPath(p string) uint64 {
	for {
		fi, err := os.Stat(p)
		if err == nil {
			return deviceOf(fi)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0
		}
		p = parent
	}
}

func doOne(cfg config.Config, p PreviewItem, now time.Time) (ResultItem, *undoEntry, error) {
	ri := ResultItem{Source: p.Source, Target: p.Target, CrossDevice: p.CrossDevice}
	fi, err := os.Lstat(p.Source)
	if err != nil {
		return ri, nil, err
	}
	if !p.Whole {
		projName := firstPathComponent(cfg.Root, p.Target)
		if projName != "" {
			proj := filepath.Join(cfg.Root, projName)
			if _, err := os.Stat(proj); os.IsNotExist(err) {
				if err := makeSkeleton(cfg, proj, projName, now); err != nil {
					return ri, nil, err
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Target), 0o755); err != nil {
		return ri, nil, err
	}

	dst := p.Target
	if fi.IsDir() {
		dst, err = uniqueDir(p.Target)
	} else {
		dst, err = pickFileTarget(p.Target)
	}
	if err != nil {
		return ri, nil, err
	}
	if p.CrossDevice {
		err = moveCrossDevice(p.Source, dst)
	} else {
		err = os.Rename(p.Source, dst)
	}
	if err != nil {
		ri.Target = dst
		return ri, nil, fmt.Errorf("搬迁失败：%w", err)
	}
	ri.Target = dst
	ri.Action = "搬走"
	ri.Undoable = true
	u := &undoEntry{Mode: "move", From: p.Source, To: dst, CrossDevice: p.CrossDevice}
	return ri, u, nil
}

func firstPathComponent(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// pickFileTarget 选一个还不存在的文件路径（重名加 -02），不创建文件。
func pickFileTarget(want string) (string, error) {
	if _, err := os.Stat(want); os.IsNotExist(err) {
		return want, nil
	}
	ext := filepath.Ext(want)
	stem := strings.TrimSuffix(want, ext)
	for i := 2; i <= 999; i++ {
		cand := fmt.Sprintf("%s-%02d%s", stem, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("同名文件太多：%s", want)
}

func copyTreeOrFile(src, dst string, fi os.FileInfo) (string, string, error) {
	if fi.IsDir() {
		final, err := uniqueDir(dst)
		if err != nil {
			return dst, "", err
		}
		if err := copyDir(src, final); err != nil {
			return final, "", err
		}
		return final, "", nil
	}
	final, err := pickFileTarget(dst)
	if err != nil {
		return dst, "", err
	}
	sum, err := copyFileVerified(src, final, fi)
	return final, sum, err
}

func copyFileVerified(src, dst string, fi os.FileInfo) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("复制失败（目标已留在原处，没删）：%s：%w", dst, err)
	}
	want := hex.EncodeToString(h.Sum(nil))
	got, err := put.FileSHA256(dst)
	if err != nil {
		return want, err
	}
	srcNow, err := put.FileSHA256(src)
	if err != nil {
		return want, err
	}
	if got != want || srcNow != want {
		return want, fmt.Errorf("哈希对不上（源文件可能在复制时被改了）：%s；目标留着没删，请人工检查", dst)
	}
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return want, nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") && name != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if scan.IsSensitiveName(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // 跳过密钥文件，不复制
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		_, err = copyFileVerified(path, target, fi)
		return err
	})
}

func makeSkeleton(cfg config.Config, projDir, name string, now time.Time) error {
	n := cfg.Names
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return err
	}
	for _, d := range []string{n.Tasks, n.Outputs, n.Materials, n.Code} {
		if err := os.MkdirAll(filepath.Join(projDir, d), 0o755); err != nil {
			return err
		}
	}
	_ = os.MkdirAll(filepath.Join(projDir, n.Materials, n.Loose), 0o755)
	readme := filepath.Join(projDir, n.Readme)
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		body := fmt.Sprintf("# %s\n\n- 建立：%s（WorkShelf 一键归位）\n- 这是什么：（待补）\n", name, now.Format("2006-01-02"))
		f, err := os.OpenFile(readme, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, _ = f.WriteString(body)
			f.Close()
		}
	}
	return nil
}
