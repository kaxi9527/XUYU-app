package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// HomeJob 是前端传来的一条归位任务。
type HomeJob struct {
	Source  string `json:"source"`
	Project string `json:"project"`
	Area    string `json:"area"`
	Task    string `json:"task,omitempty"`
	Whole   bool   `json:"whole,omitempty"`
}

// PreviewHome 预览归位（不改文件）。
func (s *ShelfService) PreviewHome(jobs []HomeJob) (home.Summary, error) {
	opt, err := s.homeOpt(jobs, true)
	if err != nil {
		return home.Summary{}, err
	}
	sum, err := home.Preview(opt)
	if err != nil {
		return home.Summary{}, err
	}
	return *sum, nil
}

// ApplyHome 执行归位（前端必须先 Preview 并让用户确认）。
func (s *ShelfService) ApplyHome(jobs []HomeJob) (home.Summary, error) {
	opt, err := s.homeOpt(jobs, false)
	if err != nil {
		return home.Summary{}, err
	}
	sum, err := home.Apply(opt)
	if err != nil {
		return home.Summary{}, err
	}
	return *sum, nil
}

// PreviewHomeProject 预览把一个原地项目整棵搬进总文件夹。
func (s *ShelfService) PreviewHomeProject(id string) (home.Summary, error) {
	jobs, err := s.placeJobs(id)
	if err != nil {
		return home.Summary{}, err
	}
	return s.PreviewHome(jobs)
}

// ApplyHomeProject 执行整项目归位。
func (s *ShelfService) ApplyHomeProject(id string) (home.Summary, error) {
	jobs, err := s.placeJobs(id)
	if err != nil {
		return home.Summary{}, err
	}
	return s.ApplyHome(jobs)
}

// PreviewHomeItems 按首页条目（放错位置/项目外/原处未归位等）预览归位。
func (s *ShelfService) PreviewHomeItems(paths []string) (home.Summary, error) {
	jobs, err := s.jobsFromPaths(paths)
	if err != nil {
		return home.Summary{}, err
	}
	return s.PreviewHome(jobs)
}

// ApplyHomeItems 执行按路径归位。
func (s *ShelfService) ApplyHomeItems(paths []string) (home.Summary, error) {
	jobs, err := s.jobsFromPaths(paths)
	if err != nil {
		return home.Summary{}, err
	}
	return s.ApplyHome(jobs)
}

// UndoHome 按撤销清单恢复。
func (s *ShelfService) UndoHome(undoPath string) (home.UndoResult, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return home.UndoResult{}, err
	}
	ur, err := home.Undo(cfg, undoPath)
	if err != nil {
		return home.UndoResult{}, err
	}
	return *ur, nil
}

func (s *ShelfService) homeOpt(jobs []HomeJob, dry bool) (home.Options, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return home.Options{}, err
	}
	if len(jobs) == 0 {
		return home.Options{}, fmt.Errorf("没有要归位的文件")
	}
	var hs []home.Job
	for _, j := range jobs {
		src := filepath.Clean(strings.TrimSpace(j.Source))
		if !filepath.IsAbs(src) {
			return home.Options{}, fmt.Errorf("源路径必须是绝对路径：%s", j.Source)
		}
		if err := put.CheckName("项目名", j.Project); err != nil {
			return home.Options{}, err
		}
		hs = append(hs, home.Job{Source: src, Project: j.Project, Area: j.Area, Task: j.Task, Whole: j.Whole})
	}
	return home.Options{Config: cfg, Jobs: hs, DryRun: dry}, nil
}

func (s *ShelfService) placeJobs(id string) ([]HomeJob, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	p := e.Reg.ByID(id)
	if p == nil {
		return nil, os.ErrNotExist
	}
	var out []HomeJob
	for _, j := range home.JobsFromPlaceProject(*p) {
		out = append(out, HomeJob{Source: j.Source, Project: j.Project, Area: j.Area, Task: j.Task, Whole: j.Whole})
	}
	return out, nil
}

func (s *ShelfService) jobsFromPaths(paths []string) ([]HomeJob, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	// 用 Search 空词不行；按路径逐个 describe：走 Recent 大窗口太重。直接用 registry Owner + 路径推断。
	var items []shelf.Item
	for _, p := range paths {
		p = filepath.Clean(p)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		it := shelf.Item{Path: p, Name: filepath.Base(p), Dir: filepath.Dir(p), Size: fi.Size()}
		if o := ownerOf(e.Reg, p); o != nil {
			it.Project, it.ProjectID, it.InPlace = o.Name, o.ID, true
			it.Placement = shelf.PlacedInPlace
		} else {
			// 尝试看是不是总文件夹内未归类
			rel, err := filepath.Rel(e.Config.Root, p)
			if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				parts := strings.Split(rel, string(filepath.Separator))
				if len(parts) > 0 && parts[0] != e.Config.Names.Control {
					it.Project = parts[0]
					it.Placement = shelf.PlacedLoose
				}
			}
			if it.Placement == "" {
				it.Placement = shelf.PlacedWrong
			}
		}
		items = append(items, it)
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	// 有项目上下文时按工种分配，不再默认全塞散落
	jobs, skipped := allocate.JobsForItems(cfg, items)
	if len(jobs) == 0 {
		msg := "没有可归位的文件"
		if len(skipped) > 0 {
			msg += "：" + strings.Join(skipped[:min(3, len(skipped))], "；")
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var out []HomeJob
	for _, j := range jobs {
		out = append(out, HomeJob{Source: j.Source, Project: j.Project, Area: j.Area, Task: j.Task, Whole: j.Whole})
	}
	_ = skipped
	return out, nil
}

func ownerOf(r *registry.Registry, path string) *registry.Project {
	if r == nil {
		return nil
	}
	return r.Owner(path)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
