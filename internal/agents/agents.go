// Package agents 给各家 AI 的规则文件（AGENTS.md、SOUL.md、CLAUDE.md……）末尾追加一段
// 「写文件要留身份标识」的说明。
//
// 铁律：只追加，绝不覆盖或改动原有内容。追加前先备份，追加后逐字节核对原内容没变；
// 已经接入过就不再追加；卸载只删 WorkShelf 自己那一段。
package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	Begin = "<!-- BEGIN workshelf-identity v1 -->"
	End   = "<!-- END workshelf-identity v1 -->"
)

// Block 生成要追加的那一段（不含前后换行）。
func Block(agent string) string {
	if agent == "" {
		agent = "（填你的名字）"
	}
	return Begin + `
## WorkShelf：写文件要留身份标识

（这一段由 WorkShelf 追加；上面的内容是原来的，没有改动。卸载 WorkShelf 接入时只会删掉这一段。）

你的名字：` + agent + `

以后你每新建或修改一个要交给用户的文件，都要留下你的身份标识：

1. 首选命令：` + "`workshelf mark <文件...> --agent " + agent + " --工种 <行业·工种> [--项目 名] [--任务 名]`" + `
2. 没有 workshelf 命令时：在文件所在文件夹的 ` + "`.workshelf-origin.jsonl`" + ` 末尾追加一行 JSON（只追加，别改别人的行）：
   ` + "`" + `{"time":"<带时区的时间>","file":"<文件名>","agent":"` + agent + `","worktype":"<行业·工种>","action":"新建或修改"}` + "`" + `
3. 交给用户的成品用 ` + "`workshelf put <项目> <任务> <文件...> --工种 <行业·工种> --到 成品 --agent " + agent + "`" + `，状态是「待批准」，等用户批准。
4. 不要把文件放在桌面、下载、主目录顶层或 ~/Work 顶层。
` + End
}

// Segment 是实际追加进文件的字节：前后各一个换行。
func Segment(agent string) []byte { return []byte("\n" + Block(agent) + "\n") }

// Candidate 是找到的一个规则文件。
type Candidate struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`  // AGENTS.md / SOUL.md / CLAUDE.md / ...
	Agent     string `json:"agent"` // 猜的智能体名
	Size      int64  `json:"size"`
	Installed bool   `json:"installed"`
	Symlink   bool   `json:"symlink,omitempty"`
	Note      string `json:"note,omitempty"`
	// Scope：全局（主目录或 ~/.某家 下面，管这家 AI 的所有对话）/ 项目（某个仓库里的）。
	Scope string `json:"scope"`
	// Recommended：建议接入的（全局规则、仓库根目录的规则）。子目录里的、符号链接不建议。
	Recommended bool `json:"recommended"`
}

var ruleNames = map[string]string{
	"agents.md": "AGENTS.md", "soul.md": "SOUL.md", "claude.md": "CLAUDE.md", "gemini.md": "GEMINI.md",
	".cursorrules": ".cursorrules", ".windsurfrules": ".windsurfrules", "copilot-instructions.md": "copilot-instructions.md",
	"identity.md": "IDENTITY.md",
}

var skipDirs = map[string]bool{
	"node_modules": true, "Library": true, ".Trash": true, ".cache": true, ".npm": true, ".git": true,
	"vendor": true, "Pictures": true, "Music": true, "Movies": true, "Applications": true, ".local": true,
	"go": true, "sdk": true, ".rustup": true, ".cargo": true, ".docker": true, "dist": true, "build": true,
	"Caches": true, "logs": true, "sessions": true, "backups": true, "接入备份": true,
	// 缓存、技能包、备份副本、git worktree：不是 AI 自己的规则文件。
	".bun": true, "skills": true, "skills-cold": true, ".worktrees": true, "worktrees": true,
	"originals": true, "storage-audits": true, "_归档": true, "归档": true,
}

// Scan 只读：在 home 下找规则文件。dot 目录看 3 层，~/Work、~/Projects 等看 5 层。
func Scan(home string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		fi, err := os.Lstat(p)
		if err != nil {
			return
		}
		c := Candidate{Path: p, Kind: kindOf(p), Agent: GuessAgent(home, p)}
		if fi.Mode()&os.ModeSymlink != 0 {
			c.Symlink = true
			c.Note = "是快捷方式（符号链接），不直接改；请指向它的真实文件"
			if real, err := filepath.EvalSymlinks(p); err == nil {
				c.Note += "：" + real
			}
		} else if fi.IsDir() {
			// .cursor/rules，下面统一补说明
		} else if !fi.Mode().IsRegular() {
			return
		} else {
			c.Size = fi.Size()
			if b, err := os.ReadFile(p); err == nil {
				c.Installed = bytes.Contains(b, []byte(Begin))
			}
		}
		out = append(out, c)
	}
	walk := func(root string, maxDepth int) {
		base := strings.Count(filepath.Clean(root), string(filepath.Separator))
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			depth := strings.Count(p, string(filepath.Separator)) - base
			name := d.Name()
			if d.IsDir() {
				if p != root && (skipDirs[name] || depth > maxDepth) {
					return filepath.SkipDir
				}
				return nil
			}
			if _, ok := ruleNames[strings.ToLower(name)]; ok {
				add(p)
			} else if strings.HasSuffix(name, ".mdc") && filepath.Base(filepath.Dir(p)) == "rules" && filepath.Base(filepath.Dir(filepath.Dir(p))) == ".cursor" {
				// .cursor/rules 目录：不改已有规则，接入时新建一个 workshelf-identity.mdc。
				add(filepath.Dir(p))
			}
			return nil
		})
	}
	ents, _ := os.ReadDir(home)
	for _, e := range ents {
		n := e.Name()
		p := filepath.Join(home, n)
		switch {
		case e.Type()&os.ModeSymlink != 0:
			if _, ok := ruleNames[strings.ToLower(n)]; ok {
				add(p)
			}
		case e.IsDir() && strings.HasPrefix(n, ".") && !skipDirs[n]:
			walk(p, 3)
		case e.IsDir() && (n == "Work" || n == "Projects" || n == "Documents" || n == "clawd" || n == "code" || n == "src"):
			walk(p, 5)
		case !e.IsDir():
			if _, ok := ruleNames[strings.ToLower(n)]; ok {
				add(p)
			}
		}
	}
	for i := range out {
		if fi, err := os.Stat(out[i].Path); err == nil && fi.IsDir() {
			out[i].Kind = ".cursor/rules"
			out[i].Installed = exists(filepath.Join(out[i].Path, CursorRuleFile))
			out[i].Note = "Cursor 规则目录：不改已有规则，新建 " + CursorRuleFile
		}
	}
	for i := range out {
		classify(home, &out[i])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Recommended != out[j].Recommended {
			return out[i].Recommended
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func classify(home string, c *Candidate) {
	dir := filepath.Dir(c.Path)
	if c.Kind == ".cursor/rules" {
		dir = filepath.Dir(filepath.Dir(c.Path)) // .cursor 的上一层
	}
	rel, _ := filepath.Rel(home, dir)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case rel == ".":
		c.Scope, c.Recommended = "全局", true
	case strings.HasPrefix(parts[0], "."):
		c.Scope = "全局"
		c.Recommended = len(parts) <= 2 // ~/.codex/AGENTS.md、~/.openclaw/workspace/SOUL.md
	default:
		c.Scope = "项目"
		c.Recommended = exists(filepath.Join(dir, ".git"))
	}
	if c.Symlink {
		c.Recommended = false
	}
}

// CursorRuleFile 是在 .cursor/rules 目录里新建的文件名。
const CursorRuleFile = "workshelf-identity.mdc"

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func kindOf(p string) string {
	if k, ok := ruleNames[strings.ToLower(filepath.Base(p))]; ok {
		return k
	}
	return filepath.Base(p)
}

// GuessAgent 从路径猜是哪家 AI。
func GuessAgent(home, p string) string {
	rel, _ := filepath.Rel(home, p)
	low := strings.ToLower(rel)
	for _, k := range []struct{ key, name string }{
		{".grokbot", "grok"}, {".codex", "codex"}, {".claude", "claude"}, {".gemini", "gemini"},
		{".cursor", "cursor"}, {"openclaw", "openclaw"}, {"clawd", "openclaw"}, {".windsurf", "windsurf"},
		{".copilot", "copilot"}, {".workbuddy", "workbuddy"}, {".github", "copilot"}, {".hermes", "hermes"}, {".qwen", "qwen"}, {".kimi", "kimi"},
	} {
		if strings.Contains(low, k.key) {
			return k.name
		}
	}
	switch strings.ToLower(filepath.Base(p)) {
	case "claude.md":
		return "claude"
	case "gemini.md":
		return "gemini"
	case ".cursorrules":
		return "cursor"
	case ".windsurfrules":
		return "windsurf"
	case "copilot-instructions.md":
		return "copilot"
	}
	return ""
}

// Target 是一次接入/卸载的目标。
type Target struct {
	Path  string `json:"path"`
	Agent string `json:"agent"`
}

// Outcome 是一个目标的结果。
type Outcome struct {
	Path      string `json:"path"`
	Agent     string `json:"agent"`
	Action    string `json:"action"` // 追加 / 已接入，跳过 / 新建 / 已删除这一段 / 没接入过 / 预演
	Backup    string `json:"backup,omitempty"`
	BeforeSHA string `json:"before_sha256,omitempty"`
	AfterSHA  string `json:"after_sha256,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Options 控制 Install / Uninstall。
type Options struct {
	BackupRoot   string // 备份放在 <BackupRoot>/<前缀><时间>/
	BackupPrefix string // 备份目录名前缀，比如 "yy-"；只允许字母数字和 -_
	DryRun       bool   // 只说要做什么，什么都不写
	Now          time.Time
}

type manifestEntry struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
	Op     string `json:"op"`
}

type session struct {
	dir     string
	entries []manifestEntry
	opt     Options
}

func newSession(opt Options) (*session, error) {
	if opt.BackupRoot == "" && !opt.DryRun {
		return nil, errors.New("没给备份目录")
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	s := &session{opt: opt}
	if strings.ContainsAny(opt.BackupPrefix, "/\\:.") {
		return nil, errors.New("备份前缀里不能有 / \\ : .")
	}
	if !opt.DryRun {
		base := filepath.Join(opt.BackupRoot, opt.BackupPrefix+now.Format("20060102-150405"))
		dir := base
		for i := 2; exists(dir); i++ {
			dir = fmt.Sprintf("%s-%02d", base, i)
		}
		s.dir = dir
	}
	return s, nil
}

func (s *session) backup(path string, data []byte, op string) (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%02d-%s", len(s.entries)+1, strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(strings.TrimPrefix(path, "/")))
	dst := filepath.Join(s.dir, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		return "", fmt.Errorf("备份核对失败：%s", dst)
	}
	s.entries = append(s.entries, manifestEntry{Path: path, Backup: dst, SHA256: sum(data), Size: len(data), Op: op})
	return dst, s.writeManifest()
}

func (s *session) writeManifest() error {
	b, _ := json.MarshalIndent(s.entries, "", "  ")
	tmp := filepath.Join(s.dir, fmt.Sprintf(".manifest-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "manifest.json"))
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Install 给每个目标追加身份段。任何一个目标出错只影响它自己，其他照做。
func Install(targets []Target, opt Options) ([]Outcome, error) {
	s, err := newSession(opt)
	if err != nil {
		return nil, err
	}
	var out []Outcome
	for _, t := range targets {
		o := installOne(s, t)
		out = append(out, o)
	}
	return out, nil
}

func installOne(s *session, t Target) Outcome {
	o := Outcome{Path: t.Path, Agent: t.Agent}
	fail := func(err error) Outcome { o.Error = err.Error(); return o }
	if t.Agent == "" {
		return fail(errors.New("没给智能体名字（--agent）"))
	}
	if strings.ContainsAny(t.Agent, "\n\r`<>") {
		return fail(errors.New("智能体名字里有不允许的字符"))
	}
	fi, err := os.Lstat(t.Path)
	if err != nil {
		return fail(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fail(errors.New("是符号链接，不直接改；请对它指向的真实文件接入"))
	}
	if fi.IsDir() {
		return installCursorDir(s, t, o)
	}
	if !fi.Mode().IsRegular() {
		return fail(errors.New("不是普通文件"))
	}
	orig, err := os.ReadFile(t.Path)
	if err != nil {
		return fail(err)
	}
	o.BeforeSHA = sum(orig)
	if bytes.Contains(orig, []byte(Begin)) {
		o.Action = "已接入，跳过"
		o.AfterSHA = o.BeforeSHA
		return o
	}
	seg := Segment(t.Agent)
	if s.opt.DryRun {
		o.Action = "预演：会在末尾追加 " + fmt.Sprint(len(seg)) + " 字节"
		return o
	}
	if o.Backup, err = s.backup(t.Path, orig, "install"); err != nil {
		return fail(fmt.Errorf("备份失败，没动原文件：%w", err))
	}
	f, err := os.OpenFile(t.Path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fail(err)
	}
	if st, err := f.Stat(); err != nil || st.Size() != int64(len(orig)) {
		f.Close()
		return fail(errors.New("读完之后文件又被别人改了，这次没追加"))
	}
	if _, err := f.Write(seg); err != nil {
		f.Close()
		return fail(fmt.Errorf("追加失败（备份在 %s）：%w", o.Backup, err))
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	after, err := os.ReadFile(t.Path)
	if err != nil {
		return fail(err)
	}
	o.AfterSHA = sum(after)
	if !bytes.Equal(after[:min(len(orig), len(after))], orig) || !bytes.HasSuffix(after, seg) || len(after) != len(orig)+len(seg) {
		return fail(fmt.Errorf("核对失败：原内容和预期不一致，请用备份 %s 检查", o.Backup))
	}
	o.Action = "追加"
	return o
}

func installCursorDir(s *session, t Target, o Outcome) Outcome {
	p := filepath.Join(t.Path, CursorRuleFile)
	o.Path = p
	if exists(p) {
		o.Action = "已接入，跳过"
		return o
	}
	body := []byte("---\ndescription: WorkShelf 身份标识\nalwaysApply: true\n---\n" + Block(t.Agent) + "\n")
	if s.opt.DryRun {
		o.Action = "预演：会新建 " + CursorRuleFile
		return o
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		o.Error = err.Error()
		return o
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		o.Error = err.Error()
		return o
	}
	if err := f.Close(); err != nil {
		o.Error = err.Error()
		return o
	}
	o.AfterSHA = sum(body)
	o.Action = "新建"
	return o
}

// findSegment 找 "\n"+BEGIN…END+"\n" 这一整段的位置；找不到返回 -1。
func findSegment(b []byte) (int, int) {
	i := bytes.Index(b, []byte("\n"+Begin))
	if i < 0 {
		return -1, -1
	}
	j := bytes.Index(b[i:], []byte(End+"\n"))
	if j < 0 {
		return -1, -1
	}
	return i, i + j + len(End) + 1
}

// Uninstall 只删 WorkShelf 自己追加的那一段；其余字节一个不动。
func Uninstall(paths []string, opt Options) ([]Outcome, error) {
	s, err := newSession(opt)
	if err != nil {
		return nil, err
	}
	var out []Outcome
	for _, p := range paths {
		out = append(out, uninstallOne(s, p))
	}
	return out, nil
}

func uninstallOne(s *session, p string) Outcome {
	o := Outcome{Path: p}
	fail := func(err error) Outcome { o.Error = err.Error(); return o }
	fi, err := os.Lstat(p)
	if err != nil {
		return fail(err)
	}
	if fi.IsDir() {
		rule := filepath.Join(p, CursorRuleFile)
		o.Path = rule
		if !exists(rule) {
			o.Action = "没接入过"
			return o
		}
		if s.opt.DryRun {
			o.Action = "预演：会把 " + CursorRuleFile + " 挪进备份"
			return o
		}
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return fail(err)
		}
		dst := filepath.Join(s.dir, fmt.Sprintf("%02d-%s", len(s.entries)+1, CursorRuleFile))
		data, _ := os.ReadFile(rule)
		if err := os.Rename(rule, dst); err != nil {
			return fail(err)
		}
		s.entries = append(s.entries, manifestEntry{Path: rule, Backup: dst, SHA256: sum(data), Size: len(data), Op: "uninstall-moved"})
		o.Backup = dst
		o.Action = "已挪进备份"
		return o
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return fail(errors.New("不是普通文件"))
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fail(err)
	}
	o.BeforeSHA = sum(b)
	i, j := findSegment(b)
	if i < 0 {
		if bytes.Contains(b, []byte(Begin)) {
			return fail(errors.New("找到了开头标记但这一段被改动过（前后换行或结尾标记不对），为了安全没动，请手工处理"))
		}
		o.Action = "没接入过"
		return o
	}
	if s.opt.DryRun {
		o.Action = fmt.Sprintf("预演：会删掉第 %d–%d 字节这一段", i, j)
		return o
	}
	if o.Backup, err = s.backup(p, b, "uninstall"); err != nil {
		return fail(fmt.Errorf("备份失败，没动原文件：%w", err))
	}
	nb := append(append([]byte{}, b[:i]...), b[j:]...)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fail(err)
	}
	if _, err := f.Write(nb); err != nil {
		f.Close()
		return fail(fmt.Errorf("写回失败，请用备份 %s 恢复：%w", o.Backup, err))
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	after, _ := os.ReadFile(p)
	o.AfterSHA = sum(after)
	if !bytes.Equal(after, nb) {
		return fail(fmt.Errorf("核对失败，请用备份 %s 恢复", o.Backup))
	}
	o.Action = "已删除这一段"
	return o
}

// Installed 判断一个文件（或 .cursor/rules 目录）是否已接入。
func Installed(p string) bool {
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return exists(filepath.Join(p, CursorRuleFile))
	}
	b, err := os.ReadFile(p)
	return err == nil && bytes.Contains(b, []byte(Begin))
}
