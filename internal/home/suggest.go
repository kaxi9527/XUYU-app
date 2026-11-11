package home

import (
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// JobsFromItems 把首页/搜索里「不在规定位置」的文件收成归位任务。
// 默认进所属项目的 资料/散落文件；没有项目名的跳过（调用方应先让用户指定）。
// 桌面一键归位请优先用 allocate.JobsForItems（按工种分到任务/成品/代码等）。
func JobsFromItems(items []shelf.Item) (jobs []Job, skipped []string) {
	seen := map[string]bool{}
	for _, it := range items {
		switch it.Placement {
		case shelf.PlacedWrong, shelf.PlacedOutside, shelf.PlacedLoose, shelf.PlacedInPlace:
		default:
			skipped = append(skipped, it.Path+"（已在规定位置："+it.Placement+"）")
			continue
		}
		if it.Project == "" {
			skipped = append(skipped, it.Path+"（不知道属于哪个项目，请先指定）")
			continue
		}
		if seen[it.Path] {
			continue
		}
		seen[it.Path] = true
		// 原处未归位的单个文件：仍按散落进总文件夹对应项目；整项目请用 JobsFromPlaceProject
		jobs = append(jobs, Job{Source: it.Path, Project: it.Project, Area: AreaLoose})
	}
	return jobs, skipped
}

// JobsFromPlaceProject 整项目归位：主版本文件夹搬进总文件夹；
// 不在主版本目录下的成员（副本/zip/相关文件）进 资料/散落文件。
func JobsFromPlaceProject(p registry.Project) []Job {
	var jobs []Job
	jobs = append(jobs, Job{Source: p.Path, Project: p.Name, Whole: true})
	main := filepath.Clean(p.Path) + string(filepath.Separator)
	for _, m := range p.Members {
		if m.Path == "" || m.Path == p.Path {
			continue
		}
		cp := filepath.Clean(m.Path)
		if cp == filepath.Clean(p.Path) || strings.HasPrefix(cp+string(filepath.Separator), main) {
			continue // 已在主版本树里，跟着整项目走
		}
		jobs = append(jobs, Job{Source: cp, Project: p.Name, Area: AreaLoose})
	}
	return jobs
}
