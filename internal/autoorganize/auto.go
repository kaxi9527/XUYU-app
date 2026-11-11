// Package autoorganize groups explicitly attributed loose outputs and reuses
// the normal project creation, placement and restoration paths.
package autoorganize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/flock"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/organize"
	"github.com/kaxi9527/WorkShelf/internal/projectcreate"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type Policy struct {
	Enabled            bool              `json:"enabled"`
	ObserveSessions    bool              `json:"observeSessions,omitempty"`
	ObjectionEnabledAt string            `json:"objectionEnabledAt,omitempty"`
	Roots              []string          `json:"roots"`
	Sources            []SessionSource   `json:"sources,omitempty"`
	ProjectTrades      map[string]string `json:"projectTrades,omitempty"`
	SessionProjects    map[string]string `json:"sessionProjects,omitempty"`
}
type SessionSource struct {
	Adapter string `json:"adapter"`
	Path    string `json:"path"`
}
type Candidate struct {
	Project        string                  `json:"project"`
	ProjectKey     string                  `json:"projectKey,omitempty"`
	TargetPath     string                  `json:"targetPath,omitempty"`
	WorkType       string                  `json:"workType"`
	Files          []string                `json:"files"`
	Evidence       []string                `json:"evidence,omitempty"`
	Reason         string                  `json:"reason,omitempty"`
	Signals        []FileSignal            `json:"signals,omitempty"`
	ModelDecisions []ModelDecisionEvidence `json:"modelDecisions,omitempty"`
}
type FileSignal struct {
	Path        string `json:"path"`
	Agent       string `json:"agent,omitempty"`
	Session     string `json:"session,omitempty"`
	Task        string `json:"task,omitempty"`
	RecordSHA   string `json:"recordSHA,omitempty"`
	ActivitySHA string `json:"activitySHA,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Line        int64  `json:"line,omitempty"`
	SessionKey  string `json:"sessionKey,omitempty"`
}
type ModelDecisionEvidence struct {
	TaskID   string `json:"taskID"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Reason   string `json:"reason,omitempty"`
	InputSHA string `json:"inputSHA"`
}
type Operation struct {
	ID                     string                  `json:"id"`
	Project                string                  `json:"project"`
	ProjectKey             string                  `json:"projectKey,omitempty"`
	WorkType               string                  `json:"workType"`
	Files                  []string                `json:"files"`
	Signals                []FileSignal            `json:"signals,omitempty"`
	ModelDecisions         []ModelDecisionEvidence `json:"modelDecisions,omitempty"`
	PlacementReasons       map[string]string       `json:"placementReasons,omitempty"`
	Status                 string                  `json:"status"`
	Error                  string                  `json:"error,omitempty"`
	Target                 string                  `json:"target,omitempty"`
	UndoPath               string                  `json:"undoPath,omitempty"`
	UndoPaths              []string                `json:"undoPaths,omitempty"`
	Summary                *home.Summary           `json:"summary,omitempty"`
	Restore                *home.UndoResult        `json:"restore,omitempty"`
	Scaffold               map[string]string       `json:"scaffold,omitempty"`
	CreatedProject         bool                    `json:"createdProject,omitempty"`
	MemoryInitialized      bool                    `json:"memoryInitialized,omitempty"`
	PendingScaffoldCleanup bool                    `json:"pendingScaffoldCleanup,omitempty"`
	CreatedAt              time.Time               `json:"createdAt"`
	UpdatedAt              time.Time               `json:"updatedAt"`
}

func MarkMemoryInitialized(cfg config.Config, id string) error {
	_, err := locked(cfg, func() (Operation, error) {
		o, e := read(cfg, id)
		if e != nil {
			return o, e
		}
		o.MemoryInitialized = true
		return o, save(cfg, o)
	})
	return err
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func allowed(p Policy, path string) bool {
	for _, root := range p.Roots {
		if filepath.IsAbs(root) && within(filepath.Clean(root), path) {
			return true
		}
	}
	return false
}

// ExistingProjectNames lists only projects the current organization scope can use.
func ExistingProjectNames(cfg config.Config, p Policy) ([]string, error) {
	entries, err := os.ReadDir(cfg.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != cfg.Names.Control && put.CheckName("项目", entry.Name()) == nil {
			names = append(names, entry.Name())
			seen[entry.Name()] = true
		}
	}
	reg, err := registry.Load(cfg)
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	for _, project := range reg.Projects {
		if allowed(p, project.Path) && !seen[project.Name] && strings.TrimSpace(project.Name) != "" {
			names = append(names, project.Name)
			seen[project.Name] = true
		}
	}
	sort.Strings(names)
	return names, nil
}

func namedRegisteredProject(cfg config.Config, p Policy, reg *registry.Registry, name string) (*registry.Project, bool) {
	if reg == nil {
		return nil, false
	}
	if put.CheckName("项目", name) == nil {
		if _, err := os.Stat(filepath.Join(cfg.Root, name)); err == nil {
			return nil, false
		}
	}
	var found *registry.Project
	for i := range reg.Projects {
		project := &reg.Projects[i]
		if project.Name == name && allowed(p, project.Path) {
			if found != nil {
				return nil, true
			}
			found = project
		}
	}
	return found, false
}

func replacesRecord(previous, next activity.Record) bool {
	before, after := previous.When(), next.When()
	return before.IsZero() || after.IsZero() || !after.Before(before)
}

func latestFileRecords(records []activity.Record) map[string]activity.Record {
	latest := map[string]activity.Record{}
	for _, record := range records {
		path := filepath.Clean(record.Path)
		previous, exists := latest[path]
		if exists && !replacesRecord(previous, record) {
			continue
		}
		latest[path] = record
	}
	return latest
}

// Candidates uses the latest project and work-type declarations for each file.
// Files without a project or a recognizable trade remain available for later review.
func Candidates(cfg config.Config, policy Policy, records []activity.Record) []Candidate {
	reg, _ := registry.Load(cfg)
	type sessionSignal struct {
		path, source, hash string
		line               int64
	}
	type legacyMark struct{ path, agent, task, project string }
	restored := map[sessionSignal]bool{}
	restoredMarks := map[string]bool{}
	legacyMarks := map[legacyMark]time.Time{}
	operations, _ := List(cfg)
	for _, operation := range operations {
		if operation.Status != "restored" && operation.Status != "restore_partial" && operation.Status != "restore_failed" {
			continue
		}
		for _, signal := range operation.Signals {
			if signal.RecordSHA != "" {
				restored[sessionSignal{signal.Path, signal.SourcePath, signal.RecordSHA, signal.Line}] = true
			}
			if signal.ActivitySHA != "" {
				restoredMarks[signal.ActivitySHA] = true
			} else if signal.RecordSHA == "" {
				key := legacyMark{signal.Path, signal.Agent, signal.Task, operation.Project}
				if operation.CreatedAt.After(legacyMarks[key]) {
					legacyMarks[key] = operation.CreatedAt.Truncate(time.Second)
				}
			}
		}
	}
	latest := latestFileRecords(records)
	for path := range latest {
		if !filepath.IsAbs(path) || !allowed(policy, path) {
			delete(latest, path)
		}
	}
	grouping := newSessionGrouping(policy, reg, latest)
	groups := map[string]*Candidate{}
	for path, r := range latest {
		name := strings.TrimSpace(r.Project)
		activitySHA := ""
		var metadata struct {
			Session    string `json:"session"`
			RecordSHA  string `json:"recordSHA"`
			SourcePath string `json:"sourcePath"`
			Line       int64  `json:"line"`
			WorkingDir string `json:"workingDir"`
		}
		if r.Source == "会话文件操作" {
			_ = json.Unmarshal([]byte(r.Note), &metadata)
			if restored[sessionSignal{path, metadata.SourcePath, metadata.RecordSHA, metadata.Line}] {
				continue
			}
		} else {
			data, _ := json.Marshal(r)
			sum := sha256.Sum256(data)
			activitySHA = hex.EncodeToString(sum[:])
			if restoredMarks[activitySHA] {
				continue
			}
			if before := legacyMarks[legacyMark{path, r.Agent, r.Task, name}]; !before.IsZero() && !r.When().IsZero() && r.When().Before(before) {
				continue
			}
		}
		sessionID := grouping.key(r)
		chosen := policy.SessionProjects[sessionID]
		if chosen != "" {
			name = chosen
		}
		if name == "" || within(filepath.Join(cfg.Root, cfg.Names.Control), path) {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if owner := reg.Owner(path); owner != nil && owner.Name == name {
			continue
		}
		projectKey, target := "", ""
		if reg != nil && filepath.IsAbs(metadata.WorkingDir) {
			if project := reg.Owner(metadata.WorkingDir); project != nil && project.Name == name {
				for _, dir := range project.Dirs() {
					if allowed(policy, dir) && within(dir, metadata.WorkingDir) && len(dir) > len(target) {
						target = dir
					}
				}
				if target != "" {
					projectKey = "place:" + project.ID
				}
			}
		}
		if target == "" && (chosen != "" || r.Source != "会话文件操作") {
			if project, _ := namedRegisteredProject(cfg, policy, reg, name); project != nil {
				projectKey, target = "place:"+project.ID, project.Path
			}
		}
		if target != "" && within(target, path) {
			continue
		}
		if target == "" && (put.CheckName("项目", name) != nil || within(filepath.Join(cfg.Root, name), path)) {
			continue
		}
		groupKey := name + "\x00" + projectKey + "\x00" + target
		c := groups[groupKey]
		if c == nil {
			c = &Candidate{Project: name, ProjectKey: projectKey, TargetPath: target}
			groups[groupKey] = c
		}
		if w, err := worktype.Resolve(r.WorkType); err == nil {
			if c.WorkType == "" {
				c.WorkType = w.Label()
			} else if c.WorkType != w.Label() {
				c.Reason = "文件标记的工种不一致"
			}
		}
		c.Files = append(c.Files, path)
		signal := FileSignal{Path: path, Agent: r.Agent, Task: r.Task, ActivitySHA: activitySHA}
		if r.Source == "会话文件操作" {
			signal.SessionKey = sessionID
			signal.Session = metadata.Session
			signal.RecordSHA = metadata.RecordSHA
			signal.SourcePath = metadata.SourcePath
			signal.Line = metadata.Line
		}
		c.Signals = append(c.Signals, signal)
		c.Evidence = append(c.Evidence, path+"\x00"+r.Time+"\x00"+r.SHA256)
	}
	var out []Candidate
	for _, c := range groups {
		sort.Strings(c.Files)
		sort.Strings(c.Evidence)
		sort.Slice(c.Signals, func(i, j int) bool { return c.Signals[i].Path < c.Signals[j].Path })
		if c.WorkType == "" {
			c.Reason = "尚未识别工种"
		}
		if chosen := policy.ProjectTrades[c.Project]; chosen != "" {
			if w, err := worktype.Resolve(chosen); err == nil {
				c.WorkType = w.Label()
				c.Reason = ""
			}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

func createProject(cfg config.Config, o Operation, p worktype.Profile) (projectcreate.Result, error) {
	return projectcreate.Create(cfg.Root, cfg.Names.Readme, projectcreate.Input{OperationID: o.ID, Name: o.Project, WorkType: o.WorkType, Root: cfg.Root, Profile: p})
}

// Restore is triggered by the user's objection to this operation. It uses the
// same durable undo receipt as manual organization.
func Restore(cfg config.Config, id string) (Operation, error) {
	return restore(cfg, id, false)
}

// RestoreForObjection retains the empty scaffold briefly if its project memory
// still needs initialization; file restoration itself remains immediate.
func RestoreForObjection(cfg config.Config, id string) (Operation, error) {
	return restore(cfg, id, true)
}

func restore(cfg config.Config, id string, deferScaffold bool) (Operation, error) {
	if len(id) != 32 {
		return Operation{}, errors.New("整理记录标识无效")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return Operation{}, errors.New("整理记录标识无效")
	}
	return locked(cfg, func() (Operation, error) {
		o, err := read(cfg, id)
		if err != nil {
			return o, err
		}
		if o.Status == "organizing" || o.Status == "preparing" {
			finishInterrupted(cfg, &o)
			if err = save(cfg, o); err != nil {
				return o, err
			}
		}
		if o.Status == "restored" {
			return o, nil
		}
		paths := append([]string{}, o.UndoPaths...)
		if len(paths) == 0 && o.UndoPath != "" {
			paths = []string{o.UndoPath}
		}
		if len(paths) == 0 {
			return o, errors.New("本次整理没有可恢复的回执")
		}
		result := &home.UndoResult{RestoredPaths: []string{}}
		var failures []error
		for i := len(paths) - 1; i >= 0; i-- {
			part, e := home.Undo(cfg, paths[i])
			if e != nil {
				failures = append(failures, e)
				continue
			}
			result.Restored += part.Restored
			result.Quarantined += part.Quarantined
			result.Skipped += part.Skipped
			result.Unchanged += part.Unchanged
			result.Failed += part.Failed
			result.Notes = append(result.Notes, part.Notes...)
			result.RestoredPaths = append(result.RestoredPaths, part.RestoredPaths...)
		}
		err = errors.Join(failures...)
		o.Restore = result
		if err != nil {
			o.Status = "restore_failed"
			o.Error = err.Error()
		} else if result.Failed > 0 || result.Skipped > 0 {
			o.Status = "restore_partial"
			o.Error = "部分文件未恢复，请查看恢复结果"
		} else {
			o.Status = "restored"
			o.Error = ""
			if len(o.Scaffold) > 0 && deferScaffold && !o.MemoryInitialized {
				o.PendingScaffoldCleanup = true
			} else if len(o.Scaffold) > 0 {
				if e := removeScaffold(o.Target, o.Scaffold); e != nil {
					o.Error = "文件已恢复；项目目录保留：" + e.Error()
				}
			}
		}
		if e := save(cfg, o); e != nil {
			return o, e
		}
		return o, err
	})
}

func FinishScaffoldCleanup(cfg config.Config, id string) (Operation, error) {
	return locked(cfg, func() (Operation, error) {
		o, err := read(cfg, id)
		if err != nil || !o.PendingScaffoldCleanup || !o.MemoryInitialized {
			return o, err
		}
		if err = removeScaffold(o.Target, o.Scaffold); err != nil {
			o.Error = "文件已恢复；项目目录保留：" + err.Error()
		}
		o.PendingScaffoldCleanup = false
		return o, save(cfg, o)
	})
}

// FinishInterrupted waits for any active organizer before marking unfinished work.
func FinishInterrupted(cfg config.Config, id string) error {
	_, err := locked(cfg, func() (Operation, error) {
		o, err := read(cfg, id)
		if err != nil || (o.Status != "organizing" && o.Status != "preparing") {
			return o, err
		}
		finishInterrupted(cfg, &o)
		return o, save(cfg, o)
	})
	return err
}

func finishInterrupted(cfg config.Config, o *Operation) {
	if o.Status == "preparing" {
		o.Status = "failed"
		o.Error = "准备项目时应用退出，文件尚未开始整理"
		return
	}
	o.Status = "partial"
	o.Error = "整理过程中应用退出，可查看结果或恢复文件"
	if o.Summary == nil {
		o.Summary = &home.Summary{Root: cfg.Root}
	}
	seen := map[string]bool{}
	for _, item := range o.Summary.Items {
		seen[item.Source+"\x00"+item.Target] = true
	}
	for _, path := range o.UndoPaths {
		items, uncertain, err := home.InterruptedExactItems(cfg, path)
		if err != nil || uncertain > 0 {
			o.Summary.Warnings = append(o.Summary.Warnings, "部分中断记录需要核对，请查看恢复清单")
			o.Error = "整理过程中应用退出，部分文件状态待核对，请查看恢复清单"
		}
		for _, item := range items {
			if !seen[item.Source+"\x00"+item.Target] {
				o.Summary.Items = append(o.Summary.Items, item)
				o.Summary.Moved++
				seen[item.Source+"\x00"+item.Target] = true
			}
		}
	}
}

func operationID(c Candidate) string {
	// Model provenance enriches an operation without changing its identity; a
	// previously rejected file must not be collected again after an upgrade.
	c.ModelDecisions = nil
	c.Signals = append([]FileSignal(nil), c.Signals...)
	for i := range c.Signals {
		c.Signals[i].SessionKey = ""
		c.Signals[i].ActivitySHA = ""
	}
	b, _ := json.Marshal(c)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:16])
}
func dir(cfg config.Config) string                    { return filepath.Join(cfg.Root, cfg.Names.Control, "自动整理") }
func receiptPath(cfg config.Config, id string) string { return filepath.Join(dir(cfg), id+".json") }
func save(cfg config.Config, o Operation) error {
	o.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir(cfg), ".receipt-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, receiptPath(cfg, o.ID))
}
func read(cfg config.Config, id string) (Operation, error) {
	var o Operation
	b, e := os.ReadFile(receiptPath(cfg, id))
	if e != nil {
		return o, e
	}
	e = json.Unmarshal(b, &o)
	return o, e
}
func locked(cfg config.Config, fn func() (Operation, error)) (Operation, error) {
	if err := os.MkdirAll(dir(cfg), 0700); err != nil {
		return Operation{}, err
	}
	f, err := os.OpenFile(filepath.Join(dir(cfg), "执行.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Operation{}, err
	}
	defer f.Close()
	if err = flock.Lock(f); err != nil {
		return Operation{}, err
	}
	defer flock.Unlock(f)
	return fn()
}

// Execute moves a whole suggested project group only when the user has enabled
// automatic organization and authorized its source roots.
func Execute(ctx context.Context, cfg config.Config, userHome string, policy Policy, c Candidate) (Operation, error) {
	return locked(cfg, func() (Operation, error) {
		id := operationID(c)
		if old, err := read(cfg, id); err == nil {
			return old, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return Operation{}, err
		}
		if !policy.Enabled {
			return Operation{}, errors.New("自动整理未开启")
		}
		if c.Reason != "" || len(c.Files) == 0 {
			return Operation{}, errors.New("项目来源或工种尚未确定")
		}
		for _, p := range c.Files {
			if !filepath.IsAbs(p) || !allowed(policy, filepath.Clean(p)) {
				return Operation{}, errors.New("文件不在自动整理范围内")
			}
		}
		w, err := worktype.Resolve(c.WorkType)
		if err != nil {
			return Operation{}, err
		}
		layout := worktype.LayoutFor(w.Template)
		profile := worktype.Profile{Name: layout.Name, Folders: layout.Folders, Roles: layout.Roles}
		base := userHome
		covers := func(root string) bool {
			for _, p := range c.Files {
				if !within(root, p) {
					return false
				}
			}
			return true
		}
		if !covers(base) {
			base = string(filepath.Separator)
			for _, root := range policy.Roots {
				if covers(root) {
					base = root
					break
				}
			}
		}
		o := Operation{ID: id, Project: c.Project, ProjectKey: c.ProjectKey, WorkType: w.Label(), Files: append([]string{}, c.Files...), Signals: append([]FileSignal{}, c.Signals...), ModelDecisions: append([]ModelDecisionEvidence{}, c.ModelDecisions...), Status: "preparing", Target: filepath.Join(cfg.Root, c.Project), CreatedAt: time.Now().UTC()}
		if c.TargetPath == "" {
			reg, _ := registry.Load(cfg)
			if _, ambiguous := namedRegisteredProject(cfg, policy, reg, c.Project); ambiguous {
				return o, errors.New("有多个同名项目，请在对应项目目录中继续会话后再整理")
			}
		}
		if c.TargetPath != "" {
			reg, e := registry.Load(cfg)
			if e != nil {
				return o, e
			}
			project := reg.ByID(strings.TrimPrefix(c.ProjectKey, "place:"))
			if !strings.HasPrefix(c.ProjectKey, "place:") || project == nil || project.Name != c.Project || !allowed(policy, c.TargetPath) {
				return o, errors.New("原地项目不在当前整理范围")
			}
			found := false
			for _, dir := range project.Dirs() {
				if filepath.Clean(dir) == filepath.Clean(c.TargetPath) {
					found = true
					break
				}
			}
			if !found {
				return o, errors.New("原地项目位置已变化")
			}
			o.Target = c.TargetPath
		}
		existing := false
		if _, e := os.Lstat(o.Target); e == nil {
			existing = true
		} else if !errors.Is(e, os.ErrNotExist) {
			return o, e
		} else if c.TargetPath != "" {
			return o, errors.New("原地项目目录不可用")
		}
		if existing {
			saved, e := worktype.ReadProfile(filepath.Join(o.Target, worktype.ProfileFile))
			if e == nil {
				profile = *saved
			} else if !errors.Is(e, os.ErrNotExist) {
				return o, fmt.Errorf("项目的自定义整理规则无法读取：%w", e)
			}
		}
		created := false
		var runErr error
		blockedReason := ""
		var sources []string
		for _, file := range c.Files {
			checkRoot := base
			if within(cfg.Root, file) {
				checkRoot = cfg.Root
			}
			if e := home.CheckPath(checkRoot, file); e != nil {
				if blockedReason == "" {
					blockedReason = "文件已保留原处：" + e.Error()
				}
				continue
			}
			sources = append(sources, file)
		}
		for start := 0; start < len(sources); start += 64 {
			end := start + 64
			if end > len(sources) {
				end = len(sources)
			}
			request := organize.Request{Project: c.Project, Sources: sources[start:end], WorkTypes: []string{w.Label()}, Profile: &profile}
			var plan *organize.Plan
			var e error
			if c.TargetPath != "" {
				plan, e = organize.BuildAtContext(ctx, cfg, base, request, c.TargetPath)
			} else {
				plan, e = organize.BuildContext(ctx, cfg, base, request)
			}
			if e != nil {
				runErr = e
				break
			}
			var ids []string
			for _, r := range plan.Rows {
				if r.Blocked && blockedReason == "" {
					blockedReason = r.Reason
					if strings.HasPrefix(blockedReason, "保持完整工程的内部结构") {
						blockedReason = "保持完整工程的内部结构，工程内文件已保留原处：" + r.Source
					}
				}
				if r.Selected && !r.Blocked && !r.Existing {
					ids = append(ids, r.ID)
					if o.PlacementReasons == nil {
						o.PlacementReasons = map[string]string{}
					}
					o.PlacementReasons[r.Source] = r.Reason
				}
			}
			if len(ids) == 0 {
				continue
			}
			if !created {
				if e = save(cfg, o); e != nil {
					return o, e
				}
				if !existing {
					project, e := createProject(cfg, o, profile)
					if e != nil {
						o.Status = "failed"
						o.Error = e.Error()
						_ = save(cfg, o)
						return o, e
					}
					o.CreatedProject = true
					o.Scaffold, e = scaffold(project.Path)
					if e != nil {
						o.Status = "failed"
						o.Error = e.Error()
						_ = save(cfg, o)
						return o, e
					}
				}
				o.Status = "organizing"
				if e = save(cfg, o); e != nil {
					return o, e
				}
				created = true
			}
			summary, e := plan.ApplyContextWithUndoReady(ctx, cfg, ids, func(path string) error {
				o.UndoPaths = append(o.UndoPaths, path)
				if o.UndoPath == "" {
					o.UndoPath = path
				}
				return save(cfg, o)
			})
			if summary != nil {
				if o.Summary == nil {
					o.Summary = &home.Summary{Root: cfg.Root}
				}
				o.Summary.Items = append(o.Summary.Items, summary.Items...)
				o.Summary.Moved += summary.Moved
				o.Summary.Copied += summary.Copied
				o.Summary.Skipped += summary.Skipped
				o.Summary.Failed += summary.Failed
				o.Summary.Stopped += summary.Stopped
				o.Summary.Warnings = append(o.Summary.Warnings, summary.Warnings...)
			}
			if err = save(cfg, o); err != nil {
				return o, err
			}
			if e != nil {
				runErr = e
				break
			}
		}
		if !created && runErr == nil {
			if blockedReason != "" {
				return Operation{}, fmt.Errorf("没有可整理的文件，例如：%s", blockedReason)
			}
			return Operation{}, errors.New("没有可整理的文件")
		}
		err = runErr
		summary := o.Summary
		if err != nil {
			o.Status = "failed"
			if created {
				o.Status = "partial"
			}
			o.Error = err.Error()
		} else if summary == nil || summary.Failed > 0 || summary.Moved+summary.Copied < len(c.Files) {
			o.Status = "partial"
			o.Error = "部分文件未整理，请查看本次结果"
			if blockedReason != "" {
				o.Error = "部分文件未整理，例如：" + blockedReason
			}
		} else {
			o.Status = "organized"
		}
		if e := save(cfg, o); e != nil {
			return o, e
		}
		return o, err
	})
}
