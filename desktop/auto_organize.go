package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type autoOrganizeConnection struct {
	mu              sync.Mutex
	run             sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	lastError       string
	pending         bool
	interval        time.Duration
	modelCancel     context.CancelFunc
	objectionCancel context.CancelFunc
	report          func(autoorganize.Operation, bool)
}
type AutoOrganizeView struct {
	Policy           autoorganize.Policy              `json:"policy"`
	ProjectRoot      string                           `json:"projectRoot"`
	SuggestedRoots   []string                         `json:"suggestedRoots"`
	WorkTypes        []string                         `json:"workTypes"`
	Candidates       []autoorganize.Candidate         `json:"candidates"`
	Operations       []autoorganize.Operation         `json:"operations"`
	Error            string                           `json:"error,omitempty"`
	SessionIssues    []string                         `json:"sessionIssues,omitempty"`
	SessionFileCount int                              `json:"sessionFileCount"`
	Unassigned       []autoorganize.UnassignedSession `json:"unassigned,omitempty"`
	Model            AutoModelView                    `json:"model"`
	ObjectionModel   AutoModelView                    `json:"objectionModel"`
}

func (s *ShelfService) AutoOrganizeStatus() (AutoOrganizeView, error) {
	var view AutoOrganizeView
	cfg, _, err := loadConfig()
	if err != nil {
		return view, err
	}
	view.Policy, err = autoorganize.ReadPolicy(cfg)
	if err != nil {
		return view, err
	}
	view.ProjectRoot = cfg.Root
	view.Model, err = s.autoModelStateView(cfg, view.Policy)
	if err != nil {
		return view, err
	}
	view.ObjectionModel, err = s.autoObjectionModelView(cfg, view.Policy)
	if err != nil {
		return view, err
	}
	view.SuggestedRoots = append([]string{}, cfg.ScanPaths...)
	for _, w := range worktype.All() {
		view.WorkTypes = append(view.WorkTypes, w.Label())
	}
	view.Operations, err = autoorganize.List(cfg)
	if err != nil {
		return view, err
	}
	var sessions []activity.Record
	if view.Policy.Enabled || view.Policy.ObserveSessions {
		var issues []string
		sessions, issues, err = autoorganize.ReadSessionEvidence(cfg, view.Policy)
		if err != nil {
			return view, err
		}
		view.SessionIssues = issues
		view.SessionFileCount = len(sessions)
		records, e := activity.ReadSince(activity.Dir(cfg), time.Time{})
		if e != nil {
			return view, e
		}
		view.Candidates = autoorganize.Candidates(cfg, view.Policy, append(sessions, records...))
		view.Unassigned = autoorganize.UnassignedSessions(cfg, view.Policy, append(sessions, records...))
	}
	s.autoOrganize.mu.Lock()
	view.Error = s.autoOrganize.lastError
	s.autoOrganize.mu.Unlock()
	return view, nil
}
func (s *ShelfService) SetAutoOrganize(enabled bool, roots []string, sources []autoorganize.SessionSource) (AutoOrganizeView, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return AutoOrganizeView{}, err
	}
	if (enabled || len(sources) > 0) && len(roots) == 0 {
		return AutoOrganizeView{}, errors.New("请先选择自动整理范围")
	}
	previous, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		return AutoOrganizeView{}, err
	}
	p := autoorganize.Policy{Enabled: enabled, ObserveSessions: len(sources) > 0, Roots: roots, Sources: sources, ProjectTrades: previous.ProjectTrades, SessionProjects: previous.SessionProjects}
	if autoorganize.PolicyScopeSHA(previous) == autoorganize.PolicyScopeSHA(p) {
		p.ObjectionEnabledAt = previous.ObjectionEnabledAt
	}
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		return AutoOrganizeView{}, err
	}
	if !enabled || autoorganize.PolicyScopeSHA(previous) != autoorganize.PolicyScopeSHA(p) {
		s.cancelAutoModel()
	}
	if autoorganize.PolicyScopeSHA(previous) != autoorganize.PolicyScopeSHA(p) {
		s.cancelObjectionModel()
	}
	return s.AutoOrganizeStatus()
}

func (s *ShelfService) SetAutoObjections(enabled bool) (AutoOrganizeView, error) {
	if !enabled {
		s.cancelObjectionModel()
	}
	s.autoOrganize.run.Lock()
	defer s.autoOrganize.run.Unlock()
	cfg, _, err := loadConfig()
	if err != nil {
		return AutoOrganizeView{}, err
	}
	p, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		return AutoOrganizeView{}, err
	}
	if enabled && (len(p.Sources) == 0 || (!p.Enabled && !p.ObserveSessions)) {
		return AutoOrganizeView{}, errors.New("请先保存会话目录")
	}
	if enabled && p.ObjectionEnabledAt == "" {
		p.ObjectionEnabledAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if !enabled {
		p.ObjectionEnabledAt = ""
	}
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		return AutoOrganizeView{}, err
	}
	if !enabled {
		if err = autoorganize.SetObjectionModelConsent(cfg, nil); err != nil {
			return AutoOrganizeView{}, err
		}
	}
	return s.AutoOrganizeStatus()
}
func (s *ShelfService) ConfirmAutoSession(sessionID, project, label string) (AutoOrganizeView, error) {
	project = strings.TrimSpace(project)
	w, err := worktype.Resolve(label)
	if err != nil {
		return AutoOrganizeView{}, err
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil {
		return view, err
	}
	found := false
	for _, group := range view.Unassigned {
		if group.ID == sessionID {
			found = true
			break
		}
	}
	if !found {
		return view, errors.New("会话分组已变化，请重新检查")
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return view, err
	}
	p, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		return view, err
	}
	if !p.Enabled {
		return view, errors.New("自动整理已关闭")
	}
	if nameErr := put.CheckName("项目", project); nameErr != nil {
		names, e := autoorganize.ExistingProjectNames(cfg, p)
		if e != nil {
			return view, e
		}
		found := false
		for _, name := range names {
			if name == project {
				found = true
				break
			}
		}
		if !found {
			return view, nameErr
		}
	}
	if p.SessionProjects == nil {
		p.SessionProjects = map[string]string{}
	}
	p.SessionProjects[sessionID] = project
	if p.ProjectTrades == nil {
		p.ProjectTrades = map[string]string{}
	}
	p.ProjectTrades[project] = w.Label()
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		return view, err
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		view, _ = s.AutoOrganizeStatus()
		return view, err
	}
	return s.AutoOrganizeStatus()
}
func (s *ShelfService) ConfirmAutoProjectTrade(project, label string) (AutoOrganizeView, error) {
	w, err := worktype.Resolve(label)
	if err != nil {
		return AutoOrganizeView{}, err
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil {
		return view, err
	}
	if !view.Policy.Enabled {
		return view, errors.New("请先开启自动整理")
	}
	found := false
	for _, candidate := range view.Candidates {
		if candidate.Project == project && candidate.Reason != "" {
			found = true
			break
		}
	}
	if !found {
		return view, errors.New("项目候选已变化，请重新检查")
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return view, err
	}
	p, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		return view, err
	}
	if !p.Enabled {
		return view, errors.New("自动整理已关闭")
	}
	if p.ProjectTrades == nil {
		p.ProjectTrades = map[string]string{}
	}
	p.ProjectTrades[project] = w.Label()
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		return view, err
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		view, _ = s.AutoOrganizeStatus()
		return view, err
	}
	return s.AutoOrganizeStatus()
}
func (s *ShelfService) RejectAutoOrganize(id string) (autoorganize.Operation, error) {
	return s.rejectAutoOrganize(id, "用户", nil)
}
func (s *ShelfService) rejectAutoOrganize(id, requestedBy string, source *autoorganize.ObjectionSource) (autoorganize.Operation, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return autoorganize.Operation{}, err
	}
	o, err := autoorganize.RestoreForObjection(cfg, id)
	if err != nil {
		return o, err
	}
	if !o.MemoryInitialized && o.Restore != nil {
		p, e := autoorganize.ReadPolicy(cfg)
		if e != nil {
			return o, e
		}
		// The worker may have restored a just-interrupted operation while the
		// application was closed. Preserve both events in project memory.
		beforeRestore := o
		beforeRestore.Status = "partial"
		beforeRestore.Restore = nil
		if e = s.initializeAutoProjectMemory(beforeRestore, p); e != nil {
			return o, e
		}
		if e = autoorganize.MarkMemoryInitialized(cfg, o.ID); e != nil {
			return o, e
		}
		o.MemoryInitialized = true
	}
	if o.MemoryInitialized && o.Restore != nil {
		if e := s.recordAutoMemory(o, requestedBy, source); e != nil && err == nil {
			return o, e
		}
	}
	if o.PendingScaffoldCleanup {
		return autoorganize.FinishScaffoldCleanup(cfg, o.ID)
	}
	return o, err
}
func (s *ShelfService) RunAutoOrganize() error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	if err = autoorganize.RetryModelAttempts(cfg); err != nil {
		return err
	}
	return s.runAutoOrganize(context.Background())
}
func (s *ShelfService) processAutoObjections(cfg config.Config) error {
	requests, err := autoorganize.PendingObjections(cfg)
	if err != nil {
		return err
	}
	for _, request := range requests {
		o, err := s.rejectAutoOrganize(request.OperationID, request.Agent, request.Source)
		if err != nil {
			return err
		}
		if !o.MemoryInitialized {
			return errors.New("文件已恢复，等待同步项目记忆")
		}
		if err = autoorganize.CompleteObjection(cfg, request); err != nil {
			return err
		}
		s.reportAutoOrganize(o, true)
	}
	return nil
}

func (s *ShelfService) runAutoOrganize(ctx context.Context) error {
	s.autoOrganize.run.Lock()
	defer s.autoOrganize.run.Unlock()
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	unfinished, err := autoorganize.List(cfg)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(unfinished))
	for _, o := range unfinished {
		known[o.ID] = true
		if o.Status == "organizing" || o.Status == "preparing" {
			if err = autoorganize.FinishInterrupted(cfg, o.ID); err != nil {
				return err
			}
		}
	}
	if err = s.processAutoObjections(cfg); err != nil {
		return err
	}
	p, err := autoorganize.ReadPolicy(cfg)
	if err != nil || (!p.Enabled && !p.ObserveSessions) {
		s.autoOrganize.mu.Lock()
		s.autoOrganize.pending = false
		s.autoOrganize.mu.Unlock()
		return err
	}
	if !p.Enabled {
		_, err = s.scanAutoSessions(ctx, cfg, p)
		if err == nil {
			err = s.processAutoObjections(cfg)
		}
		s.autoOrganize.mu.Lock()
		s.autoOrganize.pending = autoorganize.SessionsPending(cfg, p)
		s.autoOrganize.mu.Unlock()
		return err
	}
	operations, err := autoorganize.List(cfg)
	if err != nil {
		return err
	}
	var problems []error
	for _, o := range operations {
		if (o.Status == "organized" || o.Status == "partial") && !o.MemoryInitialized {
			if err = s.initializeAutoProjectMemory(o, p); err != nil {
				problems = append(problems, fmt.Errorf("%s 的项目记录：%w", o.Project, err))
				continue
			}
			if err = autoorganize.MarkMemoryInitialized(cfg, o.ID); err != nil {
				problems = append(problems, fmt.Errorf("%s 的项目记录：%w", o.Project, err))
				continue
			}
		}
	}
	records, err := activity.ReadSince(activity.Dir(cfg), time.Time{})
	if err != nil {
		return err
	}
	sessionRecords, err := s.scanAutoSessions(ctx, cfg, p)
	if err != nil {
		return err
	}
	if err = s.processAutoObjections(cfg); err != nil {
		return err
	}
	s.autoOrganize.mu.Lock()
	s.autoOrganize.pending = autoorganize.SessionsPending(cfg, p)
	s.autoOrganize.mu.Unlock()
	records = append(sessionRecords, records...)
	if e := s.runAutoModel(ctx, cfg, p, records); e != nil {
		problems = append(problems, e)
	}
	if latest, e := autoorganize.ReadPolicy(cfg); e == nil {
		p = latest
	} else {
		problems = append(problems, e)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	modelState, modelErr := autoorganize.ReadModelState(cfg)
	if modelErr != nil {
		problems = append(problems, modelErr)
	}
	for _, c := range autoorganize.Candidates(cfg, p, records) {
		if err = ctx.Err(); err != nil {
			return err
		}
		if c.Reason != "" {
			continue
		}
		current, e := autoorganize.ReadPolicy(cfg)
		if e != nil {
			return e
		}
		if !current.Enabled {
			return nil
		}
		attachModelEvidence(&c, modelState)
		o, e := autoorganize.Execute(ctx, cfg, userHome, current, c)
		if !known[o.ID] && (o.Status == "organized" || o.Status == "partial") && o.Summary != nil && o.Summary.Moved+o.Summary.Copied > 0 {
			known[o.ID] = true
			s.reportAutoOrganize(o, false)
		}
		if e != nil {
			problems = append(problems, fmt.Errorf("%s：%w", c.Project, e))
		}
		if (o.Status == "organized" || o.Status == "partial") && !o.MemoryInitialized {
			if e = s.initializeAutoProjectMemory(o, current); e != nil {
				problems = append(problems, fmt.Errorf("%s 的项目记录：%w", c.Project, e))
				continue
			}
			if e = autoorganize.MarkMemoryInitialized(cfg, o.ID); e != nil {
				problems = append(problems, fmt.Errorf("%s 的项目记录：%w", c.Project, e))
				continue
			}
		}
	}
	return errors.Join(problems...)
}

func (s *ShelfService) initializeAutoProjectMemory(o autoorganize.Operation, p autoorganize.Policy) error {
	key := "root:" + o.Project
	if o.ProjectKey != "" {
		key = o.ProjectKey
	}
	prefix := "auto-" + o.ID
	status, err := s.ProjectMemory(key)
	if err != nil {
		return err
	}
	if status.Revision > 0 && !status.Enabled {
		return errors.New("项目文件记录已暂停，自动整理不会重新开启")
	}
	if !status.Enabled {
		if _, err = s.SetProjectMemoryTracking(key, true, status.Revision, prefix+"-tracking", status.CurrentScopeSHA); err != nil {
			return err
		}
		status, err = s.ProjectMemory(key)
		if err != nil {
			return err
		}
	}
	if !status.Automatic {
		if _, err = s.SetProjectMemoryAutomatic(key, true, status.Revision, prefix+"-watch", status.CurrentScopeSHA); err != nil {
			return err
		}
		status, err = s.ProjectMemory(key)
		if err != nil {
			return err
		}
	}
	for _, source := range p.Sources {
		found := false
		for _, old := range status.Sources {
			if old.Adapter == source.Adapter && old.Path == source.Path && old.Directory && old.Enabled && !old.ScopeChanged {
				found = true
				break
			}
		}
		if found {
			continue
		}
		sourceID := sha256.Sum256([]byte(source.Adapter + "\x00" + source.Path))
		result := s.ApplyProjectMemoryAction(MemoryAction{Kind: "source_on", ProjectKey: key, Revision: status.Revision, OperationID: fmt.Sprintf("%s-source-%x", prefix, sourceID[:8]), ScopeSHA: status.CurrentScopeSHA, SourceAdapter: source.Adapter, SourcePath: source.Path, SourceDirectory: true})
		if result.Error != "" {
			return errors.New(result.Error)
		}
		status, err = s.ProjectMemory(key)
		if err != nil {
			return err
		}
	}
	if _, err = s.ObserveProjectMemory(key, status.Revision, prefix+"-observe", status.CurrentScopeSHA); err != nil {
		return err
	}
	return s.recordAutoMemory(o, "", nil)
}

func autoMemoryPath(p string) string {
	if strings.HasPrefix(p, "/var/") {
		return "/private" + p
	}
	if strings.HasPrefix(p, "/tmp/") {
		return "/private" + p
	}
	return filepath.Clean(p)
}
func (s *ShelfService) recordAutoMemory(o autoorganize.Operation, requestedBy string, source *autoorganize.ObjectionSource) error {
	key := "root:" + o.Project
	if o.ProjectKey != "" {
		key = o.ProjectKey
	}
	finish := s.holdProjectMemory(key)
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return err
	}
	if e.store == nil {
		return errors.New("项目记忆尚未建立")
	}
	view, err := e.store.Read(ctx)
	if err != nil {
		return err
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	auto := projectmemory.AutoEvidence{OperationID: o.ID, ReceiptPath: autoorganize.ReceiptPath(cfg, o.ID), Status: o.Status, RequestedBy: requestedBy}
	if source != nil {
		auto.Objection = &projectmemory.AutoObjection{Adapter: source.Adapter, Session: source.Session, SourcePath: autoMemoryPath(source.SourcePath), Line: source.Line, RecordSHA: source.RecordSHA, Provider: source.Provider, Model: source.Model}
	}
	for _, d := range o.ModelDecisions {
		auto.ModelDecisions = append(auto.ModelDecisions, projectmemory.AutoModelDecision{TaskID: d.TaskID, Provider: d.Provider, Model: d.Model, Reason: d.Reason, InputSHA: d.InputSHA})
	}
	signals := map[string]autoorganize.FileSignal{}
	for _, signal := range o.Signals {
		signals[signal.Path] = signal
	}
	files := map[string]projectmemory.File{}
	for _, file := range view.State.Files {
		files[autoMemoryPath(file.Fact.Path)] = file
	}
	restoredPaths := map[string]bool{}
	if o.Restore != nil {
		for _, path := range o.Restore.RestoredPaths {
			restoredPaths[autoMemoryPath(path)] = true
		}
	}
	if o.Summary != nil {
		for _, item := range o.Summary.Items {
			if item.Error != "" || item.Target == "" || !item.Undoable {
				continue
			}
			if o.Restore != nil && (o.Restore.RestoredPaths != nil || o.Status != "restored") && !restoredPaths[autoMemoryPath(item.Source)] {
				continue
			}
			entry := projectmemory.AutoItem{Before: item.Source, After: item.Target, PlacementReason: o.PlacementReasons[item.Source]}
			if signal, ok := signals[item.Source]; ok {
				entry.Agent = signal.Agent
				entry.Session = signal.Session
				entry.Task = signal.Task
				entry.RecordSHA = signal.RecordSHA
				entry.SourcePath = signal.SourcePath
				entry.Line = signal.Line
			}
			if file, ok := files[autoMemoryPath(item.Target)]; ok {
				f := file
				entry.File = &f
			}
			if o.Restore != nil {
				entry.Before, entry.After = entry.After, entry.Before
			}
			auto.Items = append(auto.Items, entry)
		}
	}
	opID := "auto-history-" + o.ID + "-" + o.Status
	if o.Status == "restore_partial" && o.Restore != nil && o.Restore.RestoredPaths != nil {
		data, _ := json.Marshal(o.Restore)
		sum := sha256.Sum256(data)
		opID += fmt.Sprintf("-%x", sum[:8])
	}
	previous, err := e.store.Operation(ctx, opID)
	if err != nil {
		return err
	}
	if previous.Found {
		return nil
	}
	_, err = e.store.RecordAuto(ctx, view.State.Revision, opID, auto, time.Now())
	return err
}
func (s *ShelfService) startAutoOrganize() {
	a := &s.autoOrganize
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.done = make(chan struct{})
	done := a.done
	go func() {
		defer close(done)
		for {
			err := s.runAutoOrganize(ctx)
			a.mu.Lock()
			if ctx.Err() == nil {
				a.lastError = ""
				if err != nil {
					a.lastError = err.Error()
				}
			}
			delay := 30 * time.Second
			if a.interval > 0 {
				delay = a.interval
			}
			if a.pending {
				delay = 2 * time.Second
			}
			a.mu.Unlock()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}
func (s *ShelfService) closeAutoOrganize() {
	a := &s.autoOrganize
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	a.mu.Lock()
	a.cancel = nil
	a.done = nil
	a.mu.Unlock()
}
