package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
)

// An agent can explicitly oppose a specific automatic operation. File recovery
// happens immediately; the app later incorporates the request in project memory.
func cmdAuto(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "reject") {
		return errors.New("用法：workshelf auto list [--project 项目] [--json]；或 workshelf auto reject <整理记录 ID> --agent <工作者> [--json]")
	}
	if args[0] == "list" {
		fs := flag.NewFlagSet("auto list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		project := fs.String("project", "", "只查看指定项目")
		cfgPath := fs.String("config", "", "配置文件")
		asJSON := fs.Bool("json", false, "输出 JSON")
		pos, err := parseInterspersed(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 0 {
			return errors.New("用法：workshelf auto list [--project 项目] [--json]")
		}
		cfg, _, _, err := loadConfig(*cfgPath)
		if err != nil {
			return err
		}
		operations, err := autoorganize.List(cfg)
		if err != nil {
			return err
		}
		selected := []autoorganize.Operation{}
		for _, o := range operations {
			if *project == "" || o.Project == *project {
				selected = append(selected, o)
			}
		}
		if *asJSON {
			return json.NewEncoder(stdout).Encode(selected)
		}
		if len(selected) == 0 {
			fmt.Fprintln(stdout, "没有自动整理记录。")
		}
		for _, o := range selected {
			fmt.Fprintf(stdout, "%s  %s  %s  %d 项\n", o.ID, o.Project, o.Status, len(o.Files))
		}
		return nil
	}
	fs := flag.NewFlagSet("auto reject", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agent := fs.String("agent", "", "提出反对的工作者")
	cfgPath := fs.String("config", "", "配置文件")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("请填写一次自动整理记录 ID")
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	req, err := autoorganize.RequestObjection(cfg, pos[0], *agent)
	if err != nil {
		return err
	}
	o, err := autoorganize.RestoreForObjection(cfg, pos[0])
	if err != nil {
		return fmt.Errorf("反对已记录，恢复未完成：%w", err)
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(struct {
			Request   autoorganize.Objection `json:"request"`
			Operation autoorganize.Operation `json:"operation"`
		}{req, o})
	}
	fmt.Fprintf(stdout, "反对已记录，本次整理状态：%s。项目记忆将在 WorkShelf 运行时同步。\n", o.Status)
	return nil
}
