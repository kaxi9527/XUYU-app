package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	want := "github.com/acme/site"
	for _, u := range []string{
		"git@github.com:acme/site.git",
		"https://github.com/acme/site",
		"https://github.com/Acme/site.git/",
		"ssh://git@github.com/acme/site.git",
		"ssh://git@github.com:22/acme/site.git",
		"https://user:pw@github.com/acme/site.git",
	} {
		if got := NormalizeRemote(u); got != want {
			t.Errorf("NormalizeRemote(%q) = %q", u, got)
		}
	}
}

// makeRepo 手工造一个最小的 .git 目录（不需要装 git）。
func makeRepo(t *testing.T, dir, origin string) {
	t.Helper()
	gd := filepath.Join(dir, ".git")
	must(t, os.MkdirAll(filepath.Join(gd, "refs", "heads"), 0o755))
	must(t, os.WriteFile(filepath.Join(gd, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(gd, "refs", "heads", "main"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644))
	cfg := "[core]\n\tbare = false\n"
	if origin != "" {
		cfg += "[remote \"origin\"]\n\turl = " + origin + "\n"
	}
	must(t, os.WriteFile(filepath.Join(gd, "config"), []byte(cfg), 0o644))
}

// makeWorktree 手工造一个 linked worktree。
func makeWorktree(t *testing.T, mainDir, wtDir, name string) {
	t.Helper()
	gd := filepath.Join(mainDir, ".git", "worktrees", name)
	must(t, os.MkdirAll(gd, 0o755))
	must(t, os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(gd, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(wtDir, ".git")+"\n"), 0o644))
	must(t, os.MkdirAll(wtDir, 0o755))
	must(t, os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+gd+"\n"), 0o644))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadGitMainAndWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "site")
	wt := filepath.Join(root, "site-feature")
	makeRepo(t, main, "git@github.com:acme/site.git")
	makeWorktree(t, main, wt, "site-feature")

	gm, ok := readGit(main)
	if !ok || gm.IsWorktree || gm.Identity != "github.com/acme/site" || gm.Branch != "main" || gm.Head != "0123456789ab" {
		t.Fatalf("main = %+v", gm)
	}
	if len(gm.Worktrees) != 1 || gm.Worktrees[0] != wt {
		t.Fatalf("worktrees = %v", gm.Worktrees)
	}
	gw, ok := readGit(wt)
	if !ok || !gw.IsWorktree || gw.Identity != gm.Identity || gw.Branch != "feature" {
		t.Fatalf("wt = %+v", gw)
	}
}

func TestReadGitNoOrigin(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, "")
	g, ok := readGit(root)
	if !ok || g.Identity == "" || g.Identity[:6] != "local:" {
		t.Fatalf("g = %+v", g)
	}
}
