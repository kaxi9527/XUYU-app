package organize

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func exists(path string) bool { _, e := os.Lstat(path); return e == nil }
func protected(dir string) string {
	if strings.EqualFold(filepath.Ext(dir), ".app") {
		return "应用程序包保留原位，避免拆散或影响运行"
	}
	if exists(filepath.Join(dir, ".git")) {
		return "Git 仓库保留原位，避免影响工作树和运行环境；周边资料可以归到同一项目"
	}
	for _, name := range []string{"node_modules", ".venv", "venv"} {
		if exists(filepath.Join(dir, name)) {
			return "含依赖环境的工程保留原位，避免破坏运行路径"
		}
	}
	return ""
}
func bundleKind(dir string) string {
	for _, n := range []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml", "Package.swift", "Makefile"} {
		if exists(filepath.Join(dir, n)) {
			return "code-bundle"
		}
	}
	f, e := os.Open(dir)
	if e != nil {
		return ""
	}
	defer f.Close()
	ents, _ := f.ReadDir(5001)
	for _, d := range ents {
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".prproj", ".aep", ".drp", ".fcpxml", ".fcpbundle":
			return "video-bundle"
		case ".als", ".logicx", ".band":
			return "audio-bundle"
		}
	}
	return ""
}

func fileRole(path string) (string, bool) {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(name))
	parent := strings.ToLower(filepath.Base(filepath.Dir(path)))
	if strings.HasSuffix(name, ".bak") || strings.Contains(name, ".bak-") || strings.HasSuffix(name, ".tmp") {
		return "backup", false
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(name, w) || strings.Contains(parent, w) {
				return true
			}
		}
		return false
	}
	// Editable sources remain sources even when named final.psd.
	switch ext {
	case ".log":
		return "runtime-log", false
	case ".psd", ".psb", ".ai", ".fig", ".sketch", ".blend", ".c4d", ".max", ".fbx", ".indd":
		return "source", true
	case ".srt", ".vtt", ".ass":
		return "subtitle", true
	}
	if has("需求", "brief", "任务书", "大纲", "storyboard", "requirement", "prd") {
		return "brief", true
	}
	if has("参考", "素材", "reference", "assets", "原始") {
		return "reference", true
	}
	if has("交付", "定稿", "成片", "final", "approved", "export", "交付稿") {
		return "delivery", true
	}
	if has("草稿", "过程", "预览", "draft", "wip", "preview") {
		return "draft", true
	}
	switch ext {
	case ".wav", ".mp3", ".aac", ".flac", ".m4a":
		return "audio", false
	case ".png", ".jpg", ".jpeg", ".webp", ".svg", ".mp4", ".mov", ".pdf":
		return "reference", false
	case ".docx", ".doc", ".md", ".txt", ".csv", ".xlsx", ".xls":
		return "draft", false
	}
	return "", false
}

// Automatic placement is a filing decision, not a claim that an unknown file is
// a finished deliverable. Prefer the selected work type's working/source folder.
// Only a custom template with no usable role needs a grouped user decision.
func automaticFolder(layouts []worktype.Layout, path, role string, confident bool) (string, string) {
	if folder, reason := chooseFolder(layouts, path, role); folder != "" {
		if !confident && role != "runtime-log" && role != "backup" {
			reason = "按文件类型自动归档；可在整理前调整"
		}
		return folder, reason
	}
	if strings.HasSuffix(role, "-bundle") {
		role = "source"
	}
	for _, l := range layouts {
		if role != "" && l.Roles[role] != "" {
			return l.Roles[role], "按主要工种自动归档"
		}
		for _, fallback := range []string{"source", "reference", "draft", "brief"} {
			if l.Roles[fallback] != "" {
				return l.Roles[fallback], "按主要工种归入工作资料，不判断为交付成果"
			}
		}
		if len(l.Folders) == 1 {
			return l.Folders[0], "归入你设置的唯一目录"
		}
	}
	return "", "自定义模板尚未指定这类文件的归档目录"
}

func chooseFolder(layouts []worktype.Layout, path, role string) (string, string) {
	logFile := role == "runtime-log"
	backup := role == "backup"
	if logFile || backup {
		role = "draft"
	}
	if strings.HasSuffix(role, "-bundle") {
		name := map[string]string{"code-bundle": "开发", "video-bundle": "视频", "audio-bundle": "音频"}[role]
		for _, l := range layouts {
			if l.Name == name {
				return l.Roles["source"], "完整保留工程及内部相对路径"
			}
		}
		return "", "请补充与工程相符的工种"
	}
	if role == "" {
		return "", "用途不明确"
	}
	var candidates []string
	for _, l := range layouts {
		f := l.Roles[role]
		if f != "" && !contains(candidates, f) {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 1 {
		if logFile {
			return candidates[0], "运行日志默认保留原处，可自行选择是否归档"
		}
		if backup {
			return candidates[0], "备份和临时文件默认保留原处，可自行选择是否归档"
		}
		return candidates[0], "按所选工种和文件用途归类"
	}
	if len(candidates) > 1 {
		return "", "多个工种对应不同目录，请指定用途"
	}
	return "", "所选工种中没有对应目录，请指定用途"
}
