package diskgrowth

import (
	"path/filepath"
	"strings"
)

func classifyDir(scope Scope, parent, name string, g group) group {
	path := filepath.Join(parent, name)
	n := strings.ToLower(name)
	p := strings.ToLower(filepath.ToSlash(path)) + "/"
	set := func(category string) group { g.Category = category; g.Path = path; return g }
	if parent == scope.Root || parent == scope.Home || g.Path == scope.Home || parent == filepath.Join(scope.Home, "Projects") || parent == filepath.Join(scope.Home, "Work") {
		g.Path = path
	}
	if g.Category == "共享包缓存" {
		return g
	}
	if parent == g.Path && filepath.Dir(parent) == scope.Home && (g.Category == "下载" || g.Category == "文档与媒体") {
		g.Path = path
	}
	switch {
	case strings.HasSuffix(p, "/.npm/"), strings.HasSuffix(p, "/.pnpm-store/"), strings.HasSuffix(p, "/pnpm/store/"), strings.HasSuffix(p, "/go/pkg/mod/"), strings.HasSuffix(p, "/.cargo/registry/"), strings.HasSuffix(p, "/caches/uv/"), strings.HasSuffix(p, "/.cache/uv/"), strings.HasSuffix(p, "/caches/pip/"), strings.HasSuffix(p, "/.cache/pip/"), strings.HasSuffix(p, "/.gradle/caches/"), strings.HasSuffix(p, "/.m2/repository/"), strings.HasSuffix(p, "/npm-cache/"), strings.HasSuffix(p, "/pip/cache/"), strings.HasSuffix(p, "/uv/cache/"), strings.HasSuffix(p, "/cache/apt/"), strings.HasSuffix(p, "/caches/homebrew/"):
		g.Project = ""
		return set("共享包缓存")
	case g.Category == "项目依赖与虚拟环境" || g.Category == "构建与测试产物":
		return g
	case n == "node_modules" || n == ".venv" || n == "venv" || n == "site-packages" || n == "vendor":
		return set("项目依赖与虚拟环境")
	case n == "dist" || n == "build" || n == "target" || n == "coverage" || n == ".next" || n == "deriveddata" || n == "go-build" || n == "test-results" || n == ".pytest_cache":
		return set("构建与测试产物")
	case n == ".codex" || n == ".claude" || n == ".gemini" || n == ".cursor" || n == ".agents" || n == ".ai-output":
		g.App = strings.TrimPrefix(name, ".")
		return set("AI 工具数据")
	case g.Category == "AI 工具数据" && (n == "sessions" || n == "projects" || n == "outputs" || n == "memories"):
		g.Path = path
		return g
	case parent == scope.Home && n == "downloads":
		return set("下载")
	case strings.HasSuffix(n, ".app"):
		g.App = name
		return set("应用数据")
	case n == "backups" || n == "backup" || n == "备份" || n == "backups.backupdb":
		return set("备份")
	case n == "logs" || strings.HasSuffix(p, "/var/log/"):
		return set("日志")
	case n == "tmp" || n == "temp" || strings.HasSuffix(p, "/var/folders/"):
		return set("临时文件")
	case n == "caches" || n == ".cache" || strings.HasSuffix(p, "/var/cache/"):
		return set("系统与应用缓存")
	case strings.HasSuffix(p, "/library/application support/") || strings.HasSuffix(p, "/appdata/local/") || strings.HasSuffix(p, "/appdata/roaming/") || strings.HasSuffix(p, "/.local/share/") || strings.HasSuffix(p, "/library/containers/"):
		return set("应用数据")
	case g.Category == "应用数据" && g.App == "" && g.Path == parent:
		g.App = name
		g.Path = path
		return g
	case g.Category == "系统与应用缓存" && g.Path == parent && g.App == "":
		g.Path = path
		g.App = name
		return g
	case parent == scope.Home && (n == "desktop" || n == "documents" || n == "pictures" || n == "movies" || n == "music" || n == "videos"):
		return set("文档与媒体")
	}
	return g
}
