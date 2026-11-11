package main

import (
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

const memorySignalInterval = 10 * time.Second

type memoryNativeWatch interface {
	Take() bool
	Close()
}

type memoryWatchState struct {
	key, scope, err string
	native          memoryNativeWatch
	first, latest   time.Time
	created         time.Time
}

// Signals never authorize scanning and never become file facts. The ordinary
// snapshot scanner and durable grant remain the only source of recorded facts.
func (s *ShelfService) ensureMemoryWatch(p versions.Project, scope projectmemory.Scope, exclude string) {
	m := &s.memories
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil || m.closing || m.holds[p.LegacyKey] > 0 {
		return
	}
	if m.watches == nil {
		m.watches = map[string]*memoryWatchState{}
	}
	if w := m.watches[p.ID]; w != nil && w.scope == scope.Digest() && time.Since(w.created) < memoryCheckInterval {
		return
	}
	s.removeMemoryWatchLocked(p.ID)
	w := &memoryWatchState{key: p.LegacyKey, scope: scope.Digest(), created: time.Now()}
	if len(m.watches) >= 128 {
		// No unbounded native resources; the normal periodic scanner still runs.
		return
	}
	m.watches[p.ID] = w
	native, err := newMemoryNativeWatch(scope, exclude)
	if err != nil {
		w.err = err.Error()
		return
	}
	w.native = native
}

func (s *ShelfService) removeMemoryWatchLocked(id string) {
	if w := s.memories.watches[id]; w != nil && w.native != nil {
		w.native.Close()
	}
	delete(s.memories.watches, id)
}

func (s *ShelfService) closeMemoryWatches() {
	s.memories.mu.Lock()
	defer s.memories.mu.Unlock()
	for id := range s.memories.watches {
		s.removeMemoryWatchLocked(id)
	}
}

func (s *ShelfService) pollMemorySignals(now time.Time) bool {
	m := &s.memories
	m.mu.Lock()
	defer m.mu.Unlock()
	due := false
	for id, w := range m.watches {
		if w.native != nil && w.native.Take() {
			if w.first.IsZero() {
				w.first = now
			}
			w.latest = now
		}
		if memorySignalDue(w, m.attempts[id], now) {
			due = true
		}
	}
	return due
}

func memorySignalDue(w *memoryWatchState, last memoryAttempt, now time.Time) bool {
	if w == nil || w.first.IsZero() {
		return false
	}
	// Errors retain the existing five-minute backoff, including capacity errors.
	gap := memorySignalInterval
	if last.err != "" {
		gap = memoryCheckInterval
	}
	return now.Sub(last.at) >= gap && (now.Sub(w.latest) >= time.Second || now.Sub(w.first) >= 5*time.Second)
}

func (s *ShelfService) memoryWatchRuntime(id string) (bool, string) {
	m := &s.memories
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.watches[id]; w != nil {
		return w.native != nil && !m.closing, w.err
	}
	if len(m.watches) >= 128 {
		return false, "实时监听数量已达上限；继续定期检查"
	}
	return false, ""
}

var errMemoryWatchUnavailable = errors.New("实时监听暂不可用；继续定期检查已授权范围")
