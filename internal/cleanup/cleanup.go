// Package cleanup 给出「推荐清理」启发式建议（第一版）。
// 只对真实存在的路径做轻量判断，不夸大成完整静态分析。
package cleanup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

// 分类（给人看，少行话）
const (
	CatDup       = "重复与副本"
	CatArchive   = "多余压缩包"
	CatEmpty     = "空文件夹"
	CatWorktree  = "旧会话与 worktree"
	CatCache     = "依赖与缓存"
	CatDeadLight = "可能用不到的文件"
	CatDecide    = "待你定"
)

const (
	ConfRecommend = "推荐"
	ConfDecide    = "待你定"
)

// Suggestion 一条清理建议。
type Suggestion struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	IsDir      bool   `json:"isDir"`
	Category   string `json:"category"`
	Reason     string `json:"reason"`
	Evidence   string `json:"evidence"`
	Confidence string `json:"confidence"` // 推荐 / 待你定
	PreCheck   bool   `json:"preCheck"`   // 是否默认勾选；待你定一律 false
	ByteSize   int64  `json:"byteSize"`
	FileCount  int    `json:"fileCount"`
	Project    string `json:"project,omitempty"`
}

// Options 控制扫描范围。
type Options struct {
	Config   config.Config
	Home     string
	Registry *registry.Registry // 可空：没有登记表时只扫少量空文件夹/名字启发
	Limit    int                // 最多返回条数，默认 80
}

var (
	reCopyName = regexp.MustCompile(`(?i)(副本| copy|copy |[\s_-]副本|\(\d+\)|[\s_-]v?\d+$|[\s_-]r\d+)`)
	reCacheDir = regexp.MustCompile(`(?i)^(node_modules|\.cache|__pycache__|\.turbo|\.next|dist|build|\.parcel-cache|\.vite)$`)
	reTmpName  = regexp.MustCompile(`(?i)(\.tmp$|~$|\.bak$|\.old$|Thumbs\.db|\.DS_Store$)`)
)

// Suggest 汇总启发式推荐。不确定的进「待你定」且不预勾。
func Suggest(opt Options) ([]Suggestion, error) {
	limit := opt.Limit
	if limit <= 0 {
		limit = 80
	}
	seen := map[string]bool{}
	var out []Suggestion
	add := func(s Suggestion) {
		p := filepath.Clean(s.Path)
		if p == "" || seen[p] {
			return
		}
		if s.Confidence == ConfDecide {
			s.PreCheck = false
			if s.Category == "" {
				s.Category = CatDecide
			}
		}
		s.Path = p
		if s.Name == "" {
			s.Name = filepath.Base(p)
		}
		seen[p] = true
		out = append(out, s)
	}

	root := filepath.Clean(opt.Config.Root)
	control := filepath.Join(root, opt.Config.Names.Control)

	blocked := func(p string) bool {
		p = filepath.Clean(p)
		if p == root || p == opt.Home {
			return true
		}
		if p == control || strings.HasPrefix(p, control+string(os.PathSeparator)) {
			return true
		}
		return false
	}

	if opt.Registry != nil {
		for _, proj := range opt.Registry.Projects {
			for i, m := range proj.Members {
				if i == 0 && m.Path == proj.Path {
					continue // 主版本不推荐删
				}
				if blocked(m.Path) {
					continue
				}
				fi, err := os.Lstat(m.Path)
				if err != nil {
					continue
				}
				if fi.Mode()&os.ModeSymlink != 0 {
					continue
				}
				s := Suggestion{
					Path: m.Path, Name: filepath.Base(m.Path), IsDir: fi.IsDir(),
					Project: proj.Name, Evidence: "原地登记：" + m.Kind,
				}
				switch m.Kind {
				case registry.KindArchive:
					s.Category, s.Confidence, s.PreCheck = CatArchive, ConfRecommend, true
					s.Reason = "登记里已有主版本，这个压缩包多半是交付残留"
				case registry.KindCopy, registry.KindHandoff:
					s.Category, s.Confidence, s.PreCheck = CatDup, ConfRecommend, true
					s.Reason = "名字像副本/交付包，登记已归到项目「" + proj.Name + "」下"
				case registry.KindWorktree, registry.KindClone:
					s.Category, s.Confidence, s.PreCheck = CatWorktree, ConfDecide, false
					s.Reason = "可能是 git worktree / 另一个 clone，删前请确认不再用"
				case registry.KindFile:
					if looksLikeCopyName(filepath.Base(m.Path)) {
						s.Category, s.Confidence, s.PreCheck = CatDup, ConfRecommend, true
						s.Reason = "散落相关文件，名字像旧副本"
					} else {
						continue
					}
				default:
					if looksLikeCopyName(filepath.Base(m.Path)) {
						s.Category, s.Confidence, s.PreCheck = CatDup, ConfDecide, false
						s.Reason = "名字像副本，但类型不明确，请你定"
					} else {
						continue
					}
				}
				fillSize(&s, fi)
				add(s)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
	}

	// 桌面/下载顶层：空文件夹、明显副本名
	for _, base := range []string{
		filepath.Join(opt.Home, "Desktop"),
		filepath.Join(opt.Home, "Downloads"),
		filepath.Join(opt.Home, "Documents"),
	} {
		ents, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range ents {
			p := filepath.Join(base, e.Name())
			if blocked(p) || seen[p] {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if e.IsDir() {
				empty, n, err := dirStats(p, 400)
				if err != nil {
					continue
				}
				if empty {
					s := Suggestion{
						Path: p, Name: e.Name(), IsDir: true,
						Category: CatEmpty, Confidence: ConfRecommend, PreCheck: true,
						Reason: "空文件夹，里面没有文件", Evidence: "扫了 " + short(base, opt.Home) + " 顶层",
						FileCount: 0, ByteSize: 0,
					}
					add(s)
				} else if reCacheDir.MatchString(e.Name()) {
					s := Suggestion{
						Path: p, Name: e.Name(), IsDir: true,
						Category: CatCache, Confidence: ConfDecide, PreCheck: false,
						Reason: "名字像依赖/缓存目录，删了可能要重新安装", Evidence: short(p, opt.Home),
						FileCount: n,
					}
					add(s)
				} else if looksLikeCopyName(e.Name()) {
					s := Suggestion{
						Path: p, Name: e.Name(), IsDir: true,
						Category: CatDup, Confidence: ConfDecide, PreCheck: false,
						Reason: "名字像「副本 / copy / (2)」", Evidence: "在 " + short(base, opt.Home),
						FileCount: n,
					}
					fillSize(&s, fi)
					add(s)
				}
			} else {
				name := e.Name()
				low := strings.ToLower(name)
				if looksLikeCopyName(name) || (strings.HasSuffix(low, ".zip") && (strings.Contains(low, "handoff") || strings.Contains(name, "副本") || reCopyName.MatchString(name))) {
					s := Suggestion{
						Path: p, Name: name, IsDir: false,
						Category: CatArchive, Confidence: ConfRecommend, PreCheck: true,
						Reason: "下载/桌面上的交付压缩包或副本文件", Evidence: short(base, opt.Home),
						FileCount: 1, ByteSize: fi.Size(),
					}
					add(s)
				} else if reTmpName.MatchString(name) {
					s := Suggestion{
						Path: p, Name: name, IsDir: false,
						Category: CatDeadLight, Confidence: ConfDecide, PreCheck: false,
						Reason: "临时/备份文件名", Evidence: short(base, opt.Home),
						FileCount: 1, ByteSize: fi.Size(),
					}
					add(s)
				}
			}
			if len(out) >= limit {
				return out, nil
			}
		}
	}

	return out, nil
}

// ProbeProjectLight 对单个项目目录做轻量「可能用不到」探测（占位级，不宣称死代码分析）。
func ProbeProjectLight(projectPath string, limit int) ([]Suggestion, error) {
	if limit <= 0 {
		limit = 30
	}
	projectPath = filepath.Clean(projectPath)
	fi, err := os.Stat(projectPath)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("不是文件夹：%s", projectPath)
	}
	var out []Suggestion
	_ = filepath.WalkDir(projectPath, func(p string, d os.DirEntry, err error) error {
		if err != nil || len(out) >= limit {
			if len(out) >= limit {
				return fmt.Errorf("stop")
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if reCacheDir.MatchString(name) {
				info, _ := d.Info()
				s := Suggestion{
					Path: p, Name: name, IsDir: true,
					Category: CatCache, Confidence: ConfDecide, PreCheck: false,
					Reason: "项目里的依赖/缓存目录（需你确认）", Evidence: "轻量目录名匹配，不是静态分析",
				}
				if info != nil {
					fillSize(&s, info)
				}
				out = append(out, s)
				return filepath.SkipDir
			}
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if reTmpName.MatchString(name) {
			info, _ := d.Info()
			s := Suggestion{
				Path: p, Name: name, IsDir: false,
				Category: CatDeadLight, Confidence: ConfDecide, PreCheck: false,
				Reason: "临时/备份文件名", Evidence: "轻量文件名匹配", FileCount: 1,
			}
			if info != nil {
				s.ByteSize = info.Size()
			}
			out = append(out, s)
		}
		return nil
	})
	return out, nil
}

func looksLikeCopyName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return false
	}
	base := scan.CopyKey(n)
	if base != strings.ToLower(strings.TrimSuffix(n, filepath.Ext(n))) && reCopyName.MatchString(n) {
		return true
	}
	return reCopyName.MatchString(n)
}

func fillSize(s *Suggestion, fi os.FileInfo) {
	if fi.IsDir() {
		n, sz, err := countTree(s.Path, 5000)
		if err == nil {
			s.FileCount, s.ByteSize = n, sz
		}
	} else {
		s.FileCount, s.ByteSize = 1, fi.Size()
	}
}

func countTree(dir string, maxFiles int) (files int, bytes int64, err error) {
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		files++
		if info, e2 := d.Info(); e2 == nil {
			bytes += info.Size()
		}
		if files >= maxFiles {
			return fmt.Errorf("cap")
		}
		return nil
	})
	if err != nil && err.Error() == "cap" {
		err = nil
	}
	return files, bytes, err
}

func dirStats(dir string, maxFiles int) (empty bool, files int, err error) {
	n, _, err := countTree(dir, maxFiles)
	return n == 0, n, err
}

func short(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

// AgeDays 辅助测试/展示。
func AgeDays(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(time.Since(t).Hours() / 24)
}
