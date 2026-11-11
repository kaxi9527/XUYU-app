package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/trash"
)

func cmdTrash(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("trash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	dry := fs.Bool("dry", false, "只预览，不挪")
	asJSON := fs.Bool("json", false, "输出 JSON")
	outside := fs.Bool("allow-outside", false, "允许删总文件夹外、但仍在主目录下的路径")
	undoPath := fs.String("undo", "", "按撤销清单把文件搬回原处")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
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
	if *undoPath != "" {
		ur, err := trash.Undo(cfg, *undoPath)
		if err != nil {
			return err
		}
		if *asJSON {
			b, _ := json.MarshalIndent(ur, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		fmt.Fprintf(stdout, "撤销完成：搬回 %d；跳过 %d；失败 %d\n", ur.Restored, ur.Skipped, ur.Failed)
		for _, n := range ur.Notes {
			fmt.Fprintln(stderr, " ·", n)
		}
		return nil
	}
	if len(pos) == 0 {
		return errors.New("用法：workshelf trash <文件或文件夹...> [--dry] [--allow-outside]\n       workshelf trash --undo <撤销清单.json>")
	}
	var paths []string
	for _, p := range pos {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		paths = append(paths, abs)
	}
	opt := trash.Options{Config: cfg, Home: home, Paths: paths, DryRun: *dry, AllowOutsideRoot: *outside}
	var sum *trash.Summary
	if *dry {
		sum, err = trash.Preview(opt)
	} else {
		sum, err = trash.Apply(opt)
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
		fmt.Fprintf(stdout, "预览删除（什么都没挪）：共 %d 个文件，%d 字节\n", sum.TotalFiles, sum.TotalBytes)
		for _, it := range sum.Preview {
			if it.Skip {
				fmt.Fprintf(stdout, "  跳过  %s（%s）\n", it.Path, it.Reason)
			} else {
				fmt.Fprintf(stdout, "  %s\n", it.Label)
			}
		}
		if sum.MethodHint != "" {
			fmt.Fprintln(stderr, sum.MethodHint)
		}
		return nil
	}
	fmt.Fprintf(stdout, "删除完成：进废纸篓 %d；进删除暂存 %d；跳过 %d；失败 %d\n",
		sum.Trashed, sum.Staged, sum.Skipped, sum.Failed)
	for _, it := range sum.Items {
		if it.Action == "失败" || it.Action == "跳过" {
			fmt.Fprintf(stderr, "  %s %s（%s）\n", it.Action, it.Path, it.Error)
		} else {
			fmt.Fprintf(stdout, "  %s %s → %s\n", it.Action, it.Path, it.Dest)
		}
	}
	if sum.LogPath != "" {
		fmt.Fprintln(stderr, "日志：", sum.LogPath)
	}
	if sum.UndoPath != "" {
		fmt.Fprintln(stderr, "撤销清单：", sum.UndoPath, "（workshelf trash --undo 该文件）")
	}
	return nil
}
