package allocate

import (
	"path/filepath"
	"strings"
)

// 文件大类（决定往哪一类栏位靠）。
const (
	KindCode  = "代码"
	KindImage = "图片"
	KindVideo = "视频"
	KindAudio = "音频"
	KindDoc   = "文档"
	KindSheet = "表格"
	KindWeb   = "网页"
	KindOther = "其他"
)

// 代码/清单标志（目录或文件名）。
var codeMarkers = []string{
	"go.mod", "package.json", "pyproject.toml", "requirements.txt", "Cargo.toml",
	"pom.xml", "build.gradle", "build.gradle.kts", "Gemfile", "composer.json",
	"Package.swift", "CMakeLists.txt", "Makefile", "docker-compose.yml", "docker-compose.yaml",
}

var codeExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".rs": true, ".java": true, ".kt": true, ".swift": true, ".c": true, ".cc": true,
	".cpp": true, ".h": true, ".hpp": true, ".cs": true, ".rb": true, ".php": true, ".scala": true,
	".vue": true, ".svelte": true, ".dart": true, ".lua": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".proto": true, ".toml": true, ".yaml": true, ".yml": true, ".json": true,
}

var imageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".bmp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".svg": true, ".psd": true,
	".ai": true, ".fig": true, ".sketch": true, ".raw": true, ".cr2": true, ".nef": true,
}

var videoExt = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true, ".webm": true, ".m4v": true,
	".wmv": true, ".flv": true, ".m4p": true, ".prproj": true, ".aep": true, ".fcpxml": true,
}

var audioExt = map[string]bool{
	".mp3": true, ".wav": true, ".aac": true, ".flac": true, ".m4a": true, ".ogg": true,
	".aiff": true, ".wma": true, ".opus": true,
}

var docExt = map[string]bool{
	".md": true, ".txt": true, ".pdf": true, ".doc": true, ".docx": true, ".rtf": true,
	".pages": true, ".odt": true, ".epub": true, ".html": true, ".htm": true,
}

var sheetExt = map[string]bool{
	".xlsx": true, ".xls": true, ".csv": true, ".tsv": true, ".numbers": true, ".ods": true,
}

var webExt = map[string]bool{
	".html": true, ".htm": true, ".css": true,
}

// KindOfPath 看扩展名猜文件大类。
func KindOfPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))
	for _, m := range codeMarkers {
		if base == strings.ToLower(m) {
			return KindCode
		}
	}
	switch {
	case imageExt[ext]:
		return KindImage
	case videoExt[ext]:
		return KindVideo
	case audioExt[ext]:
		return KindAudio
	case sheetExt[ext]:
		return KindSheet
	case webExt[ext]:
		return KindWeb
	case docExt[ext]:
		return KindDoc
	case codeExt[ext]:
		return KindCode
	}
	return KindOther
}

// TemplateForKind 大类 → 模板编号偏好。
func TemplateForKind(kind string) []string {
	switch kind {
	case KindImage:
		return []string{"T01"}
	case KindVideo:
		return []string{"T02"}
	case KindAudio:
		return []string{"T12"}
	case KindDoc:
		return []string{"T03", "T06", "T07", "T10", "T11"}
	case KindSheet:
		return []string{"T04", "T05", "T08"}
	case KindCode:
		return []string{"T09"}
	case KindWeb:
		return []string{"T13", "T01"}
	}
	return nil
}

func isCodeMarkerName(name string) bool {
	n := strings.ToLower(name)
	if n == ".git" {
		return true
	}
	for _, m := range codeMarkers {
		if n == strings.ToLower(m) {
			return true
		}
	}
	return false
}
