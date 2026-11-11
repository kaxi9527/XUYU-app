package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

func cmdAgentDiscovery(sub string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agents "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	homeFlag := fs.String("home", "", "检查哪个主目录；指定时不扫描系统应用目录")
	stateFlag := fs.String("state-dir", "", "本地检查回执目录（不保存会话或凭据）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return errors.New("检查命令不接受位置参数")
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
	state := *stateFlag
	if state == "" {
		state = filepath.Join(filepath.Dir(config.DefaultPath(home)), "agent-discovery")
	}
	store, err := agentdiscovery.NewStore(state)
	if err != nil {
		return err
	}
	scanner := agentdiscovery.Scanner{Home: home}
	if *homeFlag == "" && runtime.GOOS == "darwin" {
		scanner.ApplicationRoots = []string{"/Applications", filepath.Join(home, "Applications")}
	}
	emit := func(r agentdiscovery.Result) error {
		if *asJSON {
			return json.NewEncoder(stdout).Encode(r)
		}
		if !r.Ran {
			_, err := fmt.Fprintf(stdout, "未执行新检查；下次到期 %s。\n", r.Receipt.NextDue.Local().Format(time.RFC3339))
			return err
		}
		if _, err := fmt.Fprintf(stdout, "已检查：%d 个候选，%d 项状态变化，%d 个覆盖缺口。\n", len(r.Receipt.Scan.Candidates), len(r.Receipt.Changes), len(r.Receipt.Scan.Gaps)); err != nil {
			return err
		}
		for _, c := range r.Receipt.Records {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", c.ID, c.Name, c.Status); err != nil {
				return err
			}
		}
		for _, e := range r.Receipt.Enrollments {
			if _, err := fmt.Fprintf(stdout, "接入：%s\t%s\t%s\n", e.Intent.Agent, e.Status, e.Reason); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(stdout, "规则写入不代表 Agent 已加载或产物已标记。单次接入使用 agents enroll，持续接入使用 agents policy。")
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if sub == "watch" {
		err := store.Watch(ctx, scanner, emit)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	result, err := store.Check(ctx, scanner, time.Now(), sub == "inventory")
	if err != nil {
		return err
	}
	if sub == "daily" {
		if result.Ran {
			result.Receipt, err = store.ReconcileEnrollment(ctx)
		} else {
			result.Receipt, err = store.RecoverEnrollment(ctx)
		}
		if err != nil {
			return err
		}
	}
	return emit(result)
}
