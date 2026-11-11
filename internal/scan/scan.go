// Package scan 只读地扫描本机，找出项目文件夹和散落文件。
//
// 绝不写、不移、不删任何东西；不跟随软链接；跳过系统目录和缓存。
// 项目识别思路：.git 身份（含 worktree）、去副本后缀、顶层文件重合度、并查集分组。
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version 是扫描结果格式的版本号。
const Version = 1

// Options 控制一次扫描。
type Options struct {
	Roots          []string       // 扫描根
	Containers     []string       // 只当「容器」的目录（主目录、桌面、下载……），本身永远不算项目
	ShallowRoots   map[string]int // 某些根只往下扫几层（例如主目录 1 层）
	MaxDepth       int
	MaxDirs        int
	MaxFiles       int
	Timeout        time.Duration
	Exclude        []string // 绝对路径，整个跳过（例如 ~/Work 自己）
	ExtraSkipNames []string
	GitStatus      bool
	GitTimeout     time.Duration
	Now            time.Time
}

// Project 是一个项目根候选。
type Project struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`      // 文件夹名
	BaseName   string    `json:"base_name"` // 去掉副本/版本后缀后的名字
	Markers    []string  `json:"markers"`   // 命中的项目标志
	Category   string    `json:"category"`  // code（代码/智能体）、media（剪辑工程）、images（图片组）、doc（说明文件）
	TopEntries int       `json:"top_entries"`
	ModTime    time.Time `json:"mod_time"` // 顶层条目里最新的修改时间
	Device     uint64    `json:"device"`
	Git        *GitInfo  `json:"git,omitempty"`
	Group      int       `json:"group"`
	Kind       string    `json:"kind"` // main 主版本 / worktree / clone / copy 手工副本 / single 单独
	top        map[string]bool
}

// LooseFile 是不在任何项目里的散落文件。
type LooseFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Category string    `json:"category"`
	Device   uint64    `json:"device"`
	Group    int       `json:"group"` // 名字对上了哪个项目组，-1 表示没对上
}

// Group 是同一项目的一组版本。
type Group struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Leader  string   `json:"leader"`
	Members []string `json:"members"`
	Reasons []string `json:"reasons"`
}

// Result 是扫描结果。
type Result struct {
	Version          int         `json:"version"`
	GeneratedAt      time.Time   `json:"generated_at"`
	Roots            []string    `json:"roots"`
	Partial          bool        `json:"partial"`
	PartialReasons   []string    `json:"partial_reasons,omitempty"`
	DirsVisited      int         `json:"dirs_visited"`
	Unreadable       int         `json:"unreadable"`
	SymlinksSkipped  int         `json:"symlinks_skipped"`
	SensitiveSkipped int         `json:"sensitive_skipped"`
	Projects         []Project   `json:"projects"`
	Groups           []Group     `json:"groups"`
	Loose            []LooseFile `json:"loose"`
}

// 跳过的目录名（大小写不敏感）。
var skipNames = map[string]bool{
	"node_modules": true, "bower_components": true, "dist": true, "build": true, "out": true,
	"target": true, "vendor": true, "coverage": true, "artifacts": true, "library": true,
	"applications": true, "pictures": true, "music": true, "movies": true, "pods": true,
	"deriveddata": true, "__pycache__": true, "venv": true, "site-packages": true, "go": true,
	"snap": true, "caches": true, "cache": true, "tmp": true, "temp": true,
	"system volume information": true, "$recycle.bin": true, "backups.backupdb": true,
	"opt": true, "miniconda3": true, "anaconda3": true,
}

var skipSuffixes = []string{".app", ".photoslibrary", ".bundle", ".framework", ".xcassets", ".lproj", ".tmp", ".musiclibrary", ".tvlibrary", ".backupbundle", ".sparsebundle"}

// 强标志：有它就一定当项目。
var strongMarkers = []string{".git", "package.json", "pyproject.toml", "requirements.txt", "Cargo.toml", "go.mod", "pom.xml", "build.gradle", "build.gradle.kts", "docker-compose.yml", "docker-compose.yaml", "Gemfile", "composer.json", "Package.swift", "CMakeLists.txt", "Makefile"}

// 弱标志：智能体痕迹和本工具自己的说明文件。扫描根本身只有弱标志时不算项目。
var weakMarkers = []string{"AGENTS.md", "CLAUDE.md", ".ai-output", ".uxqa", ".cursor", "00-说明.md"}

var mediaProjectExts = map[string]bool{".prproj": true, ".fcpbundle": true, ".fcpxml": true, ".drp": true, ".aep": true, ".kdenlive": true, ".veg": true}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".heic": true, ".tif": true, ".tiff": true, ".bmp": true, ".avif": true}

// MinImageGroup 是一个文件夹至少有多少张图才算「图片组」。
const MinImageGroup = 10

// Scan 执行一次只读扫描。
func Scan(opt Options) (*Result, error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	if opt.MaxDepth <= 0 {
		opt.MaxDepth = 6
	}
	if opt.MaxDirs <= 0 {
		opt.MaxDirs = 40000
	}
	if opt.MaxFiles <= 0 {
		opt.MaxFiles = 20000
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}
	if opt.GitTimeout <= 0 {
		opt.GitTimeout = 10 * time.Second
	}
	res := &Result{Version: Version, GeneratedAt: opt.Now}
	deadline := time.Now().Add(opt.Timeout)

	extraSkip := map[string]bool{}
	var exclude []string
	for _, e := range opt.Exclude {
		exclude = append(exclude, filepath.Clean(e))
	}
	for _, s := range opt.ExtraSkipNames {
		if filepath.IsAbs(s) {
			exclude = append(exclude, filepath.Clean(s))
		} else {
			extraSkip[strings.ToLower(s)] = true
		}
	}
	containers := map[string]bool{}
	for _, c := range opt.Containers {
		containers[filepath.Clean(c)] = true
	}

	type item struct {
		path     string
		depth    int
		maxDepth int
		isRoot   bool
	}
	var queue []item
	visited := map[string]bool{}
	for _, r := range opt.Roots {
		r = filepath.Clean(r)
		fi, err := os.Lstat(r)
		if err != nil || !fi.IsDir() {
			// 不存在或是软链接：不跟随，跳过。
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				res.SymlinksSkipped++
			}
			continue
		}
		md := opt.MaxDepth
		if d, ok := opt.ShallowRoots[r]; ok {
			md = d
		}
		res.Roots = append(res.Roots, r)
		queue = append(queue, item{path: r, depth: 0, maxDepth: md, isRoot: true})
	}

	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if visited[it.path] {
			continue
		}
		visited[it.path] = true
		if isExcluded(it.path, exclude) {
			continue
		}
		if isGoToolchain(it.path) {
			continue
		}
		if res.DirsVisited >= opt.MaxDirs {
			addPartial(res, "文件夹数量到上限")
			break
		}
		if time.Now().After(deadline) {
			addPartial(res, "扫描超时")
			break
		}
		res.DirsVisited++
		entries, err := os.ReadDir(it.path)
		if err != nil {
			res.Unreadable++
			continue
		}

		isContainer := containers[it.path]
		if !isContainer {
			if p, ok := detectProject(it.path, entries, it.isRoot); ok {
				fillProject(p, entries, opt)
				res.Projects = append(res.Projects, *p)
				continue // 项目内部不再往下扫
			}
		}

		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(it.path, name)
			if e.Type()&os.ModeSymlink != 0 {
				res.SymlinksSkipped++
				continue
			}
			if e.IsDir() {
				if skipDir(name, extraSkip) || isExcluded(full, exclude) {
					continue
				}
				if it.depth+1 <= it.maxDepth {
					queue = append(queue, item{path: full, depth: it.depth + 1, maxDepth: it.maxDepth})
				}
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".icloud") || strings.HasPrefix(name, "~$") {
				if IsSensitiveName(name) {
					res.SensitiveSkipped++
				}
				continue
			}
			if IsSensitiveName(name) {
				res.SensitiveSkipped++
				continue
			}
			if len(res.Loose) >= opt.MaxFiles {
				addPartial(res, "散落文件数量到上限")
				continue
			}
			fi, err := e.Info()
			if err != nil {
				res.Unreadable++
				continue
			}
			res.Loose = append(res.Loose, LooseFile{
				Path: full, Size: fi.Size(), ModTime: fi.ModTime(), Category: FileCategory(name),
				Device: deviceOf(fi), Group: -1,
			})
		}
	}

	if opt.GitStatus {
		for i := range res.Projects {
			if g := res.Projects[i].Git; g != nil {
				checkStatus(g, res.Projects[i].Path, opt.GitTimeout)
			}
		}
	}
	groupProjects(res)
	matchLoose(res)
	return res, nil
}

func addPartial(res *Result, reason string) {
	res.Partial = true
	for _, r := range res.PartialReasons {
		if r == reason {
			return
		}
	}
	res.PartialReasons = append(res.PartialReasons, reason)
}

func isExcluded(p string, exclude []string) bool {
	for _, e := range exclude {
		if p == e || strings.HasPrefix(p, e+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// SkipDirName 说这个目录名是不是扫描时默认跳过的（点目录、依赖、缓存、系统目录等）。
func SkipDirName(name string) bool { return skipDir(name, nil) }

func isGoToolchain(dir string) bool {
	if !strings.HasPrefix(filepath.Base(dir), "go1.") {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "go")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "src", "runtime", "runtime.go"))
	return err == nil
}

func skipDir(name string, extra map[string]bool) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	ln := strings.ToLower(name)
	if skipNames[ln] || extra[ln] {
		return true
	}
	for _, s := range skipSuffixes {
		if strings.HasSuffix(ln, s) {
			return true
		}
	}
	return false
}

// detectProject 看一个文件夹的顶层条目，判断它是不是项目根。
// DetectProjectAt uses the same recognition rules for a known working directory.
func DetectProjectAt(dir string) (*Project, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	project, ok := detectProject(dir, entries, true)
	if !ok {
		return nil, nil
	}
	fillProject(project, entries, Options{})
	return project, nil
}

func detectProject(dir string, entries []os.DirEntry, isRoot bool) (*Project, bool) {
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	var markers []string
	strong := false
	for _, m := range strongMarkers {
		if names[m] {
			markers = append(markers, m)
			strong = true
		}
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".xcodeproj") || strings.HasSuffix(e.Name(), ".xcworkspace") {
			markers = append(markers, "*.xcodeproj")
			strong = true
			break
		}
	}
	for _, m := range weakMarkers {
		if names[m] {
			markers = append(markers, m)
		}
	}
	category := ""
	if len(markers) > 0 {
		category = "code"
		if !strong && len(markers) == 1 && markers[0] == "00-说明.md" {
			category = "doc"
		}
	}
	if category == "" {
		images := 0
		documents, businessHint := 0, false
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if e.Type().IsRegular() {
				switch ext {
				case ".psd", ".psb", ".ai", ".fig", ".sketch", ".blend", ".c4d":
					markers = append(markers, "设计源文件"+ext)
					category = "design"
					strong = true
				}
				switch ext {
				case ".doc", ".docx", ".pdf", ".md", ".txt", ".xlsx", ".csv":
					documents++
				}
				lower := strings.ToLower(e.Name())
				for _, hint := range []string{"需求", "方案", "合同", "预算", "brief", "proposal"} {
					if strings.Contains(lower, hint) {
						businessHint = true
					}
				}
			}
			if mediaProjectExts[ext] {
				markers = append(markers, "剪辑工程"+ext)
				category = "media"
				break
			}
			if e.Type().IsRegular() && imageExts[ext] {
				images++
			}
		}
		if category == "" && images >= MinImageGroup {
			markers = append(markers, "图片组")
			category = "images"
		}
		if category == "" && documents >= 2 && businessHint {
			markers = append(markers, "项目业务文档")
			category = "doc"
			strong = true
		}
	}
	if len(markers) == 0 {
		return nil, false
	}
	if isRoot && !strong && category != "media" && category != "images" {
		// 扫描根本身只有弱标志（例如主目录里有 CLAUDE.md）时不算项目。
		return nil, false
	}
	name := filepath.Base(dir)
	return &Project{Path: dir, Name: name, BaseName: StripVersion(name), Markers: markers, Category: category, Group: -1}, true
}

func fillProject(p *Project, entries []os.DirEntry, opt Options) {
	p.top = map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if n == ".git" || n == ".DS_Store" {
			continue
		}
		p.top[n] = true
		if fi, err := e.Info(); err == nil && fi.ModTime().After(p.ModTime) {
			p.ModTime = fi.ModTime()
		}
	}
	p.TopEntries = len(p.top)
	if fi, err := os.Stat(p.Path); err == nil {
		p.Device = deviceOf(fi)
		if p.ModTime.IsZero() {
			p.ModTime = fi.ModTime()
		}
	}
	if g, ok := readGit(p.Path); ok {
		p.Git = g
	}
}

// FileCategory 按扩展名给散落文件分大类。
func FileCategory(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case imageExts[ext] || ext == ".svg" || ext == ".psd" || ext == ".ai" || ext == ".sketch" || ext == ".fig":
		return "图片"
	case ext == ".mp4" || ext == ".mov" || ext == ".mkv" || ext == ".avi" || ext == ".webm" || ext == ".m4v":
		return "视频"
	case ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".aac" || ext == ".flac" || ext == ".ogg":
		return "音频"
	case ext == ".md" || ext == ".txt" || ext == ".doc" || ext == ".docx" || ext == ".pdf" || ext == ".pages" || ext == ".rtf" || ext == ".epub":
		return "文稿"
	case ext == ".xls" || ext == ".xlsx" || ext == ".csv" || ext == ".numbers" || ext == ".tsv":
		return "表格"
	case ext == ".ppt" || ext == ".pptx" || ext == ".key":
		return "演示"
	case ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".tgz" || ext == ".7z" || ext == ".rar" || ext == ".dmg":
		return "压缩包"
	case ext == ".html" || ext == ".htm" || ext == ".css" || ext == ".js" || ext == ".ts" || ext == ".py" || ext == ".go" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".sh" || ext == ".sql":
		return "代码与配置"
	case strings.Contains(strings.ToLower(name), ".bak"):
		return "备份"
	}
	return "其他"
}

// SortedTop 返回项目顶层条目名（测试和计划用）。
func (p *Project) SortedTop() []string {
	out := make([]string, 0, len(p.top))
	for k := range p.top {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
