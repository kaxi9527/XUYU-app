package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	np := fs.Bool("new-project", false, "")
	ag := fs.String("agent", "", "")
	pos, err := parseInterspersed(fs, []string{"项目", "--agent", "a1", "任务", "f1", "--new-project", "--", "-f2"})
	if err != nil {
		t.Fatal(err)
	}
	if !*np || *ag != "a1" || !reflect.DeepEqual(pos, []string{"项目", "任务", "f1", "-f2"}) {
		t.Fatalf("pos=%v np=%v ag=%q", pos, *np, *ag)
	}
}

// 端到端：在临时主目录里跑 scan → plan → put。
func TestEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgFile := filepath.Join(home, ".config", "workshelf", "config.json")
	t.Setenv("WORKSHELF_CONFIG", cfgFile)
	proj := filepath.Join(home, "Projects", "demo")
	must(t, os.MkdirAll(proj, 0o755))
	must(t, os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module demo\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(home, "Downloads"), 0o755))
	note := filepath.Join(home, "Downloads", "demo-v2.zip")
	must(t, os.WriteFile(note, []byte("zip"), 0o644))

	var out, errb bytes.Buffer
	if code := run([]string{"config", "--init"}, &out, &errb); code != 0 {
		t.Fatalf("config init: %s", errb.String())
	}
	if code := run([]string{"config", "--init"}, &out, &errb); code == 0 {
		t.Fatal("config --init must not overwrite")
	}

	out.Reset()
	if code := run([]string{"scan", "--json", "--no-git-status"}, &out, &errb); code != 0 {
		t.Fatalf("scan: %s", errb.String())
	}
	var res struct {
		Projects []struct{ Path string }
		Loose    []struct {
			Path  string
			Group int
		}
	}
	must(t, json.Unmarshal(out.Bytes(), &res))
	if len(res.Projects) != 1 || res.Projects[0].Path != proj || len(res.Loose) != 1 || res.Loose[0].Group != 0 {
		t.Fatalf("scan: %s", out.String())
	}

	out.Reset()
	if code := run([]string{"plan", "--no-git-status"}, &out, &errb); code != 0 {
		t.Fatalf("plan: %s", errb.String())
	}
	if !strings.Contains(out.String(), "sha12") {
		t.Fatal(out.String())
	}
	logs, _ := os.ReadDir(filepath.Join(home, "Work", "_总控", "整理日志"))
	if len(logs) != 2 {
		t.Fatalf("plan files: %v", logs)
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"put", "demo", "收包", note, "--new-project", "--工种", "通用"}, &out, &errb); code != 0 {
		t.Fatalf("put: %s", errb.String())
	}
	stored := strings.TrimSpace(out.String())
	if !strings.HasPrefix(stored, filepath.Join(home, "Work", "demo", "任务")) || !strings.HasSuffix(stored, "收包/demo-v2.zip") {
		t.Fatalf("stored: %q", stored)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatal("source must stay")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
