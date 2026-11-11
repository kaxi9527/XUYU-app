package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	filehome "github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

type ProjectMemoryStatus struct {
	Sources             []ProjectMemorySource    `json:"sources"`
	Origins             int                      `json:"origins"`
	ProjectID           string                   `json:"projectID"`
	Revision            uint64                   `json:"revision"`
	Enabled             bool                     `json:"enabled"`
	Automatic           bool                     `json:"automatic"`
	BackgroundRunning   bool                     `json:"backgroundRunning"`
	BackgroundChecking  bool                     `json:"backgroundChecking"`
	BackgroundError     string                   `json:"backgroundError"`
	RealtimeWatching    bool                     `json:"realtimeWatching"`
	RealtimeError       string                   `json:"realtimeError"`
	NextCheckAt         string                   `json:"nextCheckAt"`
	CurrentScope        projectmemory.Scope      `json:"currentScope"`
	CurrentScopeSHA     string                   `json:"currentScopeSHA"`
	ScopeChanged        bool                     `json:"scopeChanged"`
	Tracked             int                      `json:"tracked"`
	Missing             int                      `json:"missing"`
	Untracked           int                      `json:"untracked"`
	LastObservedAt      string                   `json:"lastObservedAt"`
	ObservationComplete bool                     `json:"observationComplete"`
	Coverage            []projectmemory.Coverage `json:"coverage"`
	ProjectionPending   bool                     `json:"projectionPending"`
}

type memoryEnvironment struct {
	scope    versionScope
	tracking projectmemory.Scope
	versions *versions.Store
	catalog  versions.Catalog
	project  *versions.Project
	store    *projectmemory.Store
	control  string
}

func (s *ShelfService) memoryEnvironment(ctx context.Context, key string) (memoryEnvironment, error) {
	var out memoryEnvironment
	e, err := s.env()
	if err != nil {
		return out, err
	}
	out.scope, err = versionScopeDefinition(e, key)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for i, paths := range [][]string{out.scope.roots, out.scope.files} {
		for _, path := range paths {
			if err = out.scope.allowedBase(path); err != nil {
				return out, err
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			if i == 0 {
				out.tracking.Roots = append(out.tracking.Roots, path)
			} else {
				out.tracking.Files = append(out.tracking.Files, path)
			}
		}
	}
	sort.Strings(out.tracking.Roots)
	sort.Strings(out.tracking.Files)
	if err = out.tracking.Validate(); err != nil {
		return out, err
	}
	control := filepath.Join(e.Config.Root, e.Config.Names.Control)
	out.control = control
	if err = filehome.CheckPath(e.Config.Root, control); err != nil {
		return out, err
	}
	for _, name := range []string{"作品版本", "项目记忆"} {
		if err = filehome.CheckPath(e.Config.Root, filepath.Join(control, name)); err != nil {
			return out, err
		}
	}
	out.versions, err = versions.NewStore(filepath.Join(control, "作品版本"))
	if err != nil {
		return out, err
	}
	out.catalog, err = out.versions.Read(ctx)
	if err != nil {
		return out, err
	}
	out.project, err = out.catalog.FindProject(out.scope.project)
	if err != nil || out.project == nil {
		return out, err
	}
	out.store, err = projectmemory.NewStore(filepath.Join(control, "项目记忆"), projectmemory.Project{ID: out.project.ID, Name: out.project.Name, Path: out.project.Path})
	return out, err
}

// ProjectMemory reads existing state only; viewing an untracked project never
// creates a project ID, lock file, event or generated memory.
func (s *ShelfService) ProjectMemory(key string) (ProjectMemoryStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return ProjectMemoryStatus{}, err
	}
	out := ProjectMemoryStatus{CurrentScope: e.tracking, CurrentScopeSHA: e.tracking.Digest(), Coverage: []projectmemory.Coverage{}}
	if e.store == nil {
		return out, nil
	}
	out.ProjectID = e.project.ID
	v, err := e.store.Read(ctx)
	if err != nil {
		return out, err
	}
	out.Revision, out.Enabled = v.State.Revision, v.State.Enabled
	out.Automatic = v.State.Automatic
	out.Sources = projectMemorySources(v.State, out.CurrentScopeSHA)
	out.Origins = len(v.State.Origins)
	out.BackgroundRunning, out.BackgroundChecking, out.BackgroundError, out.NextCheckAt = s.projectMemoryRuntime(v.State.Project.ID)
	out.RealtimeWatching, out.RealtimeError = s.memoryWatchRuntime(v.State.Project.ID)
	if !v.State.Enabled || !v.State.Automatic || v.State.Scope.Digest() != out.CurrentScopeSHA {
		out.RealtimeWatching, out.RealtimeError = false, ""
	}
	out.ScopeChanged = v.State.Revision > 0 && v.State.Scope.Digest() != out.CurrentScopeSHA
	out.LastObservedAt, out.ObservationComplete = v.State.LastObservedAt, v.State.ObservationComplete
	out.Coverage, out.ProjectionPending = v.State.Coverage, v.ProjectionPending
	for _, f := range v.State.Files {
		switch f.Status {
		case "present":
			out.Tracked++
		case "missing":
			out.Missing++
		case "untracked":
			out.Untracked++
		}
	}
	return out, nil
}

func memoryRequestOK(operationID, scopeSHA string, tracking projectmemory.Scope) error {
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return errors.New("缺少有效操作标识")
	}
	if scopeSHA != tracking.Digest() {
		return projectmemory.ErrConflict
	}
	return nil
}

func (s *ShelfService) SetProjectMemoryTracking(key string, enabled bool, revision uint64, operationID, scopeSHA string) (projectmemory.Result, error) {
	finish := s.holdProjectMemory(key)
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return projectmemory.Result{}, err
	}
	if err = memoryRequestOK(operationID, scopeSHA, e.tracking); err != nil {
		return projectmemory.Result{}, err
	}
	if e.project == nil {
		if !enabled || revision != 0 {
			return projectmemory.Result{}, errors.New("项目记忆尚未建立")
		}
		// The identity write is independently durable. A later initialization
		// failure keeps this ID for retry; it never creates a second project.
		identityIntent := sha256.Sum256([]byte(key + "\x00" + operationID))
		for attempt := 0; attempt < 3; attempt++ {
			_, err = e.versions.EnsureProject(ctx, e.catalog.Revision, "memory-project-"+hex.EncodeToString(identityIntent[:]), e.scope.project)
			if err != nil && !errors.Is(err, versions.ErrConflict) {
				return projectmemory.Result{}, err
			}
			e, err = s.memoryEnvironment(ctx, key)
			if err != nil {
				return projectmemory.Result{}, err
			}
			if e.project != nil {
				break
			}
		}
		if e.store == nil {
			return projectmemory.Result{}, versions.ErrConflict
		}
	}
	if err = memoryRequestOK(operationID, scopeSHA, e.tracking); err != nil {
		return projectmemory.Result{}, err
	}
	if revision == 0 && enabled {
		return e.store.Initialize(ctx, operationID, e.tracking, time.Now())
	}
	return e.store.SetTracking(ctx, revision, operationID, e.tracking, enabled, time.Now())
}

func (s *ShelfService) ObserveProjectMemory(key string, revision uint64, operationID, scopeSHA string) (projectmemory.Result, error) {
	finish := s.holdProjectMemory(key)
	defer finish()
	// The scanner has its own 30-second budget; the extra time permits committing
	// the gap record after that deadline rather than losing the coverage result.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return projectmemory.Result{}, err
	}
	if err = memoryRequestOK(operationID, scopeSHA, e.tracking); err != nil {
		return projectmemory.Result{}, err
	}
	if e.store == nil {
		return projectmemory.Result{}, errors.New("请先开启此项目的文件管理记忆")
	}
	return e.store.Observe(ctx, revision, operationID, scopeSHA)
}

type ProjectMemoryFiles struct {
	Revision uint64               `json:"revision"`
	Total    int                  `json:"total"`
	Items    []projectmemory.File `json:"items"`
}

func (s *ShelfService) ProjectMemoryFiles(key string, revision uint64, offset int) (ProjectMemoryFiles, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return ProjectMemoryFiles{}, err
	}
	out := ProjectMemoryFiles{Items: []projectmemory.File{}}
	if e.store == nil {
		return out, nil
	}
	v, err := e.store.Read(ctx)
	if err != nil {
		return out, err
	}
	if v.State.Revision != revision {
		return out, projectmemory.ErrConflict
	}
	if offset < 0 || offset > len(v.State.Files) {
		return out, errors.New("目录分页位置无效")
	}
	files := append([]projectmemory.File{}, v.State.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Fact.Path == files[j].Fact.Path {
			return files[i].ID < files[j].ID
		}
		return files[i].Fact.Path < files[j].Fact.Path
	})
	end := offset + 100
	if end > len(files) {
		end = len(files)
	}
	out.Revision, out.Total, out.Items = v.State.Revision, len(files), files[offset:end]
	return out, nil
}

func (s *ShelfService) ProjectMemoryHistory(key string, after uint64) ([]projectmemory.Record, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return nil, err
	}
	if e.store == nil {
		return []projectmemory.Record{}, nil
	}
	return e.store.History(ctx, after, 20)
}
