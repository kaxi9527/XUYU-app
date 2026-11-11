package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/graph"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

const graphUsage = `workshelf graph projects [--json]
workshelf graph files [<项目>] [--json] [--expand-attach] [--project-id <ID>]

只读关系网：项目关系 / 文件关系。不合并、不搬家。
`

func cmdGraph(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(strings.TrimSpace(graphUsage))
	}
	switch args[0] {
	case "projects", "项目":
		return cmdGraphProjects(args[1:], stdout, stderr)
	case "files", "文件":
		return cmdGraphFiles(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, graphUsage)
		return nil
	default:
		return fmt.Errorf("未知子命令 %q\n%s", args[0], graphUsage)
	}
}

func cmdGraphProjects(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("graph-projects", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
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
	reg, _ := registry.Load(cfg)
	g := graph.Projects(shelf.Env{Config: cfg, Home: homeDir, Reg: reg})
	if *asJSON {
		b, _ := json.MarshalIndent(g, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	fmt.Fprintf(stdout, "项目关系网：%d 个节点，%d 条边\n", len(g.Nodes), len(g.Edges))
	if g.Skipped != "" {
		fmt.Fprintln(stdout, "备注：", g.Skipped)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(stdout, "  [%s] %s — %s\n    %s（%s）\n", e.Type, shortGraphNode(g.Nodes, e.From), shortGraphNode(g.Nodes, e.To), e.Reason, e.Confidence)
	}
	return nil
}

func cmdGraphFiles(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("graph-files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	asJSON := fs.Bool("json", false, "")
	expand := fs.Bool("expand-attach", false, "把桌面/下载等项目外挂靠文件也画进图")
	projectID := fs.String("project-id", "", "原地项目 ID（优先于同名）")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	project := ""
	if len(pos) > 0 {
		project = strings.TrimSpace(pos[0])
	}
	cfg, _, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	reg, _ := registry.Load(cfg)
	g := graph.Files(shelf.Env{Config: cfg, Home: homeDir, Reg: reg}, graph.FileOpts{
		Project: project, ProjectID: *projectID, ExpandAttach: *expand,
	})
	if *asJSON {
		b, _ := json.MarshalIndent(g, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	fmt.Fprintf(stdout, "文件关系网（项目 %s）：%d 个节点，%d 条边\n", g.Project, len(g.Nodes), len(g.Edges))
	if g.Note != "" {
		fmt.Fprintln(stdout, "切入：", g.Note)
	}
	if g.Skipped != "" {
		fmt.Fprintln(stdout, "备注：", g.Skipped)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(stdout, "  [%s] %s — %s\n    %s（%s）\n", e.Type, shortGraphNode(g.Nodes, e.From), shortGraphNode(g.Nodes, e.To), e.Reason, e.Confidence)
	}
	return nil
}

func shortGraphNode(ns []graph.Node, id string) string {
	for _, n := range ns {
		if n.ID == id {
			return n.Name
		}
	}
	return id
}
