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

	"github.com/kaxi9527/WorkShelf/internal/merge"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func cmdMerge(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	dry := fs.Bool("dry", false, "只预览")
	asJSON := fs.Bool("json", false, "输出 JSON")
	suggest := fs.Bool("suggest", false, "只列出合并建议，不执行")
	target := fs.String("到", "", "目标项目名（总文件夹下）")
	fs.StringVar(target, "to", "", "同 --到")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if *suggest || (len(pos) == 0 && strings.TrimSpace(*target) == "") {
		reg, _ := registry.Load(cfg)
		cands := merge.Suggest(shelf.Env{Config: cfg, Home: homeDir, Reg: reg})
		if *asJSON {
			b, _ := json.MarshalIndent(cands, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		if len(cands) == 0 {
			fmt.Fprintln(stdout, "暂时没有合并建议。")
			return nil
		}
		fmt.Fprintf(stdout, "合并建议 %d 组（只建议，没动手）：\n", len(cands))
		for i, c := range cands {
			fmt.Fprintf(stdout, "[%d] %s（%d 个）—— %s\n", i, c.Name, c.Members, strings.Join(c.Reasons, "；"))
			for _, p := range c.Paths {
				fmt.Fprintf(stdout, "    %s\n", p)
			}
		}
		return nil
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("用法：workshelf merge --suggest\n       workshelf merge <路径...> --到 <项目名> [--dry]")
	}
	var paths []string
	for _, p := range pos {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		paths = append(paths, abs)
	}
	opt := merge.Options{Config: cfg, Target: *target, Sources: paths, DryRun: *dry}
	if *dry {
		s, err := merge.Preview(opt)
		if err != nil {
			return err
		}
		if *asJSON {
			b, _ := json.MarshalIndent(s, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		fmt.Fprintln(stdout, "预览合并（什么都没挪）：")
		for _, it := range s.Preview {
			if it.Skip {
				fmt.Fprintf(stdout, "  跳过 %s（%s）\n", it.Source, it.Reason)
			} else {
				fmt.Fprintf(stdout, "  %s %s → %s\n", it.Action, it.Source, it.Target)
			}
		}
		return nil
	}
	s, err := merge.Apply(opt)
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	fmt.Fprintf(stdout, "合并完成：搬走 %d；复制 %d；跳过 %d；失败 %d\n", s.Moved, s.Copied, s.Skipped, s.Failed)
	if s.UndoPath != "" {
		fmt.Fprintln(stderr, "撤销清单：", s.UndoPath)
	}
	return nil
}
