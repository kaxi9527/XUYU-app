// Package put 把文件复制进 ~/Work/<项目>/任务/<日期>-<任务名>/，核对哈希，再登记到资产索引。
// 只复制，绝不移动、绝不删除源文件，绝不覆盖已有文件。
package put

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// Options 是一次 put 的参数。
type Options struct {
	Config     config.Config
	Project    string
	Task       string
	Files      []string
	Append     bool   // 当天同名任务目录已存在时，往里追加（默认新建 -02）
	NewProject bool   // 项目不存在时新建骨架
	Agent      string // 负责智能体，也决定写哪个索引分片；空则 WORKSHELF_AGENT 环境变量，再空则 workshelf
	Now        time.Time

	// 以下是 v4（对齐工种表）新增。
	WorkType string // 工种：「行业·工种」或工种名；库里允许空（只给警告），命令行要求必填
	Template string // 模板 T01…T15；工种表里查得到就可以不给
	To       string // 放到哪：任务（默认）/ 成品 / 资料 / 敏感
	Sub      string // 在目标目录下再放进哪个子文件夹（可空，可多层，用 / 分隔）
	Period   string // 期间（报表类常用，如 2026-10）
	Note     string // 一句话说明
	Version  string // 版本标识（如 v2、终稿）
	// Scanner 是密钥扫描器；nil 表示用 PATH 上的 gitleaks。
	Scanner SecretScanner
}

// 「放到哪」的四种取值。
const (
	ToTask      = "任务"
	ToOutput    = "成品"
	ToMaterial  = "资料"
	ToSensitive = "敏感"
)

// 索引里的状态。
const (
	StatusProcess  = "过程"
	StatusPending  = "待批准"
	StatusDone     = "已交"
	StatusMaterial = "资料"
)

// Stored 是一个已入库的文件。
type Stored struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Row    int    `json:"index_row"`
	Status string `json:"status"`
}

// Result 是 put 的结果。
type Result struct {
	ProjectDir     string   `json:"project_dir"`
	TaskDir        string   `json:"task_dir,omitempty"`
	DestDir        string   `json:"dest_dir"`
	WorkType       string   `json:"worktype,omitempty"`
	Template       string   `json:"template,omitempty"`
	NotePath       string   `json:"note_path,omitempty"`
	CreatedProject bool     `json:"created_project"`
	Files          []Stored `json:"files"`
	IndexPath      string   `json:"index_path"`
	Warnings       []string `json:"warnings,omitempty"`
}

var badName = regexp.MustCompile(`[/\\:\x00-\x1f]`)

// CheckName 检查项目名、任务名能不能直接当文件夹名。
func CheckName(kind, s string) error {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return fmt.Errorf("%s不能为空", kind)
	case t != s:
		return fmt.Errorf("%s前后不能有空格：%q", kind, s)
	case t == "." || t == ".." || strings.HasPrefix(t, "."):
		return fmt.Errorf("%s不能以点开头：%q", kind, s)
	case badName.MatchString(t):
		return fmt.Errorf("%s里不能有 / \\ : 或控制字符：%q", kind, s)
	case len(t) > 120:
		return fmt.Errorf("%s太长", kind)
	}
	return nil
}

// Put 执行入库。v4：按工种登记，--到 决定放进 任务/成品/资料/敏感。任何一步出错都直接停，已复制好的文件保留（不删），并在错误里说明。
func Put(opt Options) (*Result, error) {
	cfg := opt.Config
	n := cfg.Names
	if err := CheckName("项目名", opt.Project); err != nil {
		return nil, err
	}
	if strings.EqualFold(opt.Project, n.Control) {
		return nil, fmt.Errorf("%s 是总控目录，不能当项目", n.Control)
	}
	if err := CheckName("任务名", opt.Task); err != nil {
		return nil, err
	}
	if len(opt.Files) == 0 {
		return nil, errors.New("没给要入库的文件")
	}
	agent := opt.Agent
	if agent == "" {
		agent = os.Getenv("WORKSHELF_AGENT")
	}
	if agent == "" {
		agent = "workshelf"
	}
	if err := CheckName("智能体名", agent); err != nil {
		return nil, err
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(cfg.Location())

	res := &Result{}
	wt, err := resolveWorkType(opt, res)
	if err != nil {
		return nil, err
	}
	to := opt.To
	if to == "" {
		to = ToTask
	}
	switch to {
	case ToTask, ToOutput, ToMaterial, ToSensitive:
	default:
		return nil, fmt.Errorf("--到 只认 任务 / 成品 / 资料 / 敏感，不认 %q", opt.To)
	}
	sub, err := cleanSub(opt.Sub)
	if err != nil {
		return nil, err
	}

	// 1. 先把源文件全部检查一遍，有一个不行就一个都不复制。
	type src struct {
		path string
		info os.FileInfo
	}
	var srcs []src
	seen := map[string]bool{}
	for _, f := range opt.Files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("读不到源文件：%w", err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("第一版只收普通文件，这个不是：%s", abs)
		}
		if scan.IsSensitiveName(filepath.Base(abs)) {
			return nil, fmt.Errorf("拒绝：文件名像密钥/凭据，不入库：%s", filepath.Base(abs))
		}
		if (wt.sensitive || to == ToSensitive) && personalNumber.MatchString(filepath.Base(abs)) {
			return nil, fmt.Errorf("拒绝：🔒 工种的文件名里不能带手机号/证件号/卡号这类长数字，请先改名：%s", filepath.Base(abs))
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		srcs = append(srcs, src{abs, fi})
	}

	// 2. 项目目录：先确认存在（或允许新建），再扫密钥，最后才动手建东西。
	projDir := filepath.Join(cfg.Root, opt.Project)
	res.ProjectDir = projDir
	needCreate := false
	if fi, err := os.Stat(projDir); err != nil {
		if !os.IsNotExist(err) {
			return res, err
		}
		if !opt.NewProject {
			return res, fmt.Errorf("项目不存在：%s（确认要新建就加 --new-project）", projDir)
		}
		needCreate = true
	} else if !fi.IsDir() {
		return res, fmt.Errorf("项目路径不是文件夹：%s", projDir)
	}

	sc := opt.Scanner
	if sc == nil {
		sc = Gitleaks{}
	}
	paths := make([]string, len(srcs))
	for i, s := range srcs {
		paths[i] = s.path
	}
	warn, err := sc.Scan(paths)
	if warn != "" {
		res.Warnings = append(res.Warnings, warn)
	}
	if err != nil {
		return res, err
	}
	if needCreate {
		if err := makeSkeleton(cfg, projDir, opt.Project, now); err != nil {
			return res, err
		}
		res.CreatedProject = true
	}

	// 3. 任务目录：<日期>-<任务名>，已存在就 -02、-03……（除非 --append）。资料/敏感不建任务目录。
	taskName := opt.Task
	if to == ToTask || to == ToOutput {
		tasks := filepath.Join(projDir, n.Tasks)
		if err := os.MkdirAll(tasks, 0o755); err != nil {
			return res, err
		}
		base := now.Format("2006-01-02") + "-" + opt.Task
		for i := 1; ; i++ {
			name := base
			if i > 1 {
				name = fmt.Sprintf("%s-%02d", base, i)
			}
			td := filepath.Join(tasks, name)
			err := os.Mkdir(td, 0o755)
			if err == nil {
				res.TaskDir = td
				break
			}
			if !os.IsExist(err) {
				return res, err
			}
			// 成品默认归到当天同名任务（同一件事的过程和成品同名），不另起 -02。
			if opt.Append || (to == ToOutput && i == 1) {
				if fi, e := os.Stat(td); e == nil && fi.IsDir() {
					res.TaskDir = td
					break
				}
			}
			if i > 99 {
				return res, errors.New("同名任务目录太多（超过 99 个）")
			}
		}
		taskName = filepath.Base(res.TaskDir)
		res.NotePath = filepath.Join(res.TaskDir, "说明.md")
		_, statErr := os.Stat(res.NotePath)
		if err := writeTaskNote(res.NotePath, opt, wt, agent, to, now); err != nil {
			return res, err
		}
		if statErr == nil && to == ToOutput {
			if err := appendLine(res.NotePath, fmt.Sprintf("- 状态：%s（成品入库于 %s，%s）", StatusPending, now.Format(time.RFC3339), agent)); err != nil {
				return res, err
			}
		}
	}
	status := StatusProcess
	switch to {
	case ToTask:
		res.DestDir = res.TaskDir
	case ToOutput:
		res.DestDir = filepath.Join(projDir, n.Outputs, taskName)
		status = StatusPending
	case ToMaterial:
		res.DestDir = filepath.Join(projDir, n.Materials)
		status = StatusMaterial
	case ToSensitive:
		res.DestDir = filepath.Join(projDir, n.Materials, n.Sensitive)
		status = StatusMaterial
	}
	if sub != "" {
		res.DestDir = filepath.Join(res.DestDir, sub)
	}
	mode := os.FileMode(0o755)
	if to == ToSensitive {
		mode = 0o700
	}
	if err := os.MkdirAll(res.DestDir, mode); err != nil {
		return res, err
	}
	if to == ToSensitive {
		_ = os.Chmod(filepath.Join(projDir, n.Materials, n.Sensitive), 0o700)
	}
	if wt.label != "" {
		if err := ensureProjectWorkType(filepath.Join(projDir, n.Readme), wt.label); err != nil {
			res.Warnings = append(res.Warnings, "项目说明里没能补上工种："+err.Error())
		}
	}

	// 4. 复制 + 核对。
	for _, s := range srcs {
		dst, size, sum, err := copyVerified(s.path, res.DestDir, s.info)
		if err != nil {
			return res, err
		}
		if to == ToSensitive {
			_ = os.Chmod(dst, 0o600)
		}
		res.Files = append(res.Files, Stored{Source: s.path, Path: dst, Size: size, SHA256: sum, Status: status})
	}

	// 5. 登记资产索引（一行一个文件）+ 活动记录。
	idx := filepath.Join(cfg.Root, n.Control, n.IndexShards, n.IndexFile+"."+agent+".csv")
	res.IndexPath = idx
	extra := ""
	if opt.Period != "" {
		b, _ := json.Marshal(map[string]string{"期间": opt.Period})
		extra = string(b)
	}
	sens := ""
	if wt.sensitive || to == ToSensitive {
		sens = "是"
	}
	var rows []Row
	var recs []activity.Record
	for _, f := range res.Files {
		rows = append(rows, Row{
			Date: now.Format("2006-01-02"), Project: opt.Project, WorkType: wt.label, Template: wt.template,
			Task: taskName, Kind: worktype.KindOf(wt.template), Version: opt.Version, Status: f.Status,
			Path: relTo(cfg.Root, f.Path), NotePath: relTo(cfg.Root, res.NotePath), Agent: agent, Sensitive: sens,
			Note: opt.Note, Extra: extra, Time: now.Format(time.RFC3339), Source: f.Source, Size: f.Size, SHA256: f.SHA256,
		})
		recs = append(recs, activity.Record{
			Time: now.Format(time.RFC3339), Path: f.Path, SHA256: f.SHA256, Agent: agent, WorkType: wt.label,
			Project: opt.Project, Task: taskName, Source: activity.SourcePut, Action: "入库", Status: f.Status, Note: opt.Note,
		})
	}
	first, err := AppendRows(idx, rows)
	if err != nil {
		return res, fmt.Errorf("文件已复制好，但登记索引失败：%w", err)
	}
	for i := range res.Files {
		res.Files[i].Row = first + i
	}
	if err := activity.Append(activity.Dir(cfg), cfg.Location(), recs...); err != nil {
		res.Warnings = append(res.Warnings, "活动记录没写上（索引已登记）："+err.Error())
	}
	return res, nil
}

// 手机号、身份证号、银行卡号这类 11 位以上的连续数字。
var personalNumber = regexp.MustCompile(`\d{11,}|\d{17}[Xx]`)

type resolvedWT struct {
	label, template string
	sensitive       bool
}

func resolveWorkType(opt Options, res *Result) (resolvedWT, error) {
	tpl := strings.ToUpper(strings.TrimSpace(opt.Template))
	if tpl != "" && !worktype.ValidTemplate(tpl) {
		return resolvedWT{}, fmt.Errorf("模板只认 T01…T15，不认 %q", opt.Template)
	}
	if strings.TrimSpace(opt.WorkType) == "" {
		res.Warnings = append(res.Warnings, "⚠️ 没给工种：这批文件在项目详情里会排在「未标工种」一栏。")
		return resolvedWT{template: tpl}, nil
	}
	w, err := worktype.Resolve(opt.WorkType)
	if err != nil {
		if tpl == "" {
			return resolvedWT{}, err
		}
		if e := CheckName("工种", strings.TrimSpace(opt.WorkType)); e != nil {
			return resolvedWT{}, e
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("工种表里没有「%s」，按新工种登记（模板 %s）。", opt.WorkType, tpl))
		r := resolvedWT{label: strings.TrimSpace(opt.WorkType), template: tpl}
		res.WorkType, res.Template = r.label, r.template
		return r, nil
	}
	if tpl == "" {
		tpl = w.Template
	}
	r := resolvedWT{label: w.Label(), template: tpl, sensitive: w.Sensitive}
	res.WorkType, res.Template = r.label, r.template
	return r, nil
}

func cleanSub(s string) (string, error) {
	s = strings.Trim(strings.TrimSpace(s), "/")
	if s == "" {
		return "", nil
	}
	var parts []string
	for _, p := range strings.Split(s, "/") {
		if err := CheckName("子文件夹名", p); err != nil {
			return "", err
		}
		parts = append(parts, p)
	}
	return filepath.Join(parts...), nil
}

func relTo(root, p string) string {
	if p == "" {
		return ""
	}
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// writeTaskNote 给新任务目录写 说明.md；已经有了（--append）就不动。
func writeTaskNote(path string, opt Options, wt resolvedWT, agent, to string, now time.Time) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	status := "进行中"
	if to == ToOutput {
		status = StatusPending
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", opt.Task)
	fmt.Fprintf(&b, "- 工种：%s\n", orTBD(wt.label))
	fmt.Fprintf(&b, "- 模板：%s\n", orTBD(wt.template))
	fmt.Fprintf(&b, "- 状态：%s\n", status)
	fmt.Fprintf(&b, "- 负责智能体：%s\n", agent)
	fmt.Fprintf(&b, "- 一句话说明：%s\n", orTBD(opt.Note))
	if opt.Period != "" {
		fmt.Fprintf(&b, "- 期间：%s\n", opt.Period)
	}
	fmt.Fprintf(&b, "- 建立：%s（WorkShelf put）\n", now.Format(time.RFC3339))
	return writeNew(path, []byte(b.String()))
}

func orTBD(s string) string {
	if s == "" {
		return "（待补）"
	}
	return s
}

// EnsureProjectWorkType 项目 00-说明.md 里没写这个工种，就在末尾追加一行（只追加，不改原内容）。
func EnsureProjectWorkType(readme, label string) error {
	return ensureProjectWorkType(readme, label)
}

// ensureProjectWorkType 项目 00-说明.md 里没写这个工种，就在末尾追加一行（只追加，不改原内容）。
func ensureProjectWorkType(readme, label string) error {
	b, err := os.ReadFile(readme)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if (strings.HasPrefix(t, "- 工种：") || strings.HasPrefix(t, "- 工种:")) && strings.Contains(t, label) {
			return nil
		}
	}
	f, err := os.OpenFile(readme, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	pre := ""
	if len(b) > 0 && b[len(b)-1] != '\n' {
		pre = "\n"
	}
	if _, err := f.WriteString(pre + "- 工种：" + label + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func makeSkeleton(cfg config.Config, projDir, name string, now time.Time) error {
	n := cfg.Names
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(projDir, 0o755); err != nil {
		return err
	}
	for _, d := range []string{n.Tasks, n.Outputs, n.Materials} {
		if err := os.MkdirAll(filepath.Join(projDir, d), 0o755); err != nil {
			return err
		}
	}
	readme := fmt.Sprintf("# %s\n\n- 建立：%s（WorkShelf put --new-project）\n- 这是什么：（待补）\n- 当前在做：（待补）\n\n| 目录 | 放什么 |\n|---|---|\n| %s/ | 每次任务一个 YYYY-MM-DD-任务名 文件夹，放过程文件 |\n| %s/ | 给人看的定稿 |\n| %s/ | 参考资料 |\n",
		name, now.Format("2006-01-02"), n.Tasks, n.Outputs, n.Materials)
	return writeNew(filepath.Join(projDir, n.Readme), []byte(readme))
}

// copyVerified 用 O_EXCL 新建目标（重名加 -02），边复制边算 sha256，写完再从磁盘重读核对。
func copyVerified(srcPath, dir string, fi os.FileInfo) (string, int64, string, error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return "", 0, "", err
	}
	defer in.Close()
	name := filepath.Base(srcPath)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	var out *os.File
	var dst string
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s-%02d%s", stem, i, ext)
		}
		dst = filepath.Join(dir, cand)
		out, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) || i > 999 {
			return "", 0, "", err
		}
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return dst, size, "", fmt.Errorf("复制失败（目标已留在原处，没删）：%s：%w", dst, err)
	}
	want := hex.EncodeToString(h.Sum(nil))
	got, err := FileSHA256(dst)
	if err != nil {
		return dst, size, want, err
	}
	srcNow, err := FileSHA256(srcPath)
	if err != nil {
		return dst, size, want, err
	}
	if got != want || srcNow != want {
		return dst, size, want, fmt.Errorf("哈希对不上（源文件可能在复制时被改了）：%s；目标留着没删，请人工检查", dst)
	}
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return dst, size, want, nil
}

// FileSHA256 算文件的 sha256。
func FileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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
