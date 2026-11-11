package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildGroupsCopiesAndSymlinks(t *testing.T) {
	home := t.TempDir()
	j := func(p ...string) string { return filepath.Join(append([]string{home}, p...)...) }
	write(t, j("Projects", "Acme", "package.json"), "{}")
	write(t, j("Projects", "Acme", "README.md"), "# Acme\n")
	write(t, j("Projects", "Acme", "src", "a.ts"), "x")
	write(t, j("Projects", "acme-handoff-r2", "README.md"), "交付包")
	write(t, j("Projects", "acme-handoff-r2", "source", "core", "pyproject.toml"), "")
	write(t, j("Projects", "acme-handoff-r2.zip"), "zip")
	write(t, j("Projects", "acmedemo", "go.mod"), "module acmedemo") // 名字以 acme 开头但后面接字母：不是副本
	write(t, j("Desktop", "Bar", "package.json"), "{}")
	write(t, j("elsewhere", "baz", "go.mod"), "module baz")
	if err := os.Symlink(j("elsewhere", "baz"), j("Projects", "Baz")); err != nil {
		t.Fatal(err)
	}
	write(t, j("Work", "Mine", "00-说明.md"), "已归位的项目")
	cfg := config.Default(home)
	cfg.ScanPaths = []string{j("Desktop"), j("Projects"), home}
	opt, err := ScanOptions(cfg, home, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := Build(cfg, home, opt)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*Project{}
	for i := range reg.Projects {
		p := &reg.Projects[i]
		names[p.Name] = p
		if p.Status != Status {
			t.Errorf("%s 状态 %q", p.Name, p.Status)
		}
	}
	for _, want := range []string{"Acme", "acmedemo", "Bar", "baz"} {
		if names[want] == nil {
			t.Errorf("没识别出 %s；有 %v", want, keys(names))
		}
	}
	for n := range names {
		if n == "core" || n == "Mine" || n == "acme-handoff" || n == "source/core" {
			t.Errorf("%s 不该单独成项目", n)
		}
	}
	foo := names["Acme"]
	if foo == nil {
		t.FailNow()
	}
	kinds := map[string]string{}
	for _, m := range foo.Members {
		kinds[filepath.Base(m.Path)] = m.Kind
	}
	if kinds["acme-handoff-r2"] != KindHandoff || kinds["acme-handoff-r2.zip"] != KindArchive {
		t.Errorf("副本没归到 Acme：%v", kinds)
	}
	if _, ok := kinds["core"]; ok {
		t.Errorf("交付包里面的 core 不该单列：%v", kinds)
	}
	if b := names["baz"]; b == nil || len(b.Aliases) != 1 || b.Aliases[0] != j("Projects", "Baz") {
		t.Errorf("软链接别名不对：%+v", b)
	}
	if o := reg.Owner(j("Projects", "acme-handoff-r2", "source", "core", "pyproject.toml")); o == nil || o.Name != "Acme" {
		t.Errorf("Owner 不对：%+v", o)
	}
	if reg.Owner(j("Work", "Mine", "00-说明.md")) != nil {
		t.Error("总文件夹里的不该算原地项目")
	}
	cfg.Root = j("Work")
	if _, err := Save(cfg, reg, home); err != nil {
		t.Fatal(err)
	}
	got, err := Load(cfg)
	if err != nil || len(got.Projects) != len(reg.Projects) || got.ByID(foo.ID) == nil {
		t.Fatalf("读回来不对：%v %+v", err, got)
	}
	if _, err := os.Stat(filepath.Join(Dir(cfg), "原地项目.md")); err != nil {
		t.Error(err)
	}
	// 什么都没被搬：原文件都还在。
	for _, p := range []string{j("Projects", "acme-handoff-r2.zip"), j("Projects", "Acme", "src", "a.ts")} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
}

func keys(m map[string]*Project) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
