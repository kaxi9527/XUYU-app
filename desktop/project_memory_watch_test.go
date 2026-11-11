package main

import (
	"context"
	"testing"
	"time"
)

type testMemoryWatch struct{ changed, closed bool }

func (w *testMemoryWatch) Take() bool { changed := w.changed; w.changed = false; return changed }
func (w *testMemoryWatch) Close()     { w.closed = true }

func TestMemorySignalsCoalesceBackoffAndRelease(t *testing.T) {
	s := &ShelfService{}
	now := time.Now()
	native := &testMemoryWatch{changed: true}
	w := &memoryWatchState{key: "root:test", native: native}
	s.memories.watches = map[string]*memoryWatchState{"p": w}
	s.memories.attempts = map[string]memoryAttempt{"p": {at: now.Add(-time.Minute)}}
	if s.pollMemorySignals(now) || !s.pollMemorySignals(now.Add(time.Second)) {
		t.Fatal("burst did not settle before becoming due")
	}
	s.memories.attempts["p"] = memoryAttempt{at: now}
	if s.pollMemorySignals(now.Add(9*time.Second)) || !s.pollMemorySignals(now.Add(10*time.Second)) {
		t.Fatal("hot project bypassed scan rate limit")
	}
	s.memories.attempts["p"] = memoryAttempt{at: now, err: "budget"}
	if s.pollMemorySignals(now.Add(time.Minute)) {
		t.Fatal("event storm bypassed error backoff")
	}
	release := s.holdProjectMemory("root:test")
	if !native.closed || len(s.memories.watches) != 0 {
		t.Fatal("pause coordination retained watcher")
	}
	release()
	s.closeMemoryWatches()
}

func TestMemorySignalCannotBypassRevocation(t *testing.T) {
	s, _, _, _ := versionFixture(t)
	key := "root:品牌项目"
	st := enableAutomaticMemory(t, s, key)
	ctx := context.Background()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectMemoryAutomatic(key, false, st.Revision, "disable-before-signal", st.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	ran, err := s.checkAutomaticProjectSignal(ctx, *e.project, time.Now(), time.Now(), true)
	if err != nil || ran {
		t.Fatal("signal bypassed durable authorization", ran, err)
	}
}
