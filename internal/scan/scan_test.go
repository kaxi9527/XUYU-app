package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

// fixture 造一个假的主目录：
//
//	Projects/site（git 主仓）+ worktrees/site-feature（worktree）
//	Projects/site 副本（手工副本，无 git）
//	Projects/other（另一个 git 仓库，名字不同）
//	Projects/site2-clone（第二个 clone，同 origin）
//	Desktop/海报（12 张图 → 图片组）
//	Downloads/site-r3.zip（交付包，名字对上 site）
//	Downloads/随手记.md、Downloads/.env、Downloads/api-token.txt（后两个是敏感名）
//	Library/x.txt（系统目录，跳过）、Projects/link -> /（软链接，跳过）
func fixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	site := filepath.Join(home, "Projects", "site")
	makeRepo(t, site, "git@github.com:acme/site.git")
	for _, f := range []string{"package.json", "README.md", "index.html", "app.js"} {
		write(t, filepath.Join(site, f), f)
	}
	makeWorktree(t, site, filepath.Join(home, "worktrees", "site-feature"), "site-feature")
	write(t, filepath.Join(home, "worktrees", "site-feature", "package.json"), "{}")

	cp := filepath.Join(home, "Projects", "site 副本")
	for _, f := range []string{"package.json", "README.md", "index.html", "app.js", "old.css"} {
		write(t, filepath.Join(cp, f), f)
	}
	other := filepath.Join(home, "Projects", "other")
	makeRepo(t, other, "https://github.com/acme/other.git")
	write(t, filepath.Join(other, "go.mod"), "module x")

	clone := filepath.Join(home, "Projects", "site2-clone")
	makeRepo(t, clone, "https://github.com/acme/site")
	write(t, filepath.Join(clone, "package.json"), "{}")

	// 名字像 site 但属于另一个仓库，不能并进 site 组。
	imposter := filepath.Join(home, "Projects", "site-2")
	makeRepo(t, imposter, "https://github.com/someone/else.git")
	for _, f := range []string{"package.json", "README.md", "index.html", "app.js"} {
		write(t, filepath.Join(imposter, f), f)
	}

	for i := 0; i < 12; i++ {
		write(t, filepath.Join(home, "Desktop", "海报", "p"+string(rune('a'+i))+".png"), "img")
	}
	write(t, filepath.Join(home, "Downloads", "site-r3.zip"), "zip")
	write(t, filepath.Join(home, "Downloads", "随手记.md"), "note")
	write(t, filepath.Join(home, "Downloads", ".env"), "SECRET=1")
	write(t, filepath.Join(home, "Downloads", "api-token.txt"), "x")
	write(t, filepath.Join(home, "Library", "x.txt"), "x")
	write(t, filepath.Join(home, "CLAUDE.md"), "home rules")
	must(t, os.Symlink("/", filepath.Join(home, "Projects", "link")))
	return home
}

func scanFixture(t *testing.T, home string) *Result {
	t.Helper()
	roots := []string{
		filepath.Join(home, "Desktop"), filepath.Join(home, "Downloads"),
		filepath.Join(home, "Projects"), filepath.Join(home, "worktrees"), home,
	}
	res, err := Scan(Options{
		Roots: roots, Containers: roots, ShallowRoots: map[string]int{home: 1},
		Exclude: []string{filepath.Join(home, "Work")}, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGoToolchainIsNotReportedAsUserProjects(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, "sdk", "go1.99.1")
	write(t, filepath.Join(sdk, "bin", "go"), "toolchain")
	write(t, filepath.Join(sdk, "src", "runtime", "runtime.go"), "runtime")
	write(t, filepath.Join(sdk, "src", "go.mod"), "module std")
	write(t, filepath.Join(sdk, "misc", "Makefile"), "all:")
	project := filepath.Join(home, "sdk", "my-project")
	write(t, filepath.Join(project, "go.mod"), "module project")
	res, err := Scan(Options{Roots: []string{home}, Containers: []string{home}, MaxDepth: 5})
	if err != nil {
		t.Fatal(err)
	}
	foundProject := false
	for _, p := range res.Projects {
		if p.Path == project {
			foundProject = true
		}
		if p.Path == sdk || strings.HasPrefix(p.Path, sdk+string(filepath.Separator)) {
			t.Fatalf("toolchain reported as project: %s", p.Path)
		}
	}
	if !foundProject {
		t.Fatal("ordinary project under sdk was not found")
	}
}

func TestScanFixture(t *testing.T) {
	home := fixture(t)
	before := snapshot(t, home)
	res := scanFixture(t, home)

	byPath := map[string]Project{}
	for _, p := range res.Projects {
		byPath[p.Path] = p
	}
	site := byPath[filepath.Join(home, "Projects", "site")]
	wt := byPath[filepath.Join(home, "worktrees", "site-feature")]
	cp := byPath[filepath.Join(home, "Projects", "site 副本")]
	clone := byPath[filepath.Join(home, "Projects", "site2-clone")]
	imp := byPath[filepath.Join(home, "Projects", "site-2")]
	other := byPath[filepath.Join(home, "Projects", "other")]
	img := byPath[filepath.Join(home, "Desktop", "海报")]

	if site.Path == "" || wt.Path == "" || cp.Path == "" || clone.Path == "" || img.Path == "" {
		t.Fatalf("missing projects: %+v", res.Projects)
	}
	if site.Group != wt.Group || site.Group != cp.Group || site.Group != clone.Group {
		t.Fatalf("site group mismatch: site=%d wt=%d cp=%d clone=%d", site.Group, wt.Group, cp.Group, clone.Group)
	}
	if imp.Group == site.Group || other.Group == site.Group {
		t.Fatal("different repos must not be merged")
	}
	// 组名用文件夹名（去后缀），不用 origin 里的仓库名。
	if n := res.Groups[imp.Group].Name; n != "site" {
		t.Fatalf("imposter group name = %s", n)
	}
	if site.Kind != "main" || wt.Kind != "worktree" || cp.Kind != "copy" || clone.Kind != "clone" {
		t.Fatalf("kinds: %s %s %s %s", site.Kind, wt.Kind, cp.Kind, clone.Kind)
	}
	if img.Category != "images" {
		t.Fatalf("image group category = %s", img.Category)
	}
	if res.Groups[site.Group].Name != "site" {
		t.Fatalf("group name = %s", res.Groups[site.Group].Name)
	}
	// 主目录本身有 CLAUDE.md 但不算项目。
	if _, ok := byPath[home]; ok {
		t.Fatal("home must not be a project")
	}
	if res.SensitiveSkipped != 2 {
		t.Fatalf("sensitive skipped = %d", res.SensitiveSkipped)
	}
	if res.SymlinksSkipped < 1 {
		t.Fatal("symlink should be skipped")
	}
	var zip, note *LooseFile
	for i := range res.Loose {
		l := &res.Loose[i]
		switch filepath.Base(l.Path) {
		case "site-r3.zip":
			zip = l
		case "随手记.md":
			note = l
		case "x.txt":
			t.Fatal("Library must be skipped")
		}
	}
	if zip == nil || zip.Group != site.Group {
		t.Fatalf("zip not matched to site: %+v", zip)
	}
	if note == nil || note.Group != -1 {
		t.Fatalf("note: %+v", note)
	}
	// 只读：扫描前后文件树一模一样。
	if after := snapshot(t, home); after != before {
		t.Fatal("scan changed the file tree")
	}
}

func TestScanLimitsPartial(t *testing.T) {
	home := fixture(t)
	res, err := Scan(Options{Roots: []string{home}, Containers: []string{home}, MaxDirs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial {
		t.Fatal("want partial")
	}
}

// snapshot 记录目录树（路径+大小+修改时间），用来证明扫描没改东西。
func snapshot(t *testing.T, root string) string {
	t.Helper()
	s := ""
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		s += p + "|" + fi.ModTime().String() + "|" + string(rune(fi.Size())) + "\n"
		return nil
	})
	return s
}
