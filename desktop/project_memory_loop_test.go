package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func enableAutomaticMemory(t *testing.T, s *ShelfService, key string) ProjectMemoryStatus {
	t.Helper()
	status, err := s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision == 0 {
		if _, err = s.SetProjectMemoryTracking(key, true, 0, "enable-"+key, status.CurrentScopeSHA); err != nil {
			t.Fatal(err)
		}
		status, err = s.ProjectMemory(key)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetProjectMemoryAutomatic(key, true, status.Revision, "automatic-"+key, status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	status, err = s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestProjectMemoryLoopDefaultAndLifecycle(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	if err := s.projectMemoryTick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, cfg.Names.Control)); !os.IsNotExist(err) {
		t.Fatal("default observation allocated state", err)
	}
	s.startProjectMemoryObservation()
	s.memories.mu.Lock()
	first := s.memories.done
	s.memories.mu.Unlock()
	s.startProjectMemoryObservation()
	s.memories.mu.Lock()
	second := s.memories.done
	s.memories.mu.Unlock()
	if first != second {
		t.Fatal("duplicate worker")
	}
	if running, _, _, _ := s.projectMemoryRuntime("none"); !running {
		t.Fatal("worker not visible")
	}
	s.closeProjectMemoryObservation()
	select {
	case <-first:
	default:
		t.Fatal("shutdown did not await worker")
	}
	if running, _, _, _ := s.projectMemoryRuntime("none"); running {
		t.Fatal("worker still reported active")
	}
	s.closeProjectMemoryObservation()
}

func TestProjectMemoryLoopGrantDueRestartAndPause(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	ctx := context.Background()
	now := time.Now()
	file := versionFixtureFile(t, project, "成果.txt", "first")
	status, _ := s.ProjectMemory(key)
	if _, err := s.SetProjectMemoryTracking(key, true, 0, "manual", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	if err := s.projectMemoryTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if status, err := s.ProjectMemory(key); err != nil || status.Revision != 1 || status.Automatic {
		t.Fatal("manual grant scanned", status, err)
	}
	status = enableAutomaticMemory(t, s, key)
	if err := s.projectMemoryTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectMemory(key)
	if err != nil || status.Revision != 3 || status.Tracked != 2 {
		t.Fatal(status, err)
	}
	if err = os.WriteFile(file, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.projectMemoryTick(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.ProjectMemory(key); current.Revision != 3 {
		t.Fatal("checked before interval", current)
	}
	if err = s.projectMemoryTick(ctx, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err = s.ProjectMemory(key)
	if err != nil || status.Revision != 4 {
		t.Fatal(status, err)
	}
	page, err := s.ProjectMemoryEvents(key, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, e := range page.Items {
		if e.Kind == "content_changed" {
			changed = true
			if e.Actor != "unknown" {
				t.Fatal("invented actor", e)
			}
		}
	}
	if !changed {
		t.Fatal("automatic rewrite was not recorded")
	}
	// A new service uses the existing project identity and persisted grant.
	restarted := &ShelfService{}
	if err = restarted.projectMemoryTick(ctx, now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err = restarted.ProjectMemory(key)
	if err != nil || status.Revision != 5 {
		t.Fatal("startup catchup missing", status, err)
	}
	if _, err = restarted.SetProjectMemoryTracking(key, false, 5, "pause", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	if err = restarted.projectMemoryTick(ctx, now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err = restarted.ProjectMemory(key)
	if err != nil || status.Revision != 6 || status.Enabled {
		t.Fatal("pause did not hold", status, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "second" {
		t.Fatal("observer changed source", err)
	}
}

func TestProjectMemoryLoopRoundRobinAndFaultIsolation(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	ctx := context.Background()
	now := time.Now()
	keys := []string{"root:品牌项目", "root:第二项目", "root:第三项目", "root:第四项目"}
	for _, key := range keys {
		path := filepath.Join(cfg.Root, key[len("root:"):])
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		versionFixtureFile(t, path, "result.txt", key)
		enableAutomaticMemory(t, s, key)
	}
	count := func() int {
		n := 0
		for _, key := range keys {
			status, err := s.ProjectMemory(key)
			if err != nil {
				t.Fatal(err)
			}
			if status.LastObservedAt != "" {
				n++
			}
		}
		return n
	}
	if err := s.projectMemoryTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatal("unbounded batch", n)
	}
	if err := s.projectMemoryTick(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 4 {
		t.Fatal("starved project", n)
	}
	e, err := s.memoryEnvironment(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	narrow := projectmemory.Scope{Files: []string{filepath.Join(e.scope.project.Path, "result.txt")}}
	if _, err = e.store.SetTracking(ctx, v.State.Revision, "old-narrow-scope", narrow, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = s.projectMemoryTick(ctx, now.Add(time.Duration(6+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.ProjectMemory(keys[0])
	if err != nil || !status.ScopeChanged || status.BackgroundError == "" || status.Revision != 4 {
		t.Fatal("scope changed without waiting", status, err)
	}
	for _, key := range keys[1:] {
		status, err = s.ProjectMemory(key)
		if err != nil || status.Revision < 4 {
			t.Fatal("bad project blocked another", key, status, err)
		}
	}
}

func TestProjectMemoryLoopHoldCancelsAndOfflineDoesNotDelete(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	ctx := context.Background()
	now := time.Now()
	versionFixtureFile(t, project, "result.txt", "preserve")
	enableAutomaticMemory(t, s, key)
	job, cancel := context.WithCancel(ctx)
	defer cancel()
	s.memories.mu.Lock()
	s.memories.activeKey = key
	s.memories.activeCancel = cancel
	s.memories.mu.Unlock()
	release := s.holdProjectMemory(key)
	select {
	case <-job.Done():
	default:
		t.Fatal("user action did not cancel scan")
	}
	if err := s.projectMemoryTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, _ := s.ProjectMemory(key)
	if status.Revision != 2 {
		t.Fatal("scan passed user hold")
	}
	s.memories.mu.Lock()
	s.memories.activeKey = ""
	s.memories.activeCancel = nil
	s.memories.mu.Unlock()
	release()
	if err := s.projectMemoryTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(project, project+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := s.projectMemoryTick(ctx, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectMemory(key)
	if err != nil || status.ObservationComplete || status.Missing != 0 || status.Tracked != 2 {
		t.Fatal(status, err)
	}
	data, err := os.ReadFile(filepath.Join(project+"-offline", "result.txt"))
	if err != nil || string(data) != "preserve" {
		t.Fatal(err)
	}
}
