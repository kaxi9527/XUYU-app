package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/projectplacement"
)

type placementPreviewEntry struct {
	key         string
	expires     time.Time
	preview     projectplacement.Preview
	undo        projectplacement.UndoPreview
	operationID string
	groupID     string
}
type placementRun struct {
	groupID string
	cancel  context.CancelFunc
	done    chan struct{}
	last    PlacementOperation
}
type placementConnection struct {
	mu       sync.Mutex
	closed   bool
	previews map[string]*placementPreviewEntry
	runs     map[string]*placementRun
}
type PlacementFile struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	Folder string `json:"folder"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Stage  string `json:"stage,omitempty"`
}
type PlacementPreview struct {
	Token     string          `json:"token"`
	ExpiresAt string          `json:"expiresAt"`
	Project   string          `json:"project"`
	Target    string          `json:"target"`
	Folders   []string        `json:"folders"`
	Rows      []PlacementFile `json:"rows"`
	Untouched int             `json:"untouched"`
	Undo      bool            `json:"undo"`
}
type PlacementUndoGroup struct {
	ID       string `json:"id"`
	SHA      string `json:"sha"`
	Status   string `json:"status"`
	Restored int    `json:"restored"`
	Total    int    `json:"total"`
}
type PlacementOperation struct {
	ID                  string               `json:"id"`
	IntentSHA           string               `json:"intentSHA"`
	Project             string               `json:"project"`
	Revision            uint64               `json:"revision"`
	Status              string               `json:"status"`
	Running             bool                 `json:"running"`
	ActiveUndoGroupID   string               `json:"activeUndoGroupID,omitempty"`
	NeedsReconciliation bool                 `json:"needsReconciliation"`
	StopRequested       bool                 `json:"stopRequested"`
	Problem             string               `json:"problem,omitempty"`
	Rows                []PlacementFile      `json:"rows"`
	UndoGroups          []PlacementUndoGroup `json:"undoGroups"`
}

func placementAuthority(e memoryEnvironment) projectplacement.Authority {
	bases := []string{e.scope.base}
	if e.scope.config.Root != e.scope.base {
		bases = append(bases, e.scope.config.Root)
	}
	sort.Strings(bases)
	return projectplacement.Authority{Project: projectmemory.Project{ID: e.project.ID, Name: e.project.Name, Path: e.project.Path}, Scope: e.tracking, Boundaries: bases, Excluded: []string{filepath.Join(e.scope.config.Root, e.scope.config.Names.Control)}}
}

func (s *ShelfService) placementEnvironment(ctx context.Context, key string) (*projectplacement.Journal, *projectplacement.Executor, error) {
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if e.project == nil || e.store == nil {
		return nil, nil, errors.New("请先开启这个项目的文件记录")
	}
	a := placementAuthority(e)
	j, err := projectplacement.NewJournal(filepath.Join(e.scope.config.Root, e.scope.config.Names.Control), a.Project)
	if err != nil {
		return nil, nil, err
	}
	x, err := projectplacement.NewExecutor(j, e.store, func(ctx context.Context, _ projectplacement.Intent) (projectplacement.ExecutionAuthority, error) {
		fresh, err := s.memoryEnvironment(ctx, key)
		if err != nil {
			return projectplacement.ExecutionAuthority{}, err
		}
		if fresh.project == nil || fresh.store == nil {
			return projectplacement.ExecutionAuthority{}, projectplacement.ErrConsent
		}
		// Forward execution validates this profile. Historical recovery/undo
		// needs registration authority even if the template is now unavailable.
		profile, _ := analysisProfile(fresh)
		return projectplacement.ExecutionAuthority{Authority: placementAuthority(fresh), Profile: profile}, nil
	})
	return j, x, err
}

func (s *ShelfService) rememberPlacementPreview(entry *placementPreviewEntry) (string, error) {
	token, err := analysisToken()
	if err != nil {
		return "", err
	}
	b := &s.placements
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", errors.New("应用正在退出")
	}
	if b.previews == nil {
		b.previews = map[string]*placementPreviewEntry{}
	}
	for k, p := range b.previews {
		if time.Now().After(p.expires) {
			delete(b.previews, k)
		}
	}
	if len(b.previews) >= 8 {
		return "", errors.New("打开的整理预览较多，请先关闭其他预览")
	}
	b.previews[token] = entry
	return token, nil
}

// Only stored successful model receipts can become proposals. Browser input is
// an existing request ID, never advice, paths, scope or a native plan.
func (s *ShelfService) PreviewProjectPlacement(key, kind, id string) (PlacementPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, v, profile, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return PlacementPreview{}, err
	}
	single, batch, _, err := e.store.AnalysisReceipt(ctx, id)
	if err != nil {
		return PlacementPreview{}, err
	}
	evidence := []projectcontext.PlacementEvidence{}
	if kind == "single" && single != nil && single.Status == "succeeded" {
		packet, err := projectcontext.Restore([]byte(single.Spec.Request.Input))
		if err != nil {
			return PlacementPreview{}, err
		}
		evidence = append(evidence, projectcontext.PlacementEvidence{Kind: kind, RequestID: id, Packet: packet, Advice: single.Advice, Recipient: single.Spec.Recipient})
	} else if kind == "batch" && batch != nil {
		if batch.Status == "active" {
			return PlacementPreview{}, errors.New("请先暂停或结束分析，再查看文件整理预览")
		}
		for i, item := range batch.Items {
			if item.Status != "succeeded" {
				continue
			}
			packet, err := projectcontext.Restore([]byte(batch.Spec.Requests[i].Request.Input))
			if err != nil {
				return PlacementPreview{}, err
			}
			evidence = append(evidence, projectcontext.PlacementEvidence{Kind: kind, RequestID: id, Part: i, Packet: packet, Advice: item.Advice, Recipient: batch.Spec.Requests[i].Recipient})
		}
	} else {
		return PlacementPreview{}, errors.New("这次分析没有可采用的成功建议")
	}
	proposal, err := projectcontext.ResolvePlacements(v, profile, evidence)
	if err != nil {
		return PlacementPreview{}, err
	}
	p, err := projectplacement.BuildPreview(ctx, placementAuthority(e), proposal, projectplacement.PreviewLimits{})
	if err != nil {
		return PlacementPreview{}, err
	}
	entry := &placementPreviewEntry{key: key, expires: time.Now().Add(10 * time.Minute), preview: p}
	token, err := s.rememberPlacementPreview(entry)
	if err != nil {
		return PlacementPreview{}, err
	}
	out := PlacementPreview{Token: token, ExpiresAt: entry.expires.UTC().Format(time.RFC3339Nano), Project: e.project.Name, Target: e.project.Path, Folders: profile.Folders, Rows: []PlacementFile{}, Untouched: proposal.UntouchedFiles}
	for _, r := range p.Snapshot().Rows {
		out.Rows = append(out.Rows, PlacementFile{ID: r.FileID, Source: r.Source, Target: r.Target, Folder: r.Folder, Action: r.Action, Reason: r.Reason})
	}
	uncertain := map[string]bool{}
	for _, id := range proposal.UncertainIDs {
		uncertain[id] = true
	}
	for _, f := range v.State.Files {
		if uncertain[f.ID] {
			out.Rows = append(out.Rows, PlacementFile{ID: f.ID, Source: f.Fact.Path, Action: "keep", Reason: "依据不足，保留原处"})
		}
	}
	return out, nil
}

func (s *ShelfService) ReleaseProjectPlacementPreview(key, token string) {
	b := &s.placements
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.previews[token]; p != nil && p.key == key {
		delete(b.previews, token)
	}
}

func (s *ShelfService) ConfirmProjectPlacement(key, token, operationID string, selected []string) (PlacementOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := &s.placements
	b.mu.Lock()
	entry := b.previews[token]
	if entry == nil || entry.key != key || time.Now().After(entry.expires) || entry.undo.SHA256() != "" || entry.operationID != "" && entry.operationID != operationID {
		b.mu.Unlock()
		return PlacementOperation{}, projectplacement.ErrConsent
	}
	entry.operationID = operationID
	p := entry.preview
	b.mu.Unlock()
	j, _, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	v, err := j.Prepare(ctx, operationID, p, selected)
	if err != nil {
		return PlacementOperation{}, err
	}
	e, _, _, err := s.analysisSnapshot(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	v, err = j.Confirm(ctx, operationID, v.IntentSHA, placementAuthority(e), time.Now())
	return placementOperation(v), err
}

func placementOperation(v projectplacement.JournalView) PlacementOperation {
	o := PlacementOperation{ID: v.Intent.ID, IntentSHA: v.IntentSHA, Project: v.Intent.Preview.Authority.Project.Name, Revision: v.Revision, Status: v.Status, NeedsReconciliation: v.NeedsReconciliation, StopRequested: v.StopRequested, Rows: []PlacementFile{}, UndoGroups: []PlacementUndoGroup{}}
	rows := map[string]projectplacement.Row{}
	for _, r := range v.Intent.Preview.Rows {
		rows[r.FileID] = r
	}
	for _, item := range v.Items {
		r := rows[item.FileID]
		reason := r.Reason
		if item.Receipt != nil {
			reason = item.Receipt.Reason
		}
		o.Rows = append(o.Rows, PlacementFile{ID: item.FileID, Source: r.Source, Target: r.Target, Folder: r.Folder, Action: r.Action, Stage: item.Stage, Reason: reason})
	}
	for _, g := range v.UndoGroups {
		u := PlacementUndoGroup{ID: g.ID, SHA: g.ManifestSHA, Status: g.Status, Total: len(g.Items)}
		for _, item := range g.Items {
			if item.Stage == "undone" {
				u.Restored++
				for i := range o.Rows {
					if o.Rows[i].ID == item.FileID {
						o.Rows[i].Stage = "undone"
					}
				}
			}
		}
		o.UndoGroups = append(o.UndoGroups, u)
	}
	return o
}

func placementRunKey(key, id string) string { return key + "\x00" + id }

func (s *ShelfService) ProjectPlacementOperation(key, id string) (PlacementOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, _, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	v, readErr := j.Read(ctx, id)
	b := &s.placements
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.runs[placementRunKey(key, id)]
	if readErr != nil {
		// During a paired journal publication, show the last verified state;
		// never treat a partial read as an empty or completed operation.
		if run != nil && run.cancel != nil {
			o := run.last
			o.Running = true
			return o, nil
		}
		return PlacementOperation{}, readErr
	}
	o := placementOperation(v)
	if run != nil {
		o.Running = run.cancel != nil
		if o.Running {
			o.ActiveUndoGroupID = run.groupID
		}
		o.Problem = run.last.Problem
		if o.Revision >= run.last.Revision {
			run.last = o
		}
	}
	return o, nil
}

func (s *ShelfService) ExecuteProjectPlacement(key, id, intentSHA string) (PlacementOperation, error) {
	return s.executePlacement(key, id, intentSHA, "", "")
}
func (s *ShelfService) ExecuteProjectPlacementUndo(key, id, intentSHA, groupID, groupSHA string) (PlacementOperation, error) {
	if groupID == "" || groupSHA == "" {
		return PlacementOperation{}, projectplacement.ErrConsent
	}
	return s.executePlacement(key, id, intentSHA, groupID, groupSHA)
}

func (s *ShelfService) executePlacement(key, id, intentSHA, groupID, groupSHA string) (PlacementOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	j, x, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	v, err := j.Read(ctx, id)
	if err != nil {
		return PlacementOperation{}, err
	}
	if v.IntentSHA != intentSHA {
		return PlacementOperation{}, projectplacement.ErrConsent
	}
	b := &s.placements
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return PlacementOperation{}, errors.New("应用正在退出")
	}
	if b.runs == nil {
		b.runs = map[string]*placementRun{}
	}
	rk := placementRunKey(key, id)
	for k, r := range b.runs {
		if r.cancel != nil {
			b.mu.Unlock()
			return PlacementOperation{}, errors.New("已有文件操作正在进行，请先等待或停止")
		}
		if len(b.runs) >= 16 && k != rk {
			delete(b.runs, k)
		}
	}
	run := &placementRun{groupID: groupID, cancel: cancel, done: make(chan struct{}), last: placementOperation(v)}
	run.last.Running = true
	run.last.ActiveUndoGroupID = groupID
	b.runs[rk] = run
	b.mu.Unlock()
	finish := s.holdProjectMemory(key)
	defer finish()
	defer func() { b.mu.Lock(); run.cancel = nil; run.last.Running = false; close(run.done); b.mu.Unlock() }()
	if groupID == "" {
		v, err = x.Execute(ctx, id, intentSHA)
	} else {
		v, err = x.ExecuteUndo(ctx, id, intentSHA, groupID, groupSHA)
	}
	o := placementOperation(v)
	if err != nil {
		o.Problem = err.Error()
	}
	b.mu.Lock()
	if v.Intent.ID != "" {
		run.last = o
	}
	b.mu.Unlock()
	if v.Intent.ID == "" {
		return PlacementOperation{}, err
	}
	return o, nil
}

func (s *ShelfService) StopProjectPlacement(key, id, intentSHA, groupID, groupSHA string) (PlacementOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, _, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	var v projectplacement.JournalView
	if groupID == "" {
		v, err = j.RequestStop(ctx, id, intentSHA, time.Now())
	} else {
		v, err = j.RequestUndoStop(ctx, id, intentSHA, groupID, groupSHA, time.Now())
	}
	if err != nil {
		return PlacementOperation{}, err
	}
	b := &s.placements
	b.mu.Lock()
	if r := b.runs[placementRunKey(key, id)]; r != nil && r.cancel != nil && r.groupID == groupID {
		r.cancel()
	}
	b.mu.Unlock()
	return placementOperation(v), nil
}

func (s *ShelfService) PreviewProjectPlacementUndo(key, id, intentSHA string) (PlacementPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	j, x, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementPreview{}, err
	}
	u, err := x.ReviewUndo(ctx, id, intentSHA)
	if err != nil {
		return PlacementPreview{}, err
	}
	v, err := j.Read(ctx, id)
	if err != nil {
		return PlacementPreview{}, err
	}
	original := map[string]projectplacement.Row{}
	for _, r := range v.Intent.Preview.Rows {
		original[r.FileID] = r
	}
	entry := &placementPreviewEntry{key: key, operationID: id, undo: u, expires: time.Now().Add(10 * time.Minute)}
	token, err := s.rememberPlacementPreview(entry)
	if err != nil {
		return PlacementPreview{}, err
	}
	o := PlacementPreview{Token: token, ExpiresAt: entry.expires.UTC().Format(time.RFC3339Nano), Project: "恢复原来的位置", Target: "原来的文件夹", Rows: []PlacementFile{}, Folders: []string{}, Undo: true}
	folders := map[string]bool{}
	for _, r := range u.Snapshot().Rows {
		if r.Source.Path == "" {
			r.Source.Path = original[r.FileID].Source
		}
		folder := filepath.Dir(r.Destination.Path)
		if folder == "." {
			folder = "保留原处"
		}
		folders[folder] = true
		action := "keep"
		if r.Action == "restore" {
			action = "move"
		}
		o.Rows = append(o.Rows, PlacementFile{ID: r.FileID, Source: r.Source.Path, Target: r.Destination.Path, Folder: folder, Action: action, Reason: r.Reason})
	}
	for folder := range folders {
		o.Folders = append(o.Folders, folder)
	}
	sort.Strings(o.Folders)
	return o, nil
}

func (s *ShelfService) ConfirmProjectPlacementUndo(key, token, groupID string, selected []string) (PlacementOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	b := &s.placements
	b.mu.Lock()
	entry := b.previews[token]
	if entry == nil || entry.key != key || entry.undo.SHA256() == "" || time.Now().After(entry.expires) || entry.groupID != "" && entry.groupID != groupID {
		b.mu.Unlock()
		return PlacementOperation{}, projectplacement.ErrConsent
	}
	entry.groupID = groupID
	u := entry.undo
	b.mu.Unlock()
	_, x, err := s.placementEnvironment(ctx, key)
	if err != nil {
		return PlacementOperation{}, err
	}
	v, err := x.ConfirmUndo(ctx, groupID, u, selected)
	return placementOperation(v), err
}

func (s *ShelfService) closePlacements() {
	b := &s.placements
	b.mu.Lock()
	b.closed = true
	wait := []chan struct{}{}
	for _, r := range b.runs {
		if r.cancel != nil {
			r.cancel()
			wait = append(wait, r.done)
		}
	}
	b.previews = nil
	b.mu.Unlock()
	for _, done := range wait {
		<-done
	}
}
