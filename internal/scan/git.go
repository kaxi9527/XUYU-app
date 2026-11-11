package scan

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// GitInfo 是只读 .git 文件得到的仓库信息（不改任何 git 状态）。
type GitInfo struct {
	IsWorktree  bool     `json:"is_worktree"`            // .git 是文件且指向共享仓库的 worktrees/
	IsSubmodule bool     `json:"is_submodule,omitempty"` // .git 指向父仓库的 modules/
	GitDir      string   `json:"git_dir"`
	CommonDir   string   `json:"common_dir"`
	Origin      string   `json:"origin,omitempty"`
	Identity    string   `json:"identity"` // 规范化的 origin，没有 origin 时用 local:<共享 .git 真实路径>
	Branch      string   `json:"branch,omitempty"`
	Head        string   `json:"head,omitempty"` // 当前提交 sha12
	Worktrees   []string `json:"worktrees,omitempty"`
	Locked      bool     `json:"locked,omitempty"` // 这个 worktree 被 git worktree lock 锁住
	// 下面三项来自 `git --no-optional-locks status --porcelain`（只读）。
	StatusChecked bool   `json:"status_checked"`
	Dirty         bool   `json:"dirty"`
	DirtyCount    int    `json:"dirty_count,omitempty"`
	StatusError   string `json:"status_error,omitempty"`
}

// readGit 读取 dir/.git（目录或文件），不调用 git 命令。
// RepositoryIdentity reuses the scanner's local Git/worktree identity reader.
// It does not run Git, contact remotes, or expose config content.
func RepositoryIdentity(dir string) string {
	if info, ok := readGit(dir); ok {
		return info.Identity
	}
	return ""
}

func readGit(dir string) (*GitInfo, bool) {
	dotgit := filepath.Join(dir, ".git")
	fi, err := os.Lstat(dotgit)
	if err != nil {
		return nil, false
	}
	gi := &GitInfo{}
	switch {
	case fi.IsDir():
		gi.GitDir = dotgit
		gi.CommonDir = dotgit
	case fi.Mode().IsRegular():
		data, err := os.ReadFile(dotgit)
		if err != nil {
			return nil, false
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "gitdir:") {
			return nil, false
		}
		gd := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		gi.GitDir = filepath.Clean(gd)
		gi.CommonDir = gi.GitDir
		if cd, err := os.ReadFile(filepath.Join(gi.GitDir, "commondir")); err == nil {
			c := strings.TrimSpace(string(cd))
			if !filepath.IsAbs(c) {
				c = filepath.Join(gi.GitDir, c)
			}
			gi.CommonDir = filepath.Clean(c)
			gi.IsWorktree = true
		} else if strings.Contains(filepath.ToSlash(gi.GitDir), "/modules/") {
			gi.IsSubmodule = true
		}
		if _, err := os.Stat(filepath.Join(gi.GitDir, "locked")); err == nil {
			gi.Locked = true
		}
	default:
		return nil, false
	}
	gi.Origin = readOrigin(filepath.Join(gi.CommonDir, "config"))
	if gi.Origin != "" {
		gi.Identity = NormalizeRemote(gi.Origin)
	} else {
		real := gi.CommonDir
		if r, err := filepath.EvalSymlinks(real); err == nil {
			real = r
		}
		gi.Identity = "local:" + real
	}
	gi.Branch, gi.Head = readHead(gi.GitDir, gi.CommonDir)
	if !gi.IsWorktree {
		gi.Worktrees = listWorktrees(gi.CommonDir)
	}
	return gi, true
}

func readOrigin(configPath string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inOrigin := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if inOrigin {
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(k) == "url" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

var scpLike = regexp.MustCompile(`^(?:[\w.-]+@)?([\w.-]+):(.+)$`)

// NormalizeRemote 把 git@github.com:a/b.git、https://github.com/a/b、ssh://git@github.com/a/b 视为同一个：github.com/a/b。
func NormalizeRemote(u string) string {
	s := strings.TrimSpace(u)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.LastIndex(strings.SplitN(s, "/", 2)[0], "@"); at >= 0 {
			s = s[at+1:]
		}
		host, path, _ := strings.Cut(s, "/")
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h // 去掉端口
		}
		s = host + "/" + path
	} else if m := scpLike.FindStringSubmatch(s); m != nil && !strings.HasPrefix(s, "/") {
		s = m[1] + "/" + strings.TrimPrefix(m[2], "/")
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	return strings.ToLower(s)
}

func readHead(gitDir, commonDir string) (branch, head string) {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", ""
	}
	h := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(h, "ref:"); ok {
		ref = strings.TrimSpace(ref)
		branch = strings.TrimPrefix(ref, "refs/heads/")
		sha := ""
		for _, d := range []string{gitDir, commonDir} {
			if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(ref))); err == nil {
				sha = strings.TrimSpace(string(b))
				break
			}
		}
		if sha == "" {
			sha = packedRef(filepath.Join(commonDir, "packed-refs"), ref)
		}
		return branch, short(sha)
	}
	return "", short(h) // 分离 HEAD
}

func packedRef(path, ref string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == ref {
			return f[0]
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

// listWorktrees 列出主仓登记的 linked worktree 路径（读 .git/worktrees/*/gitdir）。
func listWorktrees(commonDir string) []string {
	entries, err := os.ReadDir(filepath.Join(commonDir, "worktrees"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(commonDir, "worktrees", e.Name(), "gitdir"))
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(b))
		out = append(out, filepath.Dir(p))
	}
	return out
}

// checkStatus 用 `git --no-optional-locks status --porcelain` 只读地查未提交改动。
// --no-optional-locks 让 git 不去刷新 index，不写任何东西。
func checkStatus(gi *GitInfo, dir string, timeout time.Duration) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		gi.StatusError = "PATH 里没有 git"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBin, "--no-optional-locks", "-C", dir, "status", "--porcelain", "--untracked-files=normal")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "git status 超时"
		}
		gi.StatusError = msg
		return
	}
	gi.StatusChecked = true
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	gi.Dirty = n > 0
	gi.DirtyCount = n
}
