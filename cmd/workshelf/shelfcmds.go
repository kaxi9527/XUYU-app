package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func shelfEnv(cfgPath string) (shelf.Env, error) {
	cfg, _, _, err := loadConfig(cfgPath)
	if err != nil {
		return shelf.Env{}, err
	}
	home, err := os.UserHomeDir()
	return shelf.Env{Config: cfg, Home: home}, err
}

func printItems(w io.Writer, items []shelf.Item, asJSON bool) error {
	if asJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		_, err := fmt.Fprintln(w, string(b))
		return err
	}
	for _, it := range items {
		agent := it.Agent
		if agent == "" {
			agent = "?"
		}
		extra := []string{it.Placement, agent + "(" + it.Confidence + ")"}
		if it.WorkType != "" {
			extra = append(extra, it.WorkType)
		}
		if it.Modified {
			extra = append(extra, "写后被改过")
		}
		fmt.Fprintf(w, "%s  %s  [%s]\n", shortTime(it.ModTime), it.Path, strings.Join(extra, " · "))
	}
	return nil
}

func cmdRecent(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("recent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	days := fs.Int("days", 3, "最近几天")
	limit := fs.Int("limit", 100, "最多列几条")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	e, err := shelfEnv(*cfgPath)
	if err != nil {
		return err
	}
	return printItems(stdout, shelf.Recent(e, *days, *limit), *asJSON)
}

func cmdSearch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "配置文件")
	limit := fs.Int("limit", 100, "最多列几条")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("用法：workshelf search <关键词...>")
	}
	e, err := shelfEnv(*cfgPath)
	if err != nil {
		return err
	}
	return printItems(stdout, shelf.Search(e, strings.Join(pos, " "), *limit), *asJSON)
}

func shortTime(rfc string) string {
	if len(rfc) >= 16 {
		return rfc[5:10] + " " + rfc[11:16]
	}
	return rfc
}
