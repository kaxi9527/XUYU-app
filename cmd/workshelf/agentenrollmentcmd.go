package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/agents"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

func cmdAgentEnrollment(sub string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agents "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	homeFlag := fs.String("home", "", "主目录，默认当前用户")
	stateFlag := fs.String("state-dir", "", "WorkShelf 检查/接入回执目录")
	agent := fs.String("agent", "", "一次接入的工具：codex、claude 或 gemini")
	allowed := fs.String("agents", "", "允许自动接入的工具，用逗号分隔")
	ignored := fs.String("ignore", "", "暂停接入的工具，用逗号分隔；未给时保留现有选择")
	enable := fs.Bool("enable", false, "开启所选工具的持续自动接入")
	disable := fs.Bool("disable", false, "停止后续自动接入，保留已写规则")
	yes := fs.Bool("yes", false, "执行已展示的接入/政策操作；默认只预览")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return errors.New("接入命令不接受位置参数")
	}
	home := *homeFlag
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	state := *stateFlag
	if state == "" {
		state = filepath.Join(filepath.Dir(config.DefaultPath(home)), "agent-discovery")
	}
	store, err := agentdiscovery.NewStore(state)
	if err != nil {
		return err
	}
	ctx := context.Background()
	emit := func(value any) error {
		b, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(b))
		return err
	}
	if !*asJSON {
		fmt.Fprintln(stderr, "接入只写来源标记规则；已写入不代表当前会话已加载。")
	}
	split := func(value string) []string {
		var out []string
		for _, s := range strings.Split(value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if sub == "enroll" {
		if *agent == "" || *enable || *disable || *allowed != "" || *ignored != "" {
			return errors.New("用法：agents enroll --agent codex|claude|gemini [--yes]")
		}
		plan, err := agents.PrepareEnrollment(ctx, home, *agent)
		if err != nil {
			return err
		}
		if !*yes {
			return emit(plan.Preview())
		}
		if plan.Preview().Status == "blocked" {
			return errors.New("接入尚不能执行：" + plan.Preview().Reason)
		}
		r, err := store.Check(ctx, agentdiscovery.Scanner{Home: home}, time.Now(), false)
		if err != nil {
			return err
		}
		result, err := store.Enroll(ctx, r.Receipt.Revision, *agent, plan.Preview().Digest)
		if err != nil {
			return err
		}
		return emit(result)
	}
	if *enable && *disable || *agent != "" {
		return errors.New("policy 只能选择 --enable 或 --disable")
	}
	r, err := store.Read(ctx)
	if err != nil {
		return err
	}
	if r.Revision > 0 && r.Scan.Home != home {
		return errors.New("此检查记录属于另一个主目录")
	}
	if !*enable && !*disable {
		if *allowed != "" || *ignored != "" || *yes {
			return errors.New("修改政策需要 --enable 或 --disable")
		}
		return emit(struct {
			Revision uint64                           `json:"revision"`
			Policy   *agentdiscovery.EnrollmentPolicy `json:"policy"`
		}{r.Revision, r.Policy})
	}
	allowedList, ignoredList := split(*allowed), split(*ignored)
	ignoreSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ignore" {
			ignoreSet = true
		}
	})
	if r.Policy != nil {
		if *disable && *allowed == "" {
			for _, g := range r.Policy.Grants {
				allowedList = append(allowedList, g.Agent)
			}
		}
		if !ignoreSet {
			ignoredList = r.Policy.Ignored
		}
	}
	preview, err := agentdiscovery.PolicyPreview(*enable, allowedList, ignoredList)
	if err != nil {
		return err
	}
	if !*yes {
		return emit(struct {
			Home   string          `json:"home"`
			Policy json.RawMessage `json:"policy"`
		}{home, preview})
	}
	if r.Revision == 0 {
		result, err := store.Check(ctx, agentdiscovery.Scanner{Home: home}, time.Now(), false)
		if err != nil {
			return err
		}
		r = result.Receipt
	}
	r, err = store.SetEnrollmentPolicy(ctx, r.Revision, *enable, allowedList, ignoredList)
	if err != nil {
		return err
	}
	if *enable {
		r, err = store.ReconcileEnrollment(ctx)
		if err != nil {
			return err
		}
	}
	return emit(r)
}
