package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/agents"
)

func discoveryLoopFixture(t *testing.T) (*ShelfService, string, *agentdiscovery.Store) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentdiscovery.NewStore(filepath.Join(home, "checks"))
	if err != nil {
		t.Fatal(err)
	}
	s := &ShelfService{discoveries: agentDiscoveryConnection{store: store, scanner: agentdiscovery.Scanner{Home: home}}}
	return s, home, store
}

func TestAgentDiscoveryLoopDefaultAndLifecycle(t *testing.T) {
	s, home, store := discoveryLoopFixture(t)
	ctx := context.Background()
	if err := s.agentDiscoveryTick(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "checks")); !os.IsNotExist(err) {
		t.Fatal("disabled loop allocated state", err)
	}
	s.startAgentDiscovery()
	s.discoveries.mu.Lock()
	first := s.discoveries.done
	s.discoveries.mu.Unlock()
	s.startAgentDiscovery()
	s.discoveries.mu.Lock()
	second := s.discoveries.done
	s.discoveries.mu.Unlock()
	if first != second {
		t.Fatal("duplicate worker")
	}
	state, err := s.AgentEnrollmentStatus()
	if err != nil || !state.BackgroundRunning {
		t.Fatal("running state", err)
	}
	s.closeAgentDiscovery()
	select {
	case <-first:
	default:
		t.Fatal("shutdown did not join worker")
	}
	state, err = s.AgentEnrollmentStatus()
	if err != nil || state.BackgroundRunning {
		t.Fatal("stopped state", err)
	}
	r, err := store.Read(ctx)
	if err != nil || r.Revision != 0 {
		t.Fatal("default worker scanned", err)
	}
	s.closeAgentDiscovery()
}

func TestAgentDiscoveryLoopDueNewToolAndPause(t *testing.T) {
	if !agents.AutomaticEnrollmentAvailable() {
		t.Skip("secure enrollment unsupported")
	}
	s, home, store := discoveryLoopFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	r, err := s.checkAgentDiscovery(ctx, true, now)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.SetEnrollmentPolicy(ctx, r.Receipt.Revision, true, []string{"codex", "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.agentDiscoveryTick(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	before, err := store.Read(ctx)
	if err != nil || before.Revision != policy.Revision {
		t.Fatal("not-due tick changed receipt", err)
	}
	if err := s.agentDiscoveryTick(ctx, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(ctx)
	if err != nil || after.Revision != policy.Revision+3 {
		t.Fatalf("expected one catchup and intent/result: %+v %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "AGENTS.md")); err != nil {
		t.Fatal("new tool missed", err)
	}
	if err := s.agentDiscoveryTick(ctx, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, _ := store.Read(ctx)
	if again.Revision != after.Revision {
		t.Fatal("repeated same-day processing")
	}
	paused, err := store.SetEnrollmentPolicy(ctx, again.Revision, false, []string{"codex", "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.agentDiscoveryTick(ctx, now.Add(14*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	final, _ := store.Read(ctx)
	if final.Revision != paused.Revision {
		t.Fatal("disabled loop scanned")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("disabled loop enrolled", err)
	}
}

func TestAgentDiscoveryLoopCancellationAndError(t *testing.T) {
	s, home, _ := discoveryLoopFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.agentDiscoveryTick(ctx, time.Now()); err == nil {
		t.Fatal("cancellation ignored")
	}
	// A file where the state directory should be must be visible as an error,
	// never repaired by overwriting it.
	if err := os.WriteFile(filepath.Join(home, "checks"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.startAgentDiscovery()
	defer s.closeAgentDiscovery()
	deadline := time.After(2 * time.Second)
	for {
		s.discoveries.mu.Lock()
		message := s.discoveries.lastError
		s.discoveries.mu.Unlock()
		if message != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("background error was not recorded")
		case <-time.After(5 * time.Millisecond):
		}
	}
	b, err := os.ReadFile(filepath.Join(home, "checks"))
	if err != nil || string(b) != "keep" {
		t.Fatal("error handling modified state", err)
	}
}
