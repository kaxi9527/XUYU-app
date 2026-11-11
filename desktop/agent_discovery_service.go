package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/agents"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

type agentDiscoveryConnection struct {
	mu        sync.Mutex
	store     *agentdiscovery.Store
	scanner   agentdiscovery.Scanner
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
	lastError string
}

// Bind the service to its initial local scope; changing environment/config
// elsewhere cannot redirect an outstanding preview to another user's rules.
func (s *ShelfService) agentDiscoveryEnv() (*agentdiscovery.Store, agentdiscovery.Scanner, error) {
	s.discoveries.mu.Lock()
	defer s.discoveries.mu.Unlock()
	if s.discoveries.store != nil {
		return s.discoveries.store, s.discoveries.scanner, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, agentdiscovery.Scanner{}, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, agentdiscovery.Scanner{}, err
	}
	store, err := agentdiscovery.NewStore(filepath.Join(filepath.Dir(config.DefaultPath(home)), "agent-discovery"))
	if err != nil {
		return nil, agentdiscovery.Scanner{}, err
	}
	scanner := agentdiscovery.Scanner{Home: home}
	if runtime.GOOS == "darwin" {
		scanner.ApplicationRoots = []string{"/Applications", filepath.Join(home, "Applications")}
	}
	s.discoveries.store, s.discoveries.scanner = store, scanner
	return store, scanner, nil
}

type AgentEnrollmentState struct {
	Supported         bool                   `json:"supported"`
	AutomaticAgents   []string               `json:"automaticAgents"`
	BackgroundRunning bool                   `json:"backgroundRunning"`
	LastError         string                 `json:"lastError,omitempty"`
	Receipt           agentdiscovery.Receipt `json:"receipt"`
}

func (s *ShelfService) AgentEnrollmentStatus() (AgentEnrollmentState, error) {
	state := AgentEnrollmentState{Supported: agents.AutomaticEnrollmentAvailable(), AutomaticAgents: agents.AutomaticAgents()}
	store, _, err := s.agentDiscoveryEnv()
	if err != nil {
		return state, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state.Receipt, err = store.Read(ctx)
	s.discoveries.mu.Lock()
	state.BackgroundRunning = s.discoveries.cancel != nil
	state.LastError = s.discoveries.lastError
	s.discoveries.mu.Unlock()
	// This is the application-owned loop, not a login item or OS daemon.
	return state, err
}

func (s *ShelfService) CheckAgentDiscovery(force bool) (agentdiscovery.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.checkAgentDiscovery(ctx, force, time.Now())
}

func (s *ShelfService) checkAgentDiscovery(ctx context.Context, force bool, now time.Time) (agentdiscovery.Result, error) {
	store, scanner, err := s.agentDiscoveryEnv()
	if err != nil {
		return agentdiscovery.Result{}, err
	}
	result, err := store.Check(ctx, scanner, now, force)
	if err != nil {
		return result, err
	}
	if result.Ran {
		result.Receipt, err = store.ReconcileEnrollment(ctx)
	} else {
		result.Receipt, err = store.RecoverEnrollment(ctx)
	}
	return result, err
}

func (s *ShelfService) PreviewAgentEnrollment(agent string) (agentdiscovery.EnrollmentReview, error) {
	store, _, err := s.agentDiscoveryEnv()
	if err != nil {
		return agentdiscovery.EnrollmentReview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return store.EnrollmentPreview(ctx, agent)
}

func (s *ShelfService) ApplyAgentEnrollment(revision uint64, agent, digest string) (agentdiscovery.Receipt, error) {
	store, _, err := s.agentDiscoveryEnv()
	if err != nil {
		return agentdiscovery.Receipt{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return store.Enroll(ctx, revision, agent, digest)
}

func (s *ShelfService) SetAgentEnrollmentPolicy(revision uint64, enabled bool, allowed, ignored []string) (agentdiscovery.Receipt, error) {
	store, _, err := s.agentDiscoveryEnv()
	if err != nil {
		return agentdiscovery.Receipt{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := store.SetEnrollmentPolicy(ctx, revision, enabled, allowed, ignored)
	if err != nil {
		return r, err
	}
	s.wakeAgentDiscovery()
	if enabled {
		return store.ReconcileEnrollment(ctx)
	}
	return r, nil
}
