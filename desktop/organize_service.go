package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/organize"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type OrganizeChoice struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type OrganizeWorkType struct {
	Label  string          `json:"label"`
	Layout worktype.Layout `json:"layout"`
}
type OrganizeContext struct {
	Project   string              `json:"project"`
	Root      string              `json:"root"`
	Sources   []string            `json:"sources"`
	Inferred  []allocate.Inferred `json:"inferred"`
	WorkTypes []OrganizeWorkType  `json:"workTypes"`
	Projects  []OrganizeChoice    `json:"projects"`
	Warnings  []string            `json:"warnings"`
	Profile   *worktype.Profile   `json:"profile,omitempty"`
	Templates []worktype.Profile  `json:"templates"`
}
type organizePlans struct {
	mu      sync.Mutex
	plans   map[string]*organize.Plan
	configs map[string]config.Config
	runs    map[string]*organizeRun
	pending map[string]context.CancelFunc
}

func (s *ShelfService) OrganizeContext(key string) (OrganizeContext, error) {
	e, err := s.env()
	if err != nil {
		return OrganizeContext{}, err
	}
	ctx := OrganizeContext{Root: e.Config.Root, Sources: []string{}, Inferred: []allocate.Inferred{}, Warnings: []string{}}
	templatesDir := filepath.Join(e.Config.Root, e.Config.Names.Control, "整理模板")
	if err := home.CheckPath(e.Config.Root, templatesDir); err == nil {
		entries, _ := os.ReadDir(templatesDir)
		for _, en := range entries {
			if len(ctx.Templates) >= 100 {
				break
			}
			if !strings.HasSuffix(en.Name(), ".json") {
				continue
			}
			if p, err := worktype.ReadProfile(filepath.Join(templatesDir, en.Name())); err == nil {
				ctx.Templates = append(ctx.Templates, *p)
			} else {
				ctx.Warnings = append(ctx.Warnings, "模板读取失败："+en.Name())
			}
		}
	}
	for _, w := range worktype.All() {
		ctx.WorkTypes = append(ctx.WorkTypes, OrganizeWorkType{w.Label(), worktype.LayoutFor(w.Template)})
	}
	for _, p := range shelf.Projects(e) {
		if p.Linked {
			continue
		}
		k := "root:" + p.Name
		if p.Source == shelf.SourceInPlace {
			k = "place:" + p.ID
		}
		ctx.Projects = append(ctx.Projects, OrganizeChoice{k, p.Name})
		if k != key {
			continue
		}
		ctx.Project = p.Name
		ctx.Sources = append(ctx.Sources, p.Path)
		if p.Source == shelf.SourceInPlace {
			if regp := e.Reg.ByID(p.ID); regp != nil {
				for _, m := range regp.Members {
					if m.Path != p.Path {
						ctx.Sources = append(ctx.Sources, m.Path)
					}
				}
			}
		}
	}
	// The scan excludes the organized root. Match its unassigned deliverables
	if ctx.Project != "" {
		path := filepath.Join(e.Config.Root, ctx.Project, worktype.ProfileFile)
		if home.CheckPath(e.Config.Root, path) == nil {
			if p, err := worktype.ReadProfile(path); err == nil {
				ctx.Profile = p
			} else if !os.IsNotExist(err) {
				ctx.Warnings = append(ctx.Warnings, "项目模板读取失败，请重新选择模板")
			}
		}
	}
	// against all known project names, retaining only unique name matches.
	if ctx.Project != "" && e.Reg != nil {
		for _, file := range e.Reg.Loose {
			stem := strings.ToLower(strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path)))
			matched := ""
			ambiguous := false
			for _, choice := range ctx.Projects {
				name := strings.ToLower(choice.Name)
				if len([]rune(name)) < 3 {
					continue
				}
				hit := stem == name
				for _, sep := range []string{"-", "_", " ", "·", "—"} {
					if strings.HasPrefix(stem, name+sep) {
						hit = true
					}
				}
				if hit {
					if matched != "" && matched != choice.Name {
						ambiguous = true
						break
					}
					matched = choice.Name
				}
			}
			if !ambiguous && matched == ctx.Project {
				exists := false
				for _, source := range ctx.Sources {
					if source == file.Path {
						exists = true
					}
				}
				if !exists {
					ctx.Sources = append(ctx.Sources, file.Path)
				}
			}
		}
	}
	if key != "" && ctx.Project == "" {
		return ctx, errors.New("项目已变化，请返回项目列表重新选择")
	}
	if e.Reg != nil && e.Reg.Partial {
		ctx.Warnings = append(ctx.Warnings, e.Reg.PartialReasons...)
	}
	seen := map[string]bool{}
	for i, path := range ctx.Sources {
		if i >= 32 {
			ctx.Warnings = append(ctx.Warnings, "仅推断前 32 个来源的工种，可手动补充")
			break
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.IsDir() {
			continue
		}
		for _, in := range allocate.InferProject(allocate.InferOptions{ProjectDir: path, ReadmeName: e.Config.Names.Readme, MaxEntries: 400}) {
			if !seen[in.Label] {
				ctx.Inferred = append(ctx.Inferred, in)
				seen[in.Label] = true
			}
		}
	}
	return ctx, nil
}

func (s *ShelfService) OrganizePreview(req organize.Request) (*organize.Plan, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return s.organizer.preview(cfg, userHome, req)
}
func (store *organizePlans) preview(cfg config.Config, userHome string, req organize.Request) (*organize.Plan, error) {
	ctx := context.Background()
	if req.PreviewID != "" {
		if len(req.PreviewID) > 80 {
			return nil, errors.New("预览标识无效")
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		store.mu.Lock()
		if store.pending == nil {
			store.pending = map[string]context.CancelFunc{}
		}
		if store.pending[req.PreviewID] != nil || len(store.pending) >= 2 {
			store.mu.Unlock()
			cancel()
			return nil, errors.New("已有预览正在生成，请等待或取消后重试")
		}
		store.pending[req.PreviewID] = cancel
		store.mu.Unlock()
		defer func() { cancel(); store.mu.Lock(); delete(store.pending, req.PreviewID); store.mu.Unlock() }()
	}
	p, err := organize.BuildContext(ctx, cfg, userHome, req)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	p.Token = hex.EncodeToString(nonce[:])
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.plans == nil {
		store.plans = map[string]*organize.Plan{}
	}
	if store.configs == nil {
		store.configs = map[string]config.Config{}
	}
	for key, old := range store.plans {
		if time.Since(old.Created) > 30*time.Minute {
			delete(store.plans, key)
			delete(store.configs, key)
		}
	}
	if len(store.plans) >= 8 {
		var oldest string
		for key, old := range store.plans {
			if oldest == "" || old.Created.Before(store.plans[oldest].Created) {
				oldest = key
			}
		}
		delete(store.plans, oldest)
		delete(store.configs, oldest)
	}
	store.plans[p.Token] = p
	store.configs[p.Token] = cfg
	return p, nil
}

// Cancellation affects only a read-only preview, never an already confirmed move.
func (s *ShelfService) OrganizeCancelPreview(id string) bool {
	s.organizer.mu.Lock()
	defer s.organizer.mu.Unlock()
	if cancel := s.organizer.pending[id]; cancel != nil {
		cancel()
		return true
	}
	return false
}
func (store *organizePlans) take(token string) (*organize.Plan, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	p := store.plans[token]
	if p == nil {
		return nil, errors.New("预览已执行、失效或应用已重启，请重新预览")
	}
	delete(store.plans, token)
	delete(store.configs, token)
	return p, nil
}
func (s *ShelfService) OrganizeApply(token string, ids []string) (*home.Summary, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return s.organizer.apply(cfg, token, ids, func(ctx context.Context, p *organize.Plan, selected []string) (*home.Summary, error) {
		sum, err := p.ApplyContext(ctx, cfg, selected)
		if err != nil {
			return sum, err
		}
		if sum.Moved+sum.Copied > 0 {
			if err := recordLayout(cfg, p); err != nil {
				sum.Warnings = append(sum.Warnings, "文件已整理，但项目目录说明未完整保存："+err.Error())
			}
			go s.RegistryRefresh()
		}
		return sum, nil
	})
}

// OrganizeStop requests a boundary stop; the running Apply call returns the receipt.
func (s *ShelfService) OrganizeStop(token string) bool { return s.organizer.stop(token) }
func recordLayout(cfg config.Config, p *organize.Plan) error {
	if err := writeProfile(cfg.Root, filepath.Join(p.Target, worktype.ProfileFile), p.Profile, false); err != nil {
		return err
	}
	for _, folder := range p.Folders {
		path := filepath.Join(p.Target, folder)
		if err := home.CheckPath(cfg.Root, path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	readme := filepath.Join(p.Target, cfg.Names.Readme)
	if err := home.CheckPath(cfg.Root, readme); err != nil {
		return err
	}
	f, err := os.OpenFile(readme, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		_, err = f.WriteString("# " + p.Project + "\n\n- 目录按已确认工种整理；原有空目录与未确认文件保留原位。\n")
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsExist(err) {
		return err
	}
	for _, label := range p.WorkTypes {
		if err := put.EnsureProjectWorkType(readme, label); err != nil {
			return err
		}
	}
	return nil
}
