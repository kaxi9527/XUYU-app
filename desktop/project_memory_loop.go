package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	filehome "github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

const memoryCheckInterval = 5 * time.Minute

type memoryAttempt struct {
	key string
	at  time.Time
	err string
}

type memoryObservationConnection struct {
	mu                  sync.Mutex
	tick                sync.Mutex
	cancel              context.CancelFunc
	done                chan struct{}
	wake                chan struct{}
	closing             bool
	started             time.Time
	cursor              string
	holds               map[string]int
	attempts            map[string]memoryAttempt
	watches             map[string]*memoryWatchState
	activeKey, activeID string
	activeCancel        context.CancelFunc
	lastError           string
}

// Only one cancellable worker belongs to this application. It has no authority
// to create a project or convert a manual tracking grant into an automatic one.
func (s *ShelfService) startProjectMemoryObservation() {
	m := &s.memories
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, wake := make(chan struct{}), make(chan struct{}, 1)
	m.cancel, m.done, m.wake = cancel, done, wake
	m.closing, m.started = false, time.Now()
	m.attempts = map[string]memoryAttempt{}
	go func() {
		defer close(done)
		defer s.closeMemoryWatches()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		nextAudit := time.Now().Add(15 * time.Second)
		for {
			if ctx.Err() != nil {
				return
			}
			round, finish := context.WithTimeout(ctx, 85*time.Second)
			err := s.projectMemoryTick(round, time.Now())
			finish()
			m.mu.Lock()
			m.lastError = ""
			if err != nil && ctx.Err() == nil {
				m.lastError = err.Error()
			}
			m.mu.Unlock()
		wait:
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					signal := s.pollMemorySignals(now)
					if !now.Before(nextAudit) {
						nextAudit = now.Add(15 * time.Second)
						break wait
					}
					if signal {
						break wait
					}
				case <-wake:
					break wait
				}
			}
		}
	}()
}

func (s *ShelfService) closeProjectMemoryObservation() {
	m := &s.memories
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	if cancel != nil {
		m.closing = true
	}
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	m.mu.Lock()
	if m.done == done {
		m.cancel, m.done, m.wake = nil, nil, nil
		m.closing = false
	}
	m.mu.Unlock()
}

// The hold is installed before cancelling: no new scan can slip between a
// cancellation and the user's persisted pause/change. The store lock is the
// cross-process authority; a stale revision is never silently overwritten.
func (s *ShelfService) holdProjectMemory(key string) func() {
	m := &s.memories
	m.mu.Lock()
	if m.holds == nil {
		m.holds = map[string]int{}
	}
	m.holds[key]++
	for id, w := range m.watches {
		if w.key == key {
			s.removeMemoryWatchLocked(id)
		}
	}
	if m.activeKey == key && m.activeCancel != nil {
		m.activeCancel()
	}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.holds[key]--
		if m.holds[key] == 0 {
			delete(m.holds, key)
		}
		for id, attempt := range m.attempts {
			if attempt.key == key {
				delete(m.attempts, id)
			}
		}
		if m.wake != nil {
			select {
			case m.wake <- struct{}{}:
			default:
			}
		}
		m.mu.Unlock()
	}
}

func (s *ShelfService) projectMemoryRuntime(id string) (bool, bool, string, string) {
	m := &s.memories
	m.mu.Lock()
	defer m.mu.Unlock()
	running := m.cancel != nil && !m.closing
	a := m.attempts[id]
	err := a.err
	if m.lastError != "" {
		err = m.lastError
	}
	next := ""
	if running && !a.at.IsZero() {
		next = a.at.Add(memoryCheckInterval).UTC().Format(time.RFC3339Nano)
	}
	return running, m.activeID == id, err, next
}

func (s *ShelfService) SetProjectMemoryAutomatic(key string, enabled bool, revision uint64, operationID, scopeSHA string) (projectmemory.Result, error) {
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
	if e.store == nil {
		return projectmemory.Result{}, errors.New("请先开启此项目的文件记录")
	}
	result, err := e.store.SetAutomatic(ctx, revision, operationID, scopeSHA, enabled, time.Now())
	if err == nil {
		s.memories.mu.Lock()
		delete(s.memories.attempts, e.project.ID)
		s.memories.mu.Unlock()
	}
	return result, err
}

func (s *ShelfService) memoryCandidates(ctx context.Context) ([]versions.Project, error) {
	e, err := s.env()
	if err != nil {
		return nil, err
	}
	control := filepath.Join(e.Config.Root, e.Config.Names.Control)
	base := filepath.Join(control, "作品版本")
	if err = filehome.CheckPath(e.Config.Root, base); err != nil {
		return nil, err
	}
	store, err := versions.NewStore(base)
	if err != nil {
		return nil, err
	}
	catalog, err := store.Read(ctx)
	if err != nil {
		return nil, err
	}
	projects := append([]versions.Project{}, catalog.Projects...)
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

func (s *ShelfService) projectMemoryTick(ctx context.Context, now time.Time) error {
	m := &s.memories
	m.tick.Lock()
	defer m.tick.Unlock()
	projects, err := s.memoryCandidates(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.started.IsZero() {
		m.started = now
	}
	started, cursor := m.started, m.cursor
	if m.attempts == nil {
		m.attempts = map[string]memoryAttempt{}
	}
	known := map[string]bool{}
	for _, p := range projects {
		known[p.ID] = true
	}
	for id := range m.attempts {
		if !known[id] {
			delete(m.attempts, id)
		}
	}
	for id := range m.watches {
		if !known[id] {
			s.removeMemoryWatchLocked(id)
		}
	}
	m.mu.Unlock()
	if len(projects) == 0 {
		return nil
	}
	start := sort.Search(len(projects), func(i int) bool { return projects[i].ID > cursor }) % len(projects)
	scanned := 0
	for i := 0; i < len(projects) && i < 20 && scanned < 2; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := projects[(start+i)%len(projects)]
		m.mu.Lock()
		m.cursor = p.ID
		held, last := m.holds[p.LegacyKey] > 0, m.attempts[p.ID]
		signaled := memorySignalDue(m.watches[p.ID], last, now)
		m.mu.Unlock()
		if held || p.LegacyKey == "" || !signaled && !last.at.IsZero() && now.Sub(last.at) < memoryCheckInterval {
			continue
		}
		job, cancel := context.WithTimeout(ctx, 40*time.Second)
		ran, jobErr := s.checkAutomaticProjectSignal(job, p, now, started, signaled)
		cancel()
		if ran {
			scanned++
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(jobErr, projectmemory.ErrConflict) || errors.Is(jobErr, context.Canceled) {
			continue
		}
		if ran || jobErr != nil {
			a := memoryAttempt{key: p.LegacyKey, at: now}
			if jobErr != nil {
				a.err = jobErr.Error()
			}
			m.mu.Lock()
			m.attempts[p.ID] = a
			m.mu.Unlock()
		}
	}
	return nil
}

func (s *ShelfService) checkAutomaticProject(ctx context.Context, p versions.Project, now, started time.Time) (bool, error) {
	return s.checkAutomaticProjectSignal(ctx, p, now, started, false)
}

func (s *ShelfService) checkAutomaticProjectSignal(ctx context.Context, p versions.Project, now, started time.Time, signaled bool) (bool, error) {
	e, err := s.memoryEnvironment(ctx, p.LegacyKey)
	if err != nil {
		return false, err
	}
	if e.project == nil || e.project.ID != p.ID || e.store == nil {
		return false, errors.New("项目身份已变化，自动检查等待核对")
	}
	v, err := e.store.Read(ctx)
	if err != nil {
		return false, err
	}
	if !v.State.Enabled || !v.State.Automatic {
		s.memories.mu.Lock()
		s.removeMemoryWatchLocked(p.ID)
		s.memories.mu.Unlock()
		return false, nil
	}
	if v.State.Scope.Digest() != e.tracking.Digest() {
		s.memories.mu.Lock()
		s.removeMemoryWatchLocked(p.ID)
		s.memories.mu.Unlock()
		return false, errors.New("项目范围已变化，请核对范围后再继续自动检查")
	}
	watchScope := projectmemory.Scope{Roots: append([]string{}, e.tracking.Roots...), Files: append([]string{}, e.tracking.Files...)}
	for _, source := range v.State.Sources {
		if source.Enabled && source.ScopeSHA == e.tracking.Digest() {
			if source.Directory {
				watchScope.Roots = append(watchScope.Roots, source.Path)
			} else {
				watchScope.Files = append(watchScope.Files, source.Path)
			}
		}
	}
	s.ensureMemoryWatch(p, watchScope, e.control)
	observed, _ := time.Parse(time.RFC3339Nano, v.State.LastObservedAt)
	granted, _ := time.Parse(time.RFC3339Nano, v.State.AutomaticSince)
	catchup := observed.IsZero() || observed.Before(started) || observed.Before(granted) || observed.After(now)
	if !signaled && !catchup && now.Sub(observed) < memoryCheckInterval {
		return false, nil
	}
	m := &s.memories
	m.mu.Lock()
	if m.closing || m.holds[p.LegacyKey] > 0 || ctx.Err() != nil {
		m.mu.Unlock()
		return false, nil
	}
	job, cancel := context.WithCancel(ctx)
	m.activeKey, m.activeID, m.activeCancel = p.LegacyKey, p.ID, cancel
	// Consume only already-polled hints. A callback during scanning remains
	// queued in the native stream for the next pass.
	if w := m.watches[p.ID]; w != nil {
		w.first, w.latest = time.Time{}, time.Time{}
	}
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.activeKey, m.activeID, m.activeCancel = "", "", nil
		m.mu.Unlock()
	}()
	op := fmt.Sprintf("automatic-%s-%d", p.ID, v.State.Revision)
	result, err := e.store.ObserveAutomatic(job, v.State.Revision, op, e.tracking.Digest())
	if err == nil {
		more := false
		for _, event := range result.Record.Events {
			if event.SourceRead != nil && event.SourceRead.More {
				more = true
			}
		}
		if more {
			m.mu.Lock()
			if w := m.watches[p.ID]; w != nil {
				w.first, w.latest = now.Add(-time.Second), now.Add(-time.Second)
			}
			m.mu.Unlock()
		}
	}
	return true, err
}
