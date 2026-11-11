package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// OrganizeChooseSources only returns the user's native dialog selection.
// Selection is not permission to move files; the existing preview/apply boundary remains.
func (s *ShelfService) OrganizeChooseSources() ([]string, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("文件选择器尚未就绪，请稍后重试")
	}
	return app.Dialog.OpenFile().CanChooseFiles(true).CanChooseDirectories(true).
		SetTitle("选择要整理的文件或文件夹").SetMessage("可以多选；这里只添加来源，不会移动文件。").PromptForMultipleSelection()
}

type OrganizeSuggestion struct {
	Project  string              `json:"project"`
	Inferred []allocate.Inferred `json:"inferred"`
}

func (s *ShelfService) OrganizeSuggest(sources []string) (OrganizeSuggestion, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return OrganizeSuggestion{}, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return OrganizeSuggestion{}, err
	}
	return suggestSources(cfg, userHome, sources)
}

func suggestSources(cfg config.Config, userHome string, sources []string) (OrganizeSuggestion, error) {
	out := OrganizeSuggestion{Inferred: []allocate.Inferred{}}
	if len(sources) == 0 || len(sources) > 64 {
		return out, errors.New("请选择 1–64 个文件或文件夹")
	}
	seen := map[string]bool{}
	for i, raw := range sources {
		path := filepath.Clean(raw)
		if strings.HasPrefix(raw, "~/") {
			path = filepath.Join(userHome, raw[2:])
		}
		if !filepath.IsAbs(path) {
			return out, errors.New("来源路径需要是绝对路径")
		}
		if home.CheckPath(userHome, path) != nil && home.CheckPath(cfg.Root, path) != nil {
			return out, errors.New("请选择个人文件夹或序屿总目录内的文件，链接路径暂不支持")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return out, err
		}
		if i == 0 {
			out.Project = info.Name()
			if !info.IsDir() {
				out.Project = strings.TrimSuffix(out.Project, filepath.Ext(out.Project))
			}
		}
		if i >= 32 {
			continue
		}
		// Hidden configuration is browsable, but is not evidence for automatic organization.
		hidden := false
		for _, part := range strings.Split(path, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				hidden = true
			}
		}
		if hidden {
			continue
		}
		var hints []allocate.Inferred
		if info.IsDir() {
			hints = allocate.InferProject(allocate.InferOptions{ProjectDir: path, ReadmeName: cfg.Names.Readme, MaxEntries: 400})
		} else {
			label := ""
			switch strings.ToLower(filepath.Ext(path)) {
			case ".fig", ".sketch":
				label = "设计·UI原型"
			case ".prproj", ".aep", ".drp", ".fcpxml":
				label = "视频剪辑·剪辑成片"
			case ".blend", ".c4d", ".max":
				label = "设计·3D模型渲染"
			case ".go", ".ts", ".tsx", ".py", ".rs", ".swift":
				label = "开发·功能开发"
			}
			if label != "" {
				hints = append(hints, allocate.Inferred{Label: label, Source: allocate.SrcInfer, Confidence: allocate.ConfHigh, PreConfirm: true, Reason: "包含对应的可编辑源文件"})
			}
		}
		for _, hint := range hints {
			if !seen[hint.Label] {
				out.Inferred = append(out.Inferred, hint)
				seen[hint.Label] = true
			}
		}
	}
	return out, nil
}
