package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/config"
	filehome "github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

const versionReadLimit int64 = 1 << 30

type versionScope struct {
	project versions.ProjectInput
	roots   []string
	files   []string
	base    string
	config  config.Config
}

func pathInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func versionScopeFor(e shelf.Env, key string) (versionScope, error) {
	scope, err := versionScopeDefinition(e, key)
	if err != nil {
		return scope, err
	}
	info, err := os.Lstat(scope.project.Path)
	if err != nil {
		return scope, err
	}
	if !info.IsDir() {
		return scope, errors.New("项目位置不是实际文件夹")
	}
	return scope, nil
}

// Resolve registered scope without requiring an online project directory.
// File-memory readers can retain history and record an offline coverage gap.
func versionScopeDefinition(e shelf.Env, key string) (versionScope, error) {
	scope := versionScope{config: e.Config, base: e.Home}
	switch {
	case strings.HasPrefix(key, "root:"):
		name := strings.TrimPrefix(key, "root:")
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.HasPrefix(name, ".") || name == e.Config.Names.Control {
			return scope, errors.New("请选择实际项目")
		}
		path := filepath.Join(e.Config.Root, name)
		scope.project = versions.ProjectInput{Key: key, Name: name, Path: path}
		scope.roots = []string{path}
	case strings.HasPrefix(key, "place:"):
		if e.Reg == nil {
			return scope, errors.New("原地项目登记不可用")
		}
		p := e.Reg.ByID(strings.TrimPrefix(key, "place:"))
		if p == nil {
			return scope, errors.New("原地项目不存在")
		}
		scope.project = versions.ProjectInput{Key: key, Name: p.Name, Path: filepath.Clean(p.Path)}
		scope.roots = p.Dirs()
		for _, m := range p.Members {
			if !m.IsDir && m.Via == "" {
				scope.files = append(scope.files, filepath.Clean(m.Path))
			}
		}
	default:
		return scope, errors.New("请选择要登记作品的项目")
	}
	if err := scope.allowedBase(scope.project.Path); err != nil {
		return scope, err
	}
	return scope, nil
}

func (s versionScope) allowedBase(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("文件位置必须是完整路径")
	}
	base := s.base
	if pathInside(s.config.Root, path) {
		base = s.config.Root
	} else if !pathInside(base, path) {
		// Registered projects can also reside on another volume.
		for _, root := range s.roots {
			if pathInside(root, path) {
				base = root
				break
			}
		}
		for _, file := range s.files {
			if file == path {
				base = filepath.Dir(file)
				break
			}
		}
	}
	if err := filehome.CheckPath(base, path); err != nil {
		return err
	}
	control := filepath.Join(s.config.Root, s.config.Names.Control)
	if pathInside(control, path) {
		return errors.New("总控记录不能登记为作品内容")
	}
	return nil
}

func (s versionScope) rootFor(path string, includeHidden bool) (string, error) {
	if err := s.allowedBase(path); err != nil {
		return "", err
	}
	root := ""
	for _, candidate := range s.roots {
		candidate = filepath.Clean(candidate)
		if path != candidate && pathInside(candidate, path) && (root == "" || len(candidate) > len(root)) {
			root = candidate
		}
	}
	if root == "" {
		for _, file := range s.files {
			if file == path {
				root = filepath.Dir(file)
				break
			}
		}
	}
	if root == "" {
		return "", errors.New("文件不属于所选项目的已登记位置")
	}
	// Inspect every component below the allowed home/config root, not just the
	// selected project's relative path: a project cannot grant access to .codex.
	base := s.base
	if pathInside(s.config.Root, path) {
		base = s.config.Root
	}
	relative, _ := filepath.Rel(base, path)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		lower := strings.ToLower(part)
		if scan.IsSensitiveName(part) || part == s.config.Names.Sensitive {
			return "", errors.New("此路径包含敏感文件或目录，不能登记为作品")
		}
		switch lower {
		case ".codex", ".claude", ".git", ".hg", ".svn", ".ssh", ".aws", ".gnupg", "node_modules", ".venv", "venv":
			return "", errors.New("工具历史、工程内部记录和依赖目录不能登记为作品")
		}
		if strings.HasPrefix(part, ".") && !includeHidden {
			return "", errors.New("这是隐藏文件，请明确选择包含隐藏文件后再登记")
		}
	}
	if err := filehome.CheckPath(root, path); err != nil {
		return "", err
	}
	return root, nil
}

func (s versionScope) allow(loc versions.Location) error {
	if _, err := s.rootFor(loc.Path, true); err != nil {
		return err
	}
	if err := filehome.CheckPath(loc.AuthorizedRoot, loc.Path); err != nil {
		return err
	}
	return nil
}

func (s *ShelfService) versionEnvironment(key string) (versionScope, *versions.Store, error) {
	e, err := s.env()
	if err != nil {
		return versionScope{}, nil, err
	}
	scope, err := versionScopeFor(e, key)
	if err != nil {
		return scope, nil, err
	}
	dir := filepath.Join(e.Config.Root, e.Config.Names.Control, "作品版本")
	if err = filehome.CheckPath(e.Config.Root, dir); err != nil {
		return scope, nil, err
	}
	store, err := versions.NewStore(dir)
	return scope, store, err
}
