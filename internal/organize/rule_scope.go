package organize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/scan"
)

type ruleDirectoryState struct {
	path string
	info fs.FileInfo
}
type placementScope struct {
	expand      map[string]bool
	blocked     map[string]string
	directories map[string][]ruleDirectoryState
}

func inspectPlacementScope(ctx context.Context, roots []string, req Request, target, readme string, deadline time.Time) (*placementScope, error) {
	scope := &placementScope{expand: map[string]bool{}, blocked: map[string]string{}, directories: map[string][]ruleDirectoryState{}}
	markParents := func(path, root string) {
		for parent := filepath.Dir(path); in(parent, root); parent = filepath.Dir(parent) {
			scope.expand[parent] = true
			if parent == root {
				break
			}
		}
	}
	// Manual decisions already identify their exact files. They do not require
	// scanning otherwise unrelated folders.
	for _, root := range roots {
		for path := range req.Folders {
			if in(path, root) {
				markParents(path, root)
			}
		}
	}
	hasRules := req.Profile != nil && len(req.Profile.Rules) > 0
	entries := 0
	limited := false
	limitErr := errors.New("来源检查达到 5000 条目或 15 秒上限，请缩小来源范围")
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if limited {
			scope.blocked[root] = limitErr.Error()
			continue
		}
		// Hidden/protected sources are reported by the normal planner. Do not read
		// their descendants to evaluate ordinary placement rules.
		hidden := false
		for _, part := range strings.Split(root, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") && part != "." && part != ".." {
				hidden = true
			}
		}
		if hidden {
			continue
		}
		var dirs []ruleDirectoryState
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if err != nil {
				return err
			}
			if d.IsDir() || hasRules {
				entries++
			}
			if entries > 5000 || time.Now().After(deadline) {
				limited = true
				return limitErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() && protected(path) != "" {
				markParents(path, root)
				return filepath.SkipDir
			}
			if path != root && (strings.HasPrefix(d.Name(), ".") || scan.SkipDirName(d.Name())) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if in(path, target) {
					return filepath.SkipDir
				}
				if bundleKind(path) != "" || (req.Merge && mergeBundleKind(path) != "") {
					// A rule may place a complete, movable project folder. It may
					// not inspect or redistribute the project's internal files.
					if hasRules {
						if folder, _ := req.Profile.Match(path); folder != "" {
							markParents(path, root)
						}
					}
					return filepath.SkipDir
				}
				if !hasRules {
					return nil
				}
				info, e := d.Info()
				if e != nil {
					return e
				}
				dirs = append(dirs, ruleDirectoryState{path, info})
			} else if !d.Type().IsRegular() || scan.IsSensitiveName(d.Name()) || d.Name() == readme || d.Name() == "AGENTS.md" || d.Name() == "CLAUDE.md" {
				return nil
			}
			if hasRules {
				if folder, _ := req.Profile.Match(path); folder != "" {
					markParents(path, root)
				}
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			scope.blocked[root] = "未能完整检查本来源，保留原处：" + err.Error()
			continue
		}
		scope.directories[root] = dirs
	}
	return scope, nil
}

// Planning is read-only. A directory added/renamed while matching rules can
// invalidate which folders should be split, even if existing file bytes match.
func (scope *placementScope) verify(ctx context.Context) error {
	for _, dirs := range scope.directories {
		for _, saved := range dirs {
			if err := ctx.Err(); err != nil {
				return err
			}
			now, err := os.Lstat(saved.path)
			if err != nil || !os.SameFile(saved.info, now) || !saved.info.ModTime().Equal(now.ModTime()) || saved.info.Mode() != now.Mode() {
				return fmt.Errorf("来源目录在规则检查期间变化，请重新预览：%s", saved.path)
			}
		}
	}
	return nil
}
