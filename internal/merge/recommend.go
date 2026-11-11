package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// Recommendation evidence is deliberately separate from move/dedup authority.
// Only small, non-generic regular files are inspected; this is not a full scan.
type evidenceBudget struct {
	roots     []string
	remaining int64
	deadline  time.Time
	cache     map[string]map[string]string
}

func newEvidenceBudget(roots ...string) *evidenceBudget {
	return &evidenceBudget{roots: roots, remaining: 8 << 20, deadline: time.Now().Add(800 * time.Millisecond), cache: map[string]map[string]string{}}
}

func recommend(hs []hit, budget *evidenceBudget) (string, []string) {
	identities := map[string]bool{}
	ids := make([]string, len(hs))
	for i, h := range hs {
		if h.origin != "" {
			ids[i] = scan.NormalizeRemote(h.origin)
		}
		if budget.permitted(h.path) {
			if live := scan.RepositoryIdentity(h.path); live != "" {
				ids[i] = live
			}
		}
		if ids[i] != "" {
			identities[ids[i]] = true
		}
	}
	if len(identities) > 1 {
		return "conflict", []string{"名字相近，但属于不同 Git 仓库；不推荐合并"}
	}
	if len(hs) > 64 {
		return "possible", []string{"名字去后缀后相同；来源较多，请先分组确认"}
	}
	strong, sameRepo, minCommon := true, false, 0
	for i := range hs {
		for j := 0; j < i; j++ {
			a, b := hs[i], hs[j]
			if ids[i] != "" && ids[i] == ids[j] {
				sameRepo = true
				continue
			}
			left, right := budget.files(a.path), budget.files(b.path)
			common := 0
			for name, hash := range left {
				if hash != "" && right[name] == hash {
					common++
				}
			}
			if common < 2 {
				strong = false
			}
			if minCommon == 0 || common < minCommon {
				minCommon = common
			}
		}
	}
	if !strong || len(hs) < 2 {
		return "possible", []string{"名字去后缀后相同，尚不足以确认同一项目；请核对来源"}
	}
	reasons := []string{"名字去后缀后相同"}
	if sameRepo {
		reasons = append(reasons, "属于同一 Git 仓库；工程仍保持完整")
	}
	if minCommon >= 2 {
		reasons = append(reasons, fmt.Sprintf("已核对至少 %d 个非通用同名文件内容一致", minCommon))
	}
	return "recommended", reasons
}

func evidenceName(name string) bool {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, ".") || scan.IsSensitiveName(n) {
		return false
	}
	for _, prefix := range []string{"readme", "license", "changelog", "package", "tsconfig", "vite.config", "agents", "claude", "logo", "favicon"} {
		if strings.HasPrefix(n, prefix) {
			return false
		}
	}
	switch filepath.Ext(n) {
	case ".log", ".lock", ".tmp", ".bak", ".json", ".yaml", ".yml", ".toml", ".sum":
		return false
	}
	return true
}

func (b *evidenceBudget) permitted(path string) bool {
	for _, root := range b.roots {
		if root != "" && home.CheckPath(root, path) == nil {
			return true
		}
	}
	return false
}

func (b *evidenceBudget) files(dir string) map[string]string {
	if cached, ok := b.cache[dir]; ok {
		return cached
	}
	result := map[string]string{}
	b.cache[dir] = result
	if b.remaining <= 0 || time.Now().After(b.deadline) || !b.permitted(dir) {
		return result
	}
	f, err := os.Open(dir)
	if err != nil {
		return result
	}
	names, _ := f.Readdirnames(128)
	f.Close()
	sort.Strings(names)
	for _, name := range names {
		if len(result) >= 8 || b.remaining <= 0 || time.Now().After(b.deadline) {
			break
		}
		if !evidenceName(name) {
			continue
		}
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > 512<<10 || fi.Size() > b.remaining {
			continue
		}
		if !b.permitted(path) {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		before, err := file.Stat()
		if err != nil || !os.SameFile(fi, before) {
			file.Close()
			continue
		}
		hash := sha256.New()
		count, err := io.Copy(hash, io.LimitReader(file, fi.Size()+1))
		b.remaining -= count
		after, statErr := file.Stat()
		file.Close()
		if err != nil || statErr != nil || count != fi.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			continue
		}
		result[name] = hex.EncodeToString(hash.Sum(nil))
	}
	return result
}
