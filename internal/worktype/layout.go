package worktype

// Layout binds the existing work-type templates to real project folders.
// Roles describe workflow stages, rather than grouping everything by extension.
type Layout struct {
	Name    string            `json:"name"`
	Folders []string          `json:"folders"`
	Roles   map[string]string `json:"roles"`
}

func layout(name string, pairs ...string) Layout {
	l := Layout{Name: name, Roles: map[string]string{}}
	seen := map[string]bool{}
	for i := 0; i+1 < len(pairs); i += 2 {
		l.Roles[pairs[i]] = pairs[i+1]
		if !seen[pairs[i+1]] {
			l.Folders = append(l.Folders, pairs[i+1])
			seen[pairs[i+1]] = true
		}
	}
	return l
}

// LayoutFor returns a fresh layout, so callers cannot mutate the shared catalog.
func LayoutFor(template string) Layout {
	switch template {
	case "T01":
		return layout("设计", "brief", "需求", "reference", "参考素材", "source", "源文件", "draft", "过程稿", "delivery", "交付稿")
	case "T02":
		return layout("视频", "brief", "需求与脚本", "reference", "原始素材", "source", "剪辑工程", "subtitle", "字幕", "audio", "音频", "draft", "预览稿", "delivery", "成片")
	case "T09", "T13", "T14", "T15":
		return layout("开发", "brief", "需求文档", "reference", "参考资料", "source", "代码工程", "draft", "设计与测试", "delivery", "交付说明")
	case "T04", "T05", "T08":
		return layout("数据与业务", "brief", "需求与口径", "reference", "原始资料", "source", "数据源", "draft", "工作底稿", "delivery", "结果报表")
	case "T11":
		return layout("研究", "brief", "研究计划", "reference", "参考文献", "source", "原始数据", "draft", "分析过程", "delivery", "研究成果")
	case "T12":
		return layout("音频", "brief", "文稿与需求", "reference", "参考音频", "source", "录音工程", "audio", "原始录音", "draft", "试听稿", "delivery", "成品音频")
	default:
		return layout("文档", "brief", "需求与大纲", "reference", "参考资料", "source", "源材料", "draft", "草稿", "delivery", "定稿")
	}
}
