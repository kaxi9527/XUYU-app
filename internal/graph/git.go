package graph

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// gitIdentity 只读拿仓库 identity（规范化 origin，或 local:真实.git 路径）。
// 失败返回 false；不调用 git 命令。
func gitIdentity(dir string) (string, bool) {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Lstat(dot)
	if err != nil {
		return "", false
	}
	gitDir, common := "", ""
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	if fi.IsDir() {
		gitDir = dot
		common = dot
		if b, err := os.ReadFile(filepath.Join(dot, "commondir")); err == nil {
			c := strings.TrimSpace(string(b))
			if c != "" {
				if !filepath.IsAbs(c) {
					c = filepath.Join(dot, c)
				}
				common = filepath.Clean(c)
			}
		}
	} else {
		// gitfile: gitdir: ...
		b, err := os.ReadFile(dot)
		if err != nil {
			return "", false
		}
		line := strings.TrimSpace(string(b))
		const pref = "gitdir:"
		if !strings.HasPrefix(strings.ToLower(line), pref) {
			return "", false
		}
		gd := strings.TrimSpace(line[len(pref):])
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		gitDir = filepath.Clean(gd)
		common = gitDir
		if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			c := strings.TrimSpace(string(b))
			if c != "" {
				if !filepath.IsAbs(c) {
					c = filepath.Join(gitDir, c)
				}
				common = filepath.Clean(c)
			}
		}
	}
	origin := readOriginURL(filepath.Join(common, "config"))
	if origin != "" {
		return scan.NormalizeRemote(origin), true
	}
	real, err := filepath.EvalSymlinks(common)
	if err != nil {
		real = common
	}
	return "local:" + real, true
}

func readOriginURL(configPath string) string {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if inOrigin {
			low := strings.ToLower(line)
			if strings.HasPrefix(low, "url") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return ""
}
