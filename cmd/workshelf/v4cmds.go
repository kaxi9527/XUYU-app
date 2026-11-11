package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/agents"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/thumb"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func dual(fs *flag.FlagSet, p *string, zh, en, use string) {
	fs.StringVar(p, zh, "", use)
	fs.StringVar(p, en, "", use+"（同 --"+zh+"）")
}

func cmdMark(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mark", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	agent := fs.String("agent", "", "你是谁（默认 $WORKSHELF_AGENT）")
	action := fs.String("action", "", "新建 / 修改（默认按文件是否刚建猜不出就写「写入」）")
	var wt, proj, task, note string
	dual(fs, &wt, "工种", "worktype", "行业·工种")
	dual(fs, &proj, "项目", "project", "项目名")
	dual(fs, &task, "任务", "task", "任务名")
	dual(fs, &note, "说明", "note", "一句话说明")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("用法：workshelf mark <文件...> --agent <名字> [--工种 行业·工种]")
	}
	if wt != "" {
		if w, err := worktype.Resolve(wt); err == nil {
			wt = w.Label()
		} else {
			fmt.Fprintln(stderr, "提示：", err, "（照原样记下）")
		}
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	recs, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: pos, Agent: *agent, WorkType: wt, Project: proj, Task: task, Note: note, Action: *action})
	if err != nil {
		return err
	}
	for _, r := range recs {
		fmt.Fprintf(stdout, "%s  %s  %s\n", r.Agent, r.Path, r.SHA256[:12])
	}
	fmt.Fprintf(stderr, "已记下 %d 个文件是 %s 写的（活动记录 + 文件旁的 %s）。\n", len(recs), recs[0].Agent, activity.SidecarName)
	return nil
}

func cmdApprove(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	by := fs.String("by", "", "批准人（默认 $USER）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("用法：workshelf approve <项目> <任务目录名，如 2026-11-05-双11主图>")
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	res, err := put.Approve(put.ApproveOptions{Config: cfg, Project: pos[0], Task: pos[1], By: *by})
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		_, err := fmt.Fprintln(stdout, string(b))
		return err
	}
	for _, f := range res.Files {
		fmt.Fprintln(stdout, f)
	}
	for _, f := range res.Changed {
		fmt.Fprintln(stderr, "⚠️ 入库后内容变过（照样批准了，已按现在的内容登记）：", f)
	}
	fmt.Fprintf(stderr, "已批准 %d 个成品，状态改为「已交」。索引：%s\n", len(res.Files), res.IndexPath)
	return nil
}

func cmdWorkTypes(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "infer" || args[0] == "认出" || args[0] == "识别") {
		return cmdWorkTypesInfer(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("worktypes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	q := strings.Join(pos, " ")
	var list []worktype.WorkType
	for _, w := range worktype.All() {
		if q == "" || strings.Contains(w.Label(), q) || strings.Contains(w.Merged, q) || w.Template == strings.ToUpper(q) {
			list = append(list, w)
		}
	}
	if *asJSON {
		b, _ := json.MarshalIndent(list, "", "  ")
		_, err := fmt.Fprintln(stdout, string(b))
		return err
	}
	for _, w := range list {
		lock := ""
		if w.Sensitive {
			lock = " 🔒"
		}
		fmt.Fprintf(stdout, "%s\t%s%s\n", w.Template, w.Label(), lock)
	}
	return nil
}

const agentsUsage = `workshelf agents —— 给各家 AI 的规则文件追加「写文件要留身份标识」那一段

  workshelf agents scan [--home 目录] [--all] [--json]
      只读：找 AGENTS.md / SOUL.md / CLAUDE.md / GEMINI.md / .cursorrules / .cursor/rules 等，列出来给你确认。
  workshelf agents inventory|daily|watch [--home 目录] [--state-dir 目录] [--json]
      inventory 立即检查安装/规则线索并保存回执；daily 每 24 小时至多一次，重启可续；watch 前台持续检查。
      默认不修改规则；daily/watch 仅按已开启的接入政策写入。watch 退出后停止；本命令不安装开机启动项。
  workshelf agents enroll --agent codex|claude|gemini [--yes] [--home 目录] [--state-dir 目录]
      预览真实规则入口和追加正文；--yes 先备份/记意图，再追加或新建并回读。
  workshelf agents policy [--enable --agents codex,claude,gemini | --disable] [--ignore gemini] [--yes]
      无参数查看政策；修改默认只预览，--yes 才生效。首次开启同时接入已发现的所选工具。
      关闭保留已写规则；已写规则后来被移除/改坏时不自动补回，可用 enroll 显式重新接入。
  workshelf agents status <文件...>
      看哪些已经接入。
  workshelf agents install <文件...> --agent <名字> [--yes]
      不加 --yes 只预演。加了才真的在每个文件末尾追加一段（先备份到 _总控/接入备份/[--backup-prefix]<时间>/，
      追加后逐字节核对原内容没变；已接入的跳过；.cursor/rules 目录是新建 workshelf-identity.mdc）。
      多个文件不同名字时，可写成 文件=名字。
  workshelf agents uninstall <文件...> [--yes]
      只删 WorkShelf 自己那一段（同样先备份），其余内容一个字节不动。
`

func cmdAgents(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, agentsUsage)
		return nil
	}
	sub := args[0]
	if sub == "enroll" || sub == "policy" {
		return cmdAgentEnrollment(sub, args[1:], stdout, stderr)
	}
	if sub == "inventory" || sub == "daily" || sub == "watch" {
		return cmdAgentDiscovery(sub, args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("agents "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	homeFlag := fs.String("home", "", "在哪个主目录里找（默认你的主目录）")
	agent := fs.String("agent", "", "智能体名字")
	yes := fs.Bool("yes", false, "真的执行（不加只预演）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	prefix := fs.String("backup-prefix", "", "备份目录名前缀（比如 yy-）")
	all := fs.Bool("all", false, "scan 时连不建议接入的也列出来（子目录、符号链接）")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}
	emit := func(v any) error {
		b, _ := json.MarshalIndent(v, "", "  ")
		_, err := fmt.Fprintln(stdout, string(b))
		return err
	}
	switch sub {
	case "scan":
		home := *homeFlag
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return err
			}
		}
		cs := agents.Scan(home)
		hidden := 0
		if !*all {
			var keep []agents.Candidate
			for _, c := range cs {
				if c.Recommended {
					keep = append(keep, c)
				} else {
					hidden++
				}
			}
			cs = keep
		}
		if *asJSON {
			return emit(cs)
		}
		for _, c := range cs {
			st := "未接入"
			if c.Installed {
				st = "已接入"
			}
			a := c.Agent
			if a == "" {
				a = "?"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s", st, c.Scope, a, c.Kind, c.Path)
			if c.Note != "" {
				fmt.Fprintf(stdout, "\t（%s）", c.Note)
			}
			fmt.Fprintln(stdout)
		}
		if hidden > 0 {
			fmt.Fprintf(stderr, "另有 %d 个不建议接入的（仓库子目录里的、符号链接）没列，要看加 --all。\n", hidden)
		}
		fmt.Fprintf(stderr, "找到 %d 个。这一步只读，什么都没改。确认要接入哪些后：workshelf agents install <文件> --agent <名字> --yes\n", len(cs))
		return nil
	case "status":
		if len(pos) == 0 {
			return errors.New("用法：workshelf agents status <文件...>")
		}
		for _, p := range pos {
			st := "未接入"
			if agents.Installed(p) {
				st = "已接入"
			}
			fmt.Fprintf(stdout, "%s\t%s\n", st, p)
		}
		return nil
	case "install", "uninstall":
		if len(pos) == 0 {
			return fmt.Errorf("用法：workshelf agents %s <文件...>", sub)
		}
		cfg, _, _, err := loadConfig(*cfgPath)
		if err != nil {
			return err
		}
		opt := agents.Options{BackupRoot: filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.AgentBackup), BackupPrefix: *prefix, DryRun: !*yes}
		var out []agents.Outcome
		if sub == "install" {
			var ts []agents.Target
			for _, p := range pos {
				t := agents.Target{Path: p, Agent: *agent}
				if i := strings.LastIndex(p, "="); i > 0 {
					t.Path, t.Agent = p[:i], p[i+1:]
				}
				if abs, err := filepath.Abs(t.Path); err == nil {
					t.Path = abs
				}
				if t.Agent == "" {
					home, _ := os.UserHomeDir()
					t.Agent = agents.GuessAgent(home, t.Path)
				}
				ts = append(ts, t)
			}
			out, err = agents.Install(ts, opt)
		} else {
			var ps []string
			for _, p := range pos {
				abs, _ := filepath.Abs(p)
				ps = append(ps, abs)
			}
			out, err = agents.Uninstall(ps, opt)
		}
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(out)
		}
		failed := 0
		for _, o := range out {
			if o.Error != "" {
				failed++
				fmt.Fprintf(stdout, "✗ %s：%s\n", o.Path, o.Error)
				continue
			}
			fmt.Fprintf(stdout, "✓ %s：%s", o.Path, o.Action)
			if o.Backup != "" {
				fmt.Fprintf(stdout, "（备份：%s）", o.Backup)
			}
			fmt.Fprintln(stdout)
		}
		if !*yes {
			fmt.Fprintln(stderr, "以上是预演，什么都没改。确认后加 --yes 再跑一次。")
		}
		if failed > 0 {
			return fmt.Errorf("%d 个没做成", failed)
		}
		return nil
	}
	return fmt.Errorf("不认识的 agents 子命令：%s\n\n%s", sub, agentsUsage)
}

// cmdRegister：原地登记。只读扫描，把还在原处的项目登记到 总控/原地登记/。
func cmdRegister(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	asJSON := fs.Bool("json", false, "输出 JSON")
	dry := fs.Bool("dry", false, "只看结果，不写登记表")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	opt, err := registry.ScanOptions(cfg, home, nil, false, false)
	if err != nil {
		return err
	}
	reg, err := registry.Build(cfg, home, opt)
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(reg, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, p := range reg.Projects {
			fmt.Fprintf(stdout, "%s\t%s\t副本/相关 %d\n", p.Name, short(p.Path, home), len(p.Members)-1)
		}
	}
	if *dry {
		fmt.Fprintf(stderr, "识别出 %d 个原地项目（另有 %d 个零散图片文件夹没单独列）。--dry：没写登记表。\n", len(reg.Projects), reg.Hidden)
		return nil
	}
	path, err := registry.Save(cfg, reg, home)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "识别出 %d 个原地项目（另有 %d 个零散图片文件夹没单独列），登记表：%s。没有搬动任何文件。\n", len(reg.Projects), reg.Hidden, path)
	return nil
}

func short(p, home string) string {
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// cmdThumb：调试用，看某个文件的缩略图走哪条路。
func cmdThumb(args []string, stdout io.Writer) error {
	home, _ := os.UserHomeDir()
	g, err := thumb.New("", home, 160)
	if err != nil {
		return err
	}
	for _, a := range args {
		abs, _ := filepath.Abs(a)
		r, err := g.Get(abs)
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\tdata=%d字节\tlines=%d", abs, r.Kind, r.Group, r.Via, len(r.Data), len(r.Lines))
		if err != nil {
			fmt.Fprintf(stdout, "\t错误：%v", err)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}
