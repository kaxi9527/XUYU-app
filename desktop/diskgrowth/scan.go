package diskgrowth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Bucket struct {
	Category string `json:"category"`
	Path     string `json:"path"`
	Project  string `json:"project,omitempty"`
	App      string `json:"app,omitempty"`
	Bytes    int64  `json:"bytes"`
}
type Issue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type Sample struct {
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
	Trigger    string    `json:"trigger"`
	Planned    string    `json:"planned,omitempty"`
	Root       string    `json:"root"`
	Free       uint64    `json:"free"`
	Total      uint64    `json:"total"`
	Files      int64     `json:"files"`
	Buckets    []Bucket  `json:"buckets,omitempty"`
	Issues     []Issue   `json:"issues,omitempty"`
	IssueCount int       `json:"issueCount"`
}
type Scope struct {
	Root, Home, HistoryDir string
	Projects               map[string]string
}
type identity struct{ Device, Inode uint64 }
type group struct{ Category, Path, Project, App string }
type owner struct {
	Group group
	Bytes int64
}

// Scan records directory totals. Symlinks and mounted volumes are not traversed.
func Scan(ctx context.Context, scope Scope, started time.Time, trigger, planned string) (Sample, error) {
	root := scope.Root
	sample := Sample{Started: started, Trigger: trigger, Planned: planned, Root: root}
	info, err := os.Stat(root)
	if err != nil {
		return sample, err
	}
	device := deviceID(info)
	totals := map[group]int64{}
	links := map[identity]owner{}
	reported := map[Issue]bool{}
	issue := func(path string, err error) {
		reason := "读取失败"
		if errors.Is(err, fs.ErrPermission) {
			reason = "未授权读取"
		} else if errors.Is(err, fs.ErrNotExist) {
			return
		}
		sample.IssueCount++
		problem := Issue{path, reason}
		if !reported[problem] {
			sample.Issues = append(sample.Issues, problem)
			reported[problem] = true
		}
	}
	var walk func(string, group) error
	walk = func(path string, g group) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			if path == root {
				return err
			}
			issue(path, err)
			return nil
		}
		if name, ok := scope.Projects[path]; ok && g.Category != "共享包缓存" {
			g = group{"项目文件", path, name, ""}
		}
		i := sort.Search(len(entries), func(i int) bool { return entries[i].Name() >= ".git" })
		if i < len(entries) && entries[i].Name() == ".git" && entries[i].Type()&os.ModeSymlink == 0 && g.Category != "共享包缓存" && g.Category != "项目依赖与虚拟环境" {
			g.Category = "Git 工作树"
			g.Path = path
			if g.Project == "" {
				g.Project = filepath.Base(path)
			}
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			child := filepath.Join(path, name)
			if child == scope.HistoryDir {
				continue
			}
			stat, err := entry.Info()
			if err != nil {
				issue(path, err)
				continue
			}
			if deviceID(stat) != device {
				continue
			}
			if stat.IsDir() {
				if err := walk(child, classifyDir(scope, path, name, g)); err != nil {
					return err
				}
				continue
			}
			if !stat.Mode().IsRegular() {
				continue
			}
			bytes, id, multi, err := fileUsage(child, stat)
			if err != nil {
				issue(path, err)
				continue
			}
			key := g
			if key.Category == "其他" || key.Category == "文档与媒体" || key.Category == "项目文件" {
				switch strings.ToLower(filepath.Ext(name)) {
				case ".log":
					key.Category = "日志"
				case ".bak", ".backup":
					key.Category = "备份"
				case ".pdf", ".txt", ".md", ".doc", ".docx", ".xlsx", ".pptx", ".jpg", ".jpeg", ".png", ".heic", ".mp4", ".mov", ".wav", ".mp3":
					if key.Category == "其他" {
						key.Category = "文档与媒体"
					}
				}
			}
			if multi {
				previous, ok := links[id]
				if ok {
					// A dependency can hard-link to a shared package store. Charge that store once regardless of traversal order.
					if key.Category != "共享包缓存" || previous.Group.Category == "共享包缓存" {
						continue
					}
					totals[previous.Group] -= previous.Bytes
					sample.Files--
				}
				links[id] = owner{key, bytes}
			}
			totals[key] += bytes
			sample.Files++
		}
		return nil
	}
	if err := walk(root, group{"其他", root, "", ""}); err != nil {
		return sample, fmt.Errorf("采样失败: %w", err)
	}
	sample.Free, sample.Total, err = Capacity(root)
	if err != nil {
		return sample, err
	}
	sample.Finished = time.Now()
	for k, v := range totals {
		if v != 0 {
			sample.Buckets = append(sample.Buckets, Bucket{k.Category, k.Path, k.Project, k.App, v})
		}
	}
	sort.Slice(sample.Buckets, func(i, j int) bool {
		a, b := sample.Buckets[i], sample.Buckets[j]
		if a.Path == b.Path {
			return a.Category < b.Category
		}
		return a.Path < b.Path
	})
	return sample, nil
}
