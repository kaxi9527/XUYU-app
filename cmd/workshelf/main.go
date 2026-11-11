// Command workshelf 帮你把散落在电脑各处的项目和文件收进 ~/Work。
// 第一版：scan（只读扫描）、plan（只出整理计划）、put（复制入库 + 登记索引）。
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
	"time"
	_ "time/tzdata" // 内置时区数据，不依赖系统

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/plan"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// Version 是版本号；发布时可用 -ldflags "-X main.Version=..." 覆盖。
var Version = "0.8.2-dev"

const usage = `WorkShelf %s — 把散落的项目和文件收进一个总文件夹（默认 ~/Work）

用法：
  workshelf scan [路径...] [--volumes] [--json] [--out 文件] [--no-git-status]
      只读扫描。不给路径就扫配置里的默认范围（桌面、下载、文稿、Projects、主目录第一层）。
  workshelf plan [路径...] [--from 扫描.json] [--out 目录] [--volumes] [--no-git-status]
      生成整理计划（Markdown + JSON），只出计划，什么都不挪。
      默认写到 <总文件夹>/_总控/整理日志/。
  workshelf put <项目> <任务> <文件...> --工种 <行业·工种> [--到 任务|成品|资料|敏感] [--放 子文件夹]
                [--模板 Txx] [--期间 2026-10] [--说明 一句话] [--版本 v2] [--new-project] [--append] [--agent 名字] [--json]
      把文件复制进项目（默认 任务/<日期>-<任务>/；--到 成品 放 成品/<日期>-<任务>/，状态「待批准」），
      核对 sha256，登记资产索引和活动记录。只复制，不移动、不删除源文件，不覆盖已有文件。
  workshelf approve <项目> <任务目录名> [--by 名字]
      批准 成品/<任务目录名>/ 里的成品：索引追加「已交」行，任务 说明.md 追加状态。不动文件本身。
  workshelf mark <文件...> --agent <名字> [--工种 行业·工种] [--项目 名] [--任务 名] [--说明 …]
      智能体自报「这个文件是我写的」（写活动记录 + 文件旁的 .workshelf-origin.jsonl）。
  workshelf auto list [--project 项目] [--json]
      查看自动整理记录及 ID，供工作者核对。
  workshelf auto reject <整理记录 ID> --agent <工作者>
      反对指定的整理，并按记录恢复文件。
  workshelf worktypes [关键词] [--json]
  workshelf worktypes infer <项目> [--json] [--confirm]
  workshelf allocate <项目> [--apply] [--json]
  workshelf graph projects|files [<项目>] [--json]
      列出工种表（v4，88 个工种）。
  workshelf agents scan|status|install|uninstall …
      给各家 AI 的规则文件追加「写文件要留身份标识」那一段（只追加，不覆盖）。详见 workshelf agents help。
  workshelf recent [--days 3] [--limit 100] [--json]
      只读：最近改过的文件，谁写的（自报/入库登记/侧边记录/推断/来源不明）、放对没有。
  workshelf search <关键词...> [--json]
      只读：按文件名、路径、智能体、工种、项目搜。
  workshelf home <文件...> --项目 <名> [--到 散落|任务|成品|资料|代码] [--任务 名] [--dry] [--json]
      一键归位：搬迁文件（可 --undo 撤销）。--整项目 把文件夹整棵搬进总文件夹。
  workshelf home --undo <撤销清单.json>
      按清单撤销：将本次搬迁的文件搬回原位。
  workshelf trash <文件或文件夹...> [--dry] [--allow-outside] [--json]
      删除：优先挪进系统废纸篓；不行就进总控「删除暂存」。不粉碎。
  workshelf trash --undo <撤销清单.json>
      把删掉的文件从废纸篓/暂存搬回原处。
  workshelf merge --suggest [--json]
      列出「可能是同一项目」的合并建议（只读）。
  workshelf merge <路径...> --到 <项目名> [--dry] [--json]
      合并进总文件夹下的目标项目，搬迁可撤销。
  workshelf config [--init]
      显示当前配置；--init 把默认配置写到配置文件（已存在就不写）。
  workshelf version

所有子命令都认 --config <文件>（默认 $WORKSHELF_CONFIG 或 ~/.config/workshelf/config.json）。
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, Version)
		return 2
	}
	var err error
	switch args[0] {
	case "scan":
		err = cmdScan(args[1:], stdout, stderr)
	case "plan":
		err = cmdPlan(args[1:], stdout, stderr)
	case "put":
		err = cmdPut(args[1:], stdout, stderr)
	case "config":
		err = cmdConfig(args[1:], stdout, stderr)
	case "recent":
		err = cmdRecent(args[1:], stdout, stderr)
	case "search":
		err = cmdSearch(args[1:], stdout, stderr)
	case "mark":
		err = cmdMark(args[1:], stdout, stderr)
	case "auto":
		err = cmdAuto(args[1:], stdout, stderr)
	case "approve":
		err = cmdApprove(args[1:], stdout, stderr)
	case "worktypes":
		err = cmdWorkTypes(args[1:], stdout, stderr)
	case "allocate", "分配":
		err = cmdAllocate(args[1:], stdout, stderr)
	case "agents":
		err = cmdAgents(args[1:], stdout, stderr)
	case "thumb":
		err = cmdThumb(args[1:], stdout)
	case "home", "归位", "place":
		err = cmdHome(args[1:], stdout, stderr)
	case "trash", "删除":
		err = cmdTrash(args[1:], stdout, stderr)
	case "graph", "关系网":
		err = cmdGraph(args[1:], stdout, stderr)
	case "merge", "合并":
		err = cmdMerge(args[1:], stdout, stderr)
	case "register", "登记":
		err = cmdRegister(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "workshelf", Version)
	case "help", "--help", "-h":
		fmt.Fprintf(stdout, usage, Version)
	default:
		fmt.Fprintf(stderr, "不认识的命令：%s\n\n", args[0])
		fmt.Fprintf(stderr, usage, Version)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "workshelf:", err)
		return 1
	}
	return 0
}

// parseInterspersed 允许参数和 --选项 交错出现（例如 put 项目 任务 文件 --new-project）。
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// flag 包遇到 "--" 会停下并吃掉它：之后全部当位置参数。
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func loadConfig(path string) (config.Config, string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Config{}, "", false, err
	}
	if path == "" {
		path = config.DefaultPath(home)
	}
	cfg, found, err := config.Load(path, home)
	return cfg, path, found, err
}

type scanFlags struct {
	volumes  bool
	noStatus bool
}

func buildScanOptions(cfg config.Config, paths []string, sf scanFlags) (scan.Options, error) {
	home, _ := os.UserHomeDir()
	return registry.ScanOptions(cfg, home, paths, sf.volumes, cfg.GitStatus && !sf.noStatus)
}

func cmdScan(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	var sf scanFlags
	fs.BoolVar(&sf.volumes, "volumes", false, "也扫 /Volumes 下的外置盘")
	fs.BoolVar(&sf.noStatus, "no-git-status", false, "不跑 git status")
	asJSON := fs.Bool("json", false, "输出 JSON")
	out := fs.String("out", "", "把扫描结果 JSON 写到这个文件（已存在就加 -02，不覆盖）")
	paths, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	opt, err := buildScanOptions(cfg, paths, sf)
	if err != nil {
		return err
	}
	res, err := scan.Scan(opt)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *out != "" {
		p, err := writeNewNumbered(*out, data)
		if err != nil {
			return err
		}
		fmt.Fprintln(stderr, "扫描结果已写到：", p)
	}
	if *asJSON {
		_, err = stdout.Write(data)
		return err
	}
	printScan(stdout, res, cfg)
	return nil
}

func printScan(w io.Writer, res *scan.Result, cfg config.Config) {
	loc := cfg.Location()
	fmt.Fprintf(w, "扫描完成（只读，什么都没改）：%s\n", res.GeneratedAt.In(loc).Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "看了 %d 个文件夹；认出 %d 个项目文件夹，归成 %d 组；散落文件 %d 个；", res.DirsVisited, len(res.Projects), len(res.Groups), len(res.Loose))
	fmt.Fprintf(w, "跳过软链接 %d 个、疑似密钥文件 %d 个（只计数）；读不了 %d 个。\n", res.SymlinksSkipped, res.SensitiveSkipped, res.Unreadable)
	if res.Partial {
		fmt.Fprintf(w, "⚠️ 没扫完：%s\n", strings.Join(res.PartialReasons, "、"))
	}
	byPath := map[string]scan.Project{}
	for _, p := range res.Projects {
		byPath[p.Path] = p
	}
	looseBy := map[int]int{}
	for _, l := range res.Loose {
		looseBy[l.Group]++
	}
	for _, g := range res.Groups {
		fmt.Fprintf(w, "\n[%d] %s（%d 个版本", g.ID, g.Name, len(g.Members))
		if c := looseBy[g.ID]; c > 0 {
			fmt.Fprintf(w, "，同名散落文件 %d 个", c)
		}
		fmt.Fprintln(w, "）")
		for _, m := range g.Members {
			p := byPath[m]
			var tags []string
			tags = append(tags, kindZh(p.Kind))
			if gi := p.Git; gi != nil {
				if gi.Branch != "" {
					tags = append(tags, "分支 "+gi.Branch)
				}
				switch {
				case gi.StatusChecked && gi.Dirty:
					tags = append(tags, fmt.Sprintf("有未提交改动 %d 项", gi.DirtyCount))
				case gi.StatusChecked:
					tags = append(tags, "干净")
				case gi.StatusError != "":
					tags = append(tags, "git 状态未知")
				}
				if len(gi.Worktrees) > 0 {
					tags = append(tags, fmt.Sprintf("带 %d 个 worktree", len(gi.Worktrees)))
				}
			}
			fmt.Fprintf(w, "    %s  [%s]\n", m, strings.Join(tags, "，"))
		}
		if len(g.Reasons) > 0 {
			fmt.Fprintf(w, "    归组理由：%s\n", strings.Join(g.Reasons, "；"))
		}
	}
	if c := looseBy[-1]; c > 0 {
		fmt.Fprintf(w, "\n另有 %d 个散落文件认不出属于哪个项目（plan 会列进待分类清单）。\n", c)
	}
}

func kindZh(k string) string {
	switch k {
	case "main":
		return "主版本"
	case "worktree":
		return "worktree"
	case "clone":
		return "另一个 clone"
	case "copy":
		return "手工副本"
	}
	return "单独"
}

func cmdPlan(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	var sf scanFlags
	fs.BoolVar(&sf.volumes, "volumes", false, "也扫 /Volumes 下的外置盘")
	fs.BoolVar(&sf.noStatus, "no-git-status", false, "不跑 git status")
	from := fs.String("from", "", "用已有的扫描结果 JSON，不重新扫")
	out := fs.String("out", "", "计划写到哪个目录（默认 <总文件夹>/_总控/整理日志）")
	paths, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	var res *scan.Result
	if *from != "" {
		if len(paths) > 0 {
			return errors.New("--from 和扫描路径只能二选一")
		}
		b, err := os.ReadFile(*from)
		if err != nil {
			return err
		}
		res = &scan.Result{}
		if err := json.Unmarshal(b, res); err != nil {
			return fmt.Errorf("扫描结果读不了：%w", err)
		}
	} else {
		opt, err := buildScanOptions(cfg, paths, sf)
		if err != nil {
			return err
		}
		if res, err = scan.Scan(opt); err != nil {
			return err
		}
	}
	now := time.Now()
	p := plan.Build(res, plan.Options{Config: cfg, Now: now})
	dir := *out
	if dir == "" {
		dir = filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.Logs)
	}
	jp, mp, sha, err := p.WriteFiles(dir, now, cfg.Location())
	if err != nil {
		return err
	}
	c := p.Counts
	fmt.Fprintf(stdout, "整理计划已生成（只是计划，什么都没挪）。\n")
	fmt.Fprintf(stdout, "项目 %d 个；可移动 %d 条；只登记不动 %d 条；待分类 %d 条；有未提交改动 %d 个；worktree %d 个；跨卷 %d 条。\n",
		c.Projects, c.Move, c.Keep, c.Pending, c.Dirty, c.Worktrees, c.CrossVolume)
	fmt.Fprintf(stdout, "Markdown：%s\nJSON：%s\nsha12：%s\n", mp, jp, sha)
	return nil
}

func cmdPut(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	newProject := fs.Bool("new-project", false, "项目不存在时新建")
	appendTo := fs.Bool("append", false, "当天同名任务目录已存在时往里追加（默认新建 -02）")
	agent := fs.String("agent", "", "负责智能体，也是索引分片名（默认 $WORKSHELF_AGENT 或 workshelf）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	var wt, tpl, to, sub, period, note, ver string
	for _, f := range []struct {
		p           *string
		zh, en, use string
	}{
		{&wt, "工种", "worktype", "工种：行业·工种（必填，workshelf worktypes 可查）"},
		{&tpl, "模板", "template", "模板 T01…T15（工种表里有就可不填）"},
		{&to, "到", "to", "放到哪：任务（默认）/ 成品 / 资料 / 敏感"},
		{&sub, "放", "sub", "目标下再放进哪个子文件夹"},
		{&period, "期间", "period", "期间，如 2026-10"},
		{&note, "说明", "note", "一句话说明"},
		{&ver, "版本", "ver", "版本标识，如 v2、终稿"},
	} {
		fs.StringVar(f.p, f.zh, "", f.use)
		fs.StringVar(f.p, f.en, "", f.use+"（同 --"+f.zh+"）")
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 3 {
		return errors.New("用法：workshelf put <项目> <任务> <文件...> --工种 <行业·工种> [--到 成品]")
	}
	if strings.TrimSpace(wt) == "" {
		return errors.New("要给 --工种（如 --工种 电商·主图；不知道就 workshelf worktypes 查，实在没有就 --工种 通用/其他·通用）")
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	res, err := put.Put(put.Options{
		Config: cfg, Project: pos[0], Task: pos[1], Files: pos[2:],
		Append: *appendTo, NewProject: *newProject, Agent: *agent,
		WorkType: wt, Template: tpl, To: to, Sub: sub, Period: period, Note: note, Version: ver,
	})
	if res != nil {
		for _, w := range res.Warnings {
			fmt.Fprintln(stderr, w)
		}
	}
	if err != nil {
		if res != nil && len(res.Files) > 0 {
			fmt.Fprintln(stderr, "已经复制好的文件（保留，没删）：")
			for _, f := range res.Files {
				fmt.Fprintln(stderr, "  ", f.Path)
			}
		}
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		_, err := fmt.Fprintln(stdout, string(b))
		return err
	}
	if res.CreatedProject {
		fmt.Fprintln(stderr, "新建了项目：", res.ProjectDir)
	}
	for _, f := range res.Files {
		fmt.Fprintln(stdout, f.Path)
	}
	fmt.Fprintf(stderr, "已复制 %d 个文件并核对 sha256；源文件原样保留。工种：%s（%s）；状态：%s。索引：%s（第 %d 行起）\n",
		len(res.Files), res.WorkType, res.Template, res.Files[0].Status, res.IndexPath, res.Files[0].Row)
	if res.Files[0].Status == put.StatusPending {
		fmt.Fprintf(stderr, "成品等你批准：workshelf approve %s %s\n", filepath.Base(res.ProjectDir), filepath.Base(res.DestDir))
	}
	return nil
}

func cmdConfig(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	initCfg := fs.Bool("init", false, "把默认配置写到配置文件（已存在就不写）")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	cfg, path, found, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	b = append(b, '\n')
	if *initCfg {
		if found {
			return fmt.Errorf("配置文件已存在，不覆盖：%s", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(b); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "已写入默认配置：", path)
		return nil
	}
	state := "没有，使用默认值"
	if found {
		state = "已读取"
	}
	fmt.Fprintf(stderr, "配置文件：%s（%s）\n", path, state)
	_, err = stdout.Write(b)
	return err
}

// writeNewNumbered 新建文件写入；已存在就在扩展名前加 -02、-03……，绝不覆盖。
func writeNewNumbered(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
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
		return p, f.Close()
	}
	return "", errors.New("同名文件太多")
}
