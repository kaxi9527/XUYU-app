package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

func cmdHome(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("home", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	dry := fs.Bool("dry", false, "只预览，不搬不复制")
	asJSON := fs.Bool("json", false, "输出 JSON")
	project := fs.String("项目", "", "目标项目名")
	fs.StringVar(project, "project", "", "同 --项目")
	area := fs.String("到", "散落", "放到哪：任务/成品/资料/代码/散落（默认散落→资料/散落文件）")
	fs.StringVar(area, "to", "散落", "同 --到")
	task := fs.String("任务", "", "任务/成品下的子目录名（可空）")
	fs.StringVar(task, "task", "", "同 --任务")
	whole := fs.Bool("整项目", false, "把源文件夹整个当作项目搬进总文件夹")
	fs.BoolVar(whole, "whole", false, "同 --整项目")
	undoPath := fs.String("undo", "", "按撤销清单把文件搬回原位")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *undoPath != "" {
		ur, err := home.Undo(cfg, *undoPath)
		if err != nil {
			return err
		}
		if *asJSON {
			b, _ := json.MarshalIndent(ur, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		fmt.Fprintf(stdout, "撤销完成：搬回 %d；副本进整理撤销 %d；跳过 %d；失败 %d\n",
			ur.Restored, ur.Quarantined, ur.Skipped, ur.Failed)
		for _, n := range ur.Notes {
			fmt.Fprintln(stderr, " ·", n)
		}
		return nil
	}
	if len(pos) == 0 {
		return errors.New("用法：workshelf home <文件或文件夹...> --项目 <名> [--到 散落] [--dry]\n       workshelf home <项目文件夹> --整项目 --项目 <名> [--dry]\n       workshelf home --undo <撤销清单.json>")
	}
	if strings.TrimSpace(*project) == "" {
		return errors.New("要给 --项目（总文件夹里的项目名）")
	}
	if err := put.CheckName("项目名", *project); err != nil {
		return err
	}
	var jobs []home.Job
	for _, p := range pos {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		jobs = append(jobs, home.Job{Source: abs, Project: *project, Area: *area, Task: *task, Whole: *whole})
	}
	opt := home.Options{Config: cfg, Jobs: jobs, DryRun: *dry}
	var sum *home.Summary
	if *dry {
		sum, err = home.Preview(opt)
	} else {
		sum, err = home.Apply(opt)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(sum, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	if *dry {
		fmt.Fprintln(stdout, "预览（什么都没改）：")
		for _, it := range sum.Preview {
			if it.Skip {
				fmt.Fprintf(stdout, "  跳过  %s  （%s）\n", it.Source, it.Reason)
			} else {
				fmt.Fprintf(stdout, "  %s  %s\n       → %s%s\n", it.Action, it.Source, it.Target, ternary(it.CrossDevice, "  [跨磁盘搬迁]", ""))
			}
		}
		if sum.CrossHint != "" {
			fmt.Fprintln(stderr, sum.CrossHint)
		}
		return nil
	}
	fmt.Fprintf(stdout, "归位完成：搬走 %d；复制 %d；跳过 %d；失败 %d\n", sum.Moved, sum.Copied, sum.Skipped, sum.Failed)
	for _, it := range sum.Items {
		if it.Error != "" && it.Action != "跳过" {
			fmt.Fprintf(stderr, "  ! %s：%s\n", it.Source, it.Error)
		} else if it.Action == "跳过" {
			fmt.Fprintf(stderr, "  跳过 %s（%s）\n", it.Source, it.Error)
		} else {
			extra := ""
			if it.KeptSource {
				extra = "（原件仍在）"
			}
			fmt.Fprintf(stdout, "  %s %s → %s%s\n", it.Action, it.Source, it.Target, extra)
		}
	}
	if sum.LogPath != "" {
		fmt.Fprintln(stderr, "日志：", sum.LogPath)
	}
	if sum.UndoPath != "" {
		fmt.Fprintln(stderr, "撤销清单：", sum.UndoPath, "（workshelf home --undo 该文件）")
	}
	if sum.CrossHint != "" {
		fmt.Fprintln(stderr, sum.CrossHint)
	}
	return nil
}

func ternary(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
