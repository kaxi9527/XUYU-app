package put

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

type fakeScanner struct {
	warn string
	err  error
	got  []string
}

func (f *fakeScanner) Scan(p []string) (string, error) { f.got = p; return f.warn, f.err }

func setup(t *testing.T) (config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	src := filepath.Join(home, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, src
}

func file(t *testing.T, p, s string) string {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 洛杉矶 11 月 5 日晚上 = UTC 11 月 6 日，日期应取洛杉矶的。
var now = time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC)

func TestPutBasic(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "报告.md"), "hello")
	b := file(t, filepath.Join(src, "图.png"), "png")
	sc := &fakeScanner{}
	res, err := Put(Options{Config: cfg, Project: "示例项目", Task: "写报告", Files: []string{a, b}, NewProject: true, Agent: "test", Now: now, Scanner: sc})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(cfg.Root, "示例项目", "任务", "2026-11-05-写报告")
	if res.TaskDir != wantDir || !res.CreatedProject {
		t.Fatalf("task dir %s", res.TaskDir)
	}
	for _, d := range []string{"00-说明.md", "任务", "成品", "资料"} {
		if _, err := os.Stat(filepath.Join(cfg.Root, "示例项目", d)); err != nil {
			t.Fatalf("skeleton %s: %v", d, err)
		}
	}
	if len(res.Files) != 2 || res.Files[0].Path != filepath.Join(wantDir, "报告.md") || res.Files[0].Row != 1 || res.Files[1].Row != 2 {
		t.Fatalf("%+v", res.Files)
	}
	got, _ := os.ReadFile(res.Files[0].Path)
	if string(got) != "hello" {
		t.Fatal("content")
	}
	// 源文件还在、没变。
	if s, _ := os.ReadFile(a); string(s) != "hello" {
		t.Fatal("source changed")
	}
	raw, _ := os.ReadFile(res.IndexPath)
	if !bytes.HasPrefix(raw, bom) {
		t.Fatal("index must start with BOM")
	}
	if res.IndexPath != filepath.Join(cfg.Root, "_总控", "资产索引分片", "资产索引.test.csv") {
		t.Fatal(res.IndexPath)
	}
	rows, err := ReadRows(res.IndexPath)
	if err != nil || len(rows) != 3 || rows[0][0] != "日期" || len(rows[0]) != 18 || rows[1][1] != "示例项目" || rows[1][17] != res.Files[0].SHA256 || rows[1][8] != "示例项目/任务/2026-11-05-写报告/报告.md" {
		t.Fatalf("rows %v %v", rows, err)
	}
	if len(sc.got) != 2 {
		t.Fatal("scanner not called with files")
	}
}

func TestPutCollisions(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "a.txt"), "1")
	opt := Options{Config: cfg, Project: "p", Task: "t", Files: []string{a}, NewProject: true, Now: now, Scanner: &fakeScanner{}}
	r1, err := Put(opt)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Put(opt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r2.TaskDir, "2026-11-05-t-02") {
		t.Fatalf("second task dir: %s", r2.TaskDir)
	}
	opt.Append = true
	file(t, a, "2")
	r3, err := Put(opt)
	if err != nil {
		t.Fatal(err)
	}
	if r3.TaskDir != r1.TaskDir || filepath.Base(r3.Files[0].Path) != "a-02.txt" {
		t.Fatalf("append: %s %s", r3.TaskDir, r3.Files[0].Path)
	}
	// 原来的 a.txt 没被覆盖。
	if s, _ := os.ReadFile(r1.Files[0].Path); string(s) != "1" {
		t.Fatal("overwritten!")
	}
	if r3.Files[0].Row != 3 {
		t.Fatalf("row %d", r3.Files[0].Row)
	}
}

func TestPutRefusals(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "a.txt"), "1")
	env := file(t, filepath.Join(src, "service.env"), "K=1")
	cases := []struct {
		name string
		opt  Options
		want string
	}{
		{"no project", Options{Project: "nope", Task: "t", Files: []string{a}}, "--new-project"},
		{"sensitive", Options{Project: "p", Task: "t", Files: []string{env}, NewProject: true}, "密钥"},
		{"bad name", Options{Project: "../x", Task: "t", Files: []string{a}, NewProject: true}, "不能"},
		{"control", Options{Project: "_总控", Task: "t", Files: []string{a}, NewProject: true}, "总控"},
		{"dir", Options{Project: "p", Task: "t", Files: []string{src}, NewProject: true}, "普通文件"},
		{"unknown worktype", Options{Project: "p", Task: "t", Files: []string{a}, NewProject: true, WorkType: "不存在的工种"}, "工种表里没有"},
		{"bad to", Options{Project: "p", Task: "t", Files: []string{a}, NewProject: true, To: "桌面"}, "--到"},
		{"bad sub", Options{Project: "p", Task: "t", Files: []string{a}, NewProject: true, Sub: "a/../b"}, "不能"},
		{"bad template", Options{Project: "p", Task: "t", Files: []string{a}, NewProject: true, Template: "T99"}, "模板"},
		{"secret", Options{Project: "p", Task: "t", Files: []string{a}, NewProject: true, Scanner: &fakeScanner{err: ErrSecretFound}}, "拒绝"},
	}
	for _, c := range cases {
		c.opt.Config = cfg
		c.opt.Now = now
		if c.opt.Scanner == nil {
			c.opt.Scanner = &fakeScanner{}
		}
		_, err := Put(c.opt)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err=%v", c.name, err)
		}
	}
	// 被拒绝时什么都没建。
	if _, err := os.Stat(cfg.Root); !os.IsNotExist(err) {
		t.Fatal("refused put must not create anything")
	}
}

func fakeGitleaks(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	p := filepath.Join(t.TempDir(), "gitleaks")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGitleaks(t *testing.T) {
	_, src := setup(t)
	a := file(t, filepath.Join(src, "a.txt"), "x")

	if warn, err := (Gitleaks{Bin: "yy-no-such-gitleaks"}).Scan([]string{a}); err != nil || !strings.Contains(warn, "没找到 gitleaks") {
		t.Fatalf("missing: %q %v", warn, err)
	}
	clean := fakeGitleaks(t, "exit 0")
	if warn, err := (Gitleaks{Bin: clean}).Scan([]string{a}); err != nil || warn != "" {
		t.Fatalf("clean: %q %v", warn, err)
	}
	leak := fakeGitleaks(t, "exit 99")
	if _, err := (Gitleaks{Bin: leak}).Scan([]string{a}); !errors.Is(err, ErrSecretFound) {
		t.Fatalf("leak: %v", err)
	}
	broken := fakeGitleaks(t, "echo boom; exit 2")
	if _, err := (Gitleaks{Bin: broken}).Scan([]string{a}); err == nil || errors.Is(err, ErrSecretFound) {
		t.Fatalf("broken must fail closed: %v", err)
	}
	// 老版本：dir 不认识（退出 1），detect 能用。
	old := fakeGitleaks(t, `if [ "$1" = dir ]; then exit 1; fi; exit 99`)
	if _, err := (Gitleaks{Bin: old}).Scan([]string{a}); !errors.Is(err, ErrSecretFound) {
		t.Fatalf("old: %v", err)
	}
}

func TestAppendRowsConcurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "idx", "资产索引.x.csv")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := AppendRows(p, []Row{{Task: fmt.Sprint(i), Path: "逗号,和\"引号\""}}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	rows, err := ReadRows(p)
	if err != nil || len(rows) != 21 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	raw, _ := os.ReadFile(p)
	if bytes.Count(raw, bom) != 1 {
		t.Fatal("BOM must appear exactly once")
	}
	if rows[5][8] != "逗号,和\"引号\"" {
		t.Fatal("csv quoting")
	}
}

func TestPutV4(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "主图1.png"), "img")
	sc := &fakeScanner{}
	// 成品：进 成品/<任务目录名>/，状态待批准；任务目录里有 说明.md；项目说明补上工种。
	res, err := Put(Options{Config: cfg, Project: "店铺", Task: "双11主图", Files: []string{a}, NewProject: true,
		Agent: "yy-test", Now: now, Scanner: sc, WorkType: "主图", To: ToOutput, Sub: "首版", Note: "五张主图", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	wantDest := filepath.Join(cfg.Root, "店铺", "成品", "2026-11-05-双11主图", "首版")
	if res.DestDir != wantDest || res.WorkType != "电商·主图" || res.Template != "T01" {
		t.Fatalf("%+v", res)
	}
	note, _ := os.ReadFile(filepath.Join(res.TaskDir, "说明.md"))
	for _, w := range []string{"- 工种：电商·主图", "- 模板：T01", "- 状态：待批准", "- 负责智能体：yy-test", "- 一句话说明：五张主图"} {
		if !strings.Contains(string(note), w) {
			t.Fatalf("说明.md 缺 %s:\n%s", w, note)
		}
	}
	readme, _ := os.ReadFile(filepath.Join(cfg.Root, "店铺", "00-说明.md"))
	if strings.Count(string(readme), "- 工种：电商·主图") != 1 {
		t.Fatalf("readme: %s", readme)
	}
	rows, _ := ReadRows(res.IndexPath)
	r := rows[1]
	if r[2] != "电商·主图" || r[3] != "T01" || r[5] != "图片组" || r[6] != "v1" || r[7] != "待批准" || r[10] != "yy-test" ||
		r[9] != "店铺/任务/2026-11-05-双11主图/说明.md" {
		t.Fatalf("row %v", r)
	}
	// 再放一次同工种：项目说明不重复追加。
	if _, err := Put(Options{Config: cfg, Project: "店铺", Task: "双11主图", Files: []string{a}, Agent: "yy-test", Now: now, Scanner: sc, WorkType: "电商·主图"}); err != nil {
		t.Fatal(err)
	}
	readme, _ = os.ReadFile(filepath.Join(cfg.Root, "店铺", "00-说明.md"))
	if strings.Count(string(readme), "- 工种：电商·主图") != 1 {
		t.Fatalf("readme dup: %s", readme)
	}
	// 敏感：资料/敏感，目录 0700；🔒 工种拒绝带长数字的文件名。
	b := file(t, filepath.Join(src, "订单导出.csv"), "x")
	res2, err := Put(Options{Config: cfg, Project: "店铺", Task: "订单", Files: []string{b}, Now: now, Scanner: sc, WorkType: "电商·订单", To: ToSensitive})
	if err != nil {
		t.Fatal(err)
	}
	if res2.TaskDir != "" || res2.DestDir != filepath.Join(cfg.Root, "店铺", "资料", "敏感") {
		t.Fatalf("%+v", res2)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(res2.DestDir)
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	c := file(t, filepath.Join(src, "客户13800138000.csv"), "x")
	if _, err := Put(Options{Config: cfg, Project: "店铺", Task: "订单", Files: []string{c}, Now: now, Scanner: sc, WorkType: "电商·订单"}); err == nil || !strings.Contains(err.Error(), "长数字") {
		t.Fatalf("want refusal: %v", err)
	}
	// 新工种：表里没有，但给了模板。
	if res3, err := Put(Options{Config: cfg, Project: "店铺", Task: "x", Files: []string{a}, Now: now, Scanner: sc, WorkType: "宠物·美容", Template: "t03"}); err != nil || res3.Template != "T03" {
		t.Fatalf("custom: %v", err)
	}
}

func TestOldShardMovedAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "资产索引.a.csv")
	old := "\uFEFF" + strings.Join(OldHeader, ",") + "\n2026-11-01T00:00:00-07:00,p,t,a.txt,/x/a.txt,/s/a.txt,1,abc,a\n"
	file(t, p, old)
	if _, err := AppendRows(p, []Row{{Project: "p", Path: "p/b.txt"}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := ReadRows(p)
	if len(rows) != 2 || rows[0][0] != "日期" {
		t.Fatalf("%v", rows)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, LegacyDir))
	if len(ents) != 1 {
		t.Fatal("old shard not moved aside")
	}
	kept, _ := os.ReadFile(filepath.Join(dir, LegacyDir, ents[0].Name()))
	if string(kept) != old {
		t.Fatal("old shard content changed")
	}
	all := ReadIndexDir(dir, "/root")
	if len(all) != 2 || all[0]["_abs"] != "/x/a.txt" || all[1]["_abs"] != filepath.Join("/root", "p", "b.txt") {
		t.Fatalf("%v", all)
	}
}

func TestApprove(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "海报.png"), "img")
	res, err := Put(Options{Config: cfg, Project: "店铺", Task: "海报", Files: []string{a}, NewProject: true, Agent: "yy-a",
		Now: now, Scanner: &fakeScanner{}, WorkType: "电商·主图", To: ToOutput})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(res.Files[0].Path)
	ar, err := Approve(ApproveOptions{Config: cfg, Project: "店铺", Task: filepath.Base(res.TaskDir), By: "jerry", Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(res.Files[0].Path)
	if !bytes.Equal(before, after) || len(ar.Files) != 1 || len(ar.Changed) != 0 {
		t.Fatalf("%+v", ar)
	}
	rows, _ := ReadRows(ar.IndexPath)
	if len(rows) != 2 || rows[1][7] != "已交" || rows[1][2] != "电商·主图" || rows[1][10] != "yy-a" {
		t.Fatalf("%v", rows)
	}
	// 原来那行待批准还在（只追加不改）。
	orig, _ := ReadRows(res.IndexPath)
	if orig[1][7] != "待批准" {
		t.Fatal("original row changed")
	}
	note, _ := os.ReadFile(ar.NotePath)
	if !strings.Contains(string(note), "- 状态：待批准") || !strings.HasSuffix(strings.TrimSpace(string(note)), "批准人 jerry）") {
		t.Fatalf("%s", note)
	}
	if _, err := Approve(ApproveOptions{Config: cfg, Project: "店铺", Task: "没有这个"}); err == nil {
		t.Fatal("want error")
	}
}

func TestOutputJoinsSameDayTask(t *testing.T) {
	cfg, src := setup(t)
	a := file(t, filepath.Join(src, "草稿.txt"), "d")
	b := file(t, filepath.Join(src, "终稿.png"), "f")
	r1, err := Put(Options{Config: cfg, Project: "店铺", Task: "海报", Files: []string{a}, NewProject: true, Agent: "yy-a",
		Now: now, Scanner: &fakeScanner{}, WorkType: "电商·主图"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Put(Options{Config: cfg, Project: "店铺", Task: "海报", Files: []string{b}, Agent: "yy-a",
		Now: now, Scanner: &fakeScanner{}, WorkType: "电商·主图", To: ToOutput})
	if err != nil {
		t.Fatal(err)
	}
	if r1.TaskDir != r2.TaskDir || filepath.Base(r2.DestDir) != filepath.Base(r1.TaskDir) {
		t.Fatalf("task %s vs %s / %s", r1.TaskDir, r2.TaskDir, r2.DestDir)
	}
	note, _ := os.ReadFile(r2.NotePath)
	if !strings.Contains(string(note), "- 状态：进行中") || !strings.Contains(string(note), "- 状态：待批准（成品入库于") {
		t.Fatalf("note: %s", note)
	}
	r3, err := Put(Options{Config: cfg, Project: "店铺", Task: "海报", Files: []string{a}, Agent: "yy-a",
		Now: now, Scanner: &fakeScanner{}, WorkType: "电商·主图"})
	if err != nil || r3.TaskDir == r1.TaskDir {
		t.Fatalf("plain task put should still get -02: %v %s", err, r3.TaskDir)
	}
}
