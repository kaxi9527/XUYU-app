package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

const allocateUsage = `workshelf allocate <项目> [--apply] [--json]
    按工种分配逻辑整理项目内散落/未归类文件。默认只预览；加 --apply 才搬。
    整理会搬迁文件，完成后可撤销。

workshelf worktypes infer <项目> [--json] [--confirm]
    认出项目所属工种（说明已标 + 推断）。加 --confirm 才把「较有把握/大致判断」写入说明。
`

func cmdAllocate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("allocate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	apply := fs.Bool("apply", false, "真正执行（默认只预览）")
	dryFlag := fs.Bool("dry", false, "只预览（默认；与不加 --apply 相同）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("用法：workshelf allocate <项目> [--apply] [--json]")
	}
	dry := !*apply || *dryFlag
	project := strings.TrimSpace(pos[0])
	if err := put.CheckName("项目名", project); err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	ss, err := allocate.SuggestProject(allocate.SuggestOptions{Config: cfg, Project: project})
	if err != nil {
		return err
	}
	// 默认只执行 PreCheck 的；dry 时列出全部
	var picked []allocate.Suggestion
	for _, s := range ss {
		if s.Skip {
			continue
		}
		if dry || s.PreCheck {
			picked = append(picked, s)
		}
	}
	if *asJSON && dry {
		b, _ := json.MarshalIndent(ss, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	if dry {
		fmt.Fprintln(stdout, "按工种整理预览（什么都没改）：")
		for _, s := range ss {
			if s.Skip {
				fmt.Fprintf(stdout, "  跳过  %s  （%s）\n", s.Source, s.SkipReason)
				continue
			}
			mark := " "
			if s.PreCheck {
				mark = "✓"
			} else {
				mark = "?"
			}
			extra := s.Area
			if s.Task != "" {
				extra += "/" + s.Task
			}
			if s.WorkType != "" {
				extra += " · " + s.WorkType
			}
			fmt.Fprintf(stdout, "  %s %s\n       → %s（%s；%s）\n", mark, s.Source, extra, s.Confidence, s.Reason)
		}
		fmt.Fprintf(stderr, "共 %d 条；打勾 %d 条加 --apply 会执行。\n", len(ss), countPre(ss))
		return nil
	}
	jobs := allocate.ToJobs(picked)
	if len(jobs) == 0 {
		return errors.New("没有可执行的分配（都是待你定或已跳过）；先 --dry 看预览，或在桌面勾选后执行")
	}
	sum, err := home.Apply(home.Options{Config: cfg, Jobs: jobs})
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(sum, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	fmt.Fprintf(stdout, "按工种整理完成：搬走 %d；复制 %d；跳过 %d；失败 %d\n", sum.Moved, sum.Copied, sum.Skipped, sum.Failed)
	if sum.UndoPath != "" {
		fmt.Fprintln(stderr, "撤销清单：", sum.UndoPath)
	}
	return nil
}

func countPre(ss []allocate.Suggestion) int {
	n := 0
	for _, s := range ss {
		if !s.Skip && s.PreCheck {
			n++
		}
	}
	return n
}

func cmdWorkTypesInfer(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("worktypes-infer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	asJSON := fs.Bool("json", false, "输出 JSON")
	confirm := fs.Bool("confirm", false, "把非「待你定」的推断追加进说明（只追加）")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("用法：workshelf worktypes infer <项目> [--json] [--confirm]")
	}
	project := strings.TrimSpace(pos[0])
	if err := put.CheckName("项目名", project); err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	dir := filepath.Join(cfg.Root, project)
	list := allocate.InferProject(allocate.InferOptions{ProjectDir: dir, ReadmeName: cfg.Names.Readme})
	if *confirm {
		readme := filepath.Join(dir, cfg.Names.Readme)
		for _, in := range list {
			if in.AlreadyInReadme || in.Confidence == allocate.ConfLow || !in.PreConfirm {
				continue
			}
			if err := put.EnsureProjectWorkType(readme, in.Label); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "已写入说明：%s\n", in.Label)
		}
		list = allocate.InferProject(allocate.InferOptions{ProjectDir: dir, ReadmeName: cfg.Names.Readme})
	}
	if *asJSON {
		b, _ := json.MarshalIndent(list, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	for _, in := range list {
		mark := in.Source
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", in.Label, mark, in.Confidence, in.Reason)
	}
	return nil
}
