package agents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w(t *testing.T, p, s string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScan(t *testing.T) {
	home := t.TempDir()
	w(t, filepath.Join(home, ".grokbot", "AGENTS.md"), "# grok\n")
	w(t, filepath.Join(home, ".openclaw", "workspace", "SOUL.md"), "soul")
	w(t, filepath.Join(home, ".claude", "CLAUDE.md"), "c")
	w(t, filepath.Join(home, "Work", "x", "代码", "repo", "AGENTS.md"), "repo")
	w(t, filepath.Join(home, "Work", "x", "代码", "repo", ".cursor", "rules", "a.mdc"), "rule")
	w(t, filepath.Join(home, "Work", "x", "node_modules", "AGENTS.md"), "skip")
	w(t, filepath.Join(home, "Library", "AGENTS.md"), "skip")
	w(t, filepath.Join(home, "Work", "x", "代码", "repo", ".git", "HEAD"), "ref")
	w(t, filepath.Join(home, "Work", "x", "代码", "repo", "src", "CLAUDE.md"), "sub")
	w(t, filepath.Join(home, ".agents", "skills", "s", "CLAUDE.md"), "skip")
	got := map[string]Candidate{}
	for _, c := range Scan(home) {
		rel, _ := filepath.Rel(home, c.Path)
		got[rel] = c
	}
	want := map[string]string{
		".grokbot/AGENTS.md": "grok", ".openclaw/workspace/SOUL.md": "openclaw", ".claude/CLAUDE.md": "claude",
		"Work/x/代码/repo/AGENTS.md": "", "Work/x/代码/repo/.cursor/rules": "cursor",
		"Work/x/代码/repo/src/CLAUDE.md": "claude",
	}
	for k, a := range want {
		c, ok := got[filepath.FromSlash(k)]
		if !ok || c.Agent != a {
			t.Fatalf("%s: %+v (all %v)", k, c, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("extra: %v", got)
	}
	for k, rec := range map[string]bool{".grokbot/AGENTS.md": true, ".openclaw/workspace/SOUL.md": true,
		"Work/x/代码/repo/AGENTS.md": true, "Work/x/代码/repo/.cursor/rules": true, "Work/x/代码/repo/src/CLAUDE.md": false} {
		if c := got[filepath.FromSlash(k)]; c.Recommended != rec {
			t.Fatalf("%s recommended=%v scope=%s", k, c.Recommended, c.Scope)
		}
	}
	if got[filepath.FromSlash(".grokbot/AGENTS.md")].Scope != "全局" || got[filepath.FromSlash("Work/x/代码/repo/AGENTS.md")].Scope != "项目" {
		t.Fatal("scope")
	}
}

func TestInstallUninstall(t *testing.T) {
	home := t.TempDir()
	backups := filepath.Join(home, "备份")
	origs := map[string]string{
		"yy-AGENTS.md":   "# 原来的规则\n- 第一条\n",
		"yy-SOUL.md":     "没有结尾换行的灵魂",
		"yy-空文件.md":      "",
		"yy-CLAUDE.md":   "中文\r\nCRLF 行\r\n",
		"yy-cursor-root": "",
	}
	var targets []Target
	for name, s := range origs {
		if name == "yy-cursor-root" {
			continue
		}
		targets = append(targets, Target{Path: w(t, filepath.Join(home, name), s), Agent: "yy-测试"})
	}
	rules := filepath.Join(home, "repo", ".cursor", "rules")
	w(t, filepath.Join(rules, "old.mdc"), "keep")
	targets = append(targets, Target{Path: rules, Agent: "cursor"})

	// 预演：什么都不写。
	out, err := Install(targets, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if !strings.HasPrefix(o.Action, "预演") {
			t.Fatalf("%+v", o)
		}
	}
	if _, err := os.Stat(backups); !os.IsNotExist(err) {
		t.Fatal("dry run wrote backups")
	}

	out, err = Install(targets, Options{BackupRoot: backups})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Error != "" {
			t.Fatalf("%+v", o)
		}
	}
	for name, s := range origs {
		if name == "yy-cursor-root" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(home, name))
		if !bytes.HasPrefix(b, []byte(s)) || !bytes.HasSuffix(b, Segment("yy-测试")) || len(b) != len(s)+len(Segment("yy-测试")) {
			t.Fatalf("%s: %q", name, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(rules, "old.mdc")); string(b) != "keep" {
		t.Fatal("cursor rule touched")
	}
	if !Installed(rules) || !Installed(filepath.Join(home, "yy-AGENTS.md")) {
		t.Fatal("installed")
	}
	// 备份和清单
	dirs, _ := os.ReadDir(backups)
	if len(dirs) != 1 {
		t.Fatal("backup dir")
	}
	var man []manifestEntry
	mb, _ := os.ReadFile(filepath.Join(backups, dirs[0].Name(), "manifest.json"))
	if err := json.Unmarshal(mb, &man); err != nil || len(man) != 4 {
		t.Fatalf("manifest %s", mb)
	}
	for _, m := range man {
		b, _ := os.ReadFile(m.Backup)
		if sum(b) != m.SHA256 || string(b) != origs[filepath.Base(m.Path)] {
			t.Fatalf("backup %+v", m)
		}
	}

	// 再装一次：不重复追加。
	before, _ := os.ReadFile(filepath.Join(home, "yy-AGENTS.md"))
	out, _ = Install(targets, Options{BackupRoot: backups})
	for _, o := range out {
		if o.Action != "已接入，跳过" {
			t.Fatalf("%+v", o)
		}
	}
	after, _ := os.ReadFile(filepath.Join(home, "yy-AGENTS.md"))
	if !bytes.Equal(before, after) || bytes.Count(after, []byte(Begin)) != 1 {
		t.Fatal("double install")
	}

	// 安装后用户又在后面写了东西：卸载只删我们那段，用户新写的保留。
	f, _ := os.OpenFile(filepath.Join(home, "yy-AGENTS.md"), os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString("- 用户后来加的\n")
	f.Close()

	var paths []string
	for _, t := range targets {
		paths = append(paths, t.Path)
	}
	out, err = Uninstall(paths, Options{BackupRoot: backups})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if o.Error != "" {
			t.Fatalf("%+v", o)
		}
	}
	for name, s := range origs {
		if name == "yy-cursor-root" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(home, name))
		if name == "yy-AGENTS.md" {
			s += "- 用户后来加的\n"
		}
		if string(b) != s {
			t.Fatalf("%s after uninstall: %q want %q", name, b, s)
		}
	}
	if Installed(rules) {
		t.Fatal("cursor rule not moved")
	}
	if b, _ := os.ReadFile(filepath.Join(rules, "old.mdc")); string(b) != "keep" {
		t.Fatal("cursor rule touched")
	}
	// 卸载第二次：没接入过。
	out, _ = Uninstall(paths[:1], Options{BackupRoot: backups})
	if out[0].Action != "没接入过" {
		t.Fatalf("%+v", out[0])
	}
}

func TestRefuse(t *testing.T) {
	home := t.TempDir()
	real := w(t, filepath.Join(home, "real.md"), "x")
	link := filepath.Join(home, "AGENTS.md")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	out, _ := Install([]Target{{Path: link, Agent: "a"}, {Path: real}}, Options{BackupRoot: filepath.Join(home, "b")})
	if out[0].Error == "" || out[1].Error == "" {
		t.Fatalf("%+v", out)
	}
	if b, _ := os.ReadFile(real); string(b) != "x" {
		t.Fatal("touched")
	}
	// 被人改坏的段：卸载拒绝动。
	bad := w(t, filepath.Join(home, "bad.md"), "a\n"+Begin+"\n改了\n")
	out, _ = Uninstall([]string{bad}, Options{BackupRoot: filepath.Join(home, "b")})
	if out[0].Error == "" {
		t.Fatalf("%+v", out)
	}
}
