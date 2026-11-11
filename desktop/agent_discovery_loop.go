package main

import (
	"context"
	"time"
)

// One cancellable worker belongs to this application process. An absent or
// disabled persistent grant never creates a scan, directory, or client rule.
func (s *ShelfService) startAgentDiscovery() {
	s.discoveries.mu.Lock()
	defer s.discoveries.mu.Unlock()
	if s.discoveries.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, wake := make(chan struct{}), make(chan struct{}, 1)
	s.discoveries.cancel, s.discoveries.done, s.discoveries.wake = cancel, done, wake
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			round, finish := context.WithTimeout(ctx, 30*time.Second)
			err := s.agentDiscoveryTick(round, time.Now())
			finish()
			if ctx.Err() != nil {
				return
			}
			s.discoveries.mu.Lock()
			s.discoveries.lastError = ""
			if err != nil {
				s.discoveries.lastError = err.Error()
			}
			s.discoveries.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-wake:
			}
		}
	}()
}

func (s *ShelfService) agentDiscoveryTick(ctx context.Context, now time.Time) error {
	store, scanner, err := s.agentDiscoveryEnv()
	if err != nil {
		return err
	}
	result, err := store.CheckAutomatic(ctx, scanner, now)
	if err != nil || result.Receipt.Policy == nil || !result.Receipt.Policy.Enabled {
		return err
	}
	if result.Ran {
		_, err = store.ReconcileEnrollment(ctx)
	} else {
		_, err = store.RecoverEnrollment(ctx)
	}
	return err
}

func (s *ShelfService) wakeAgentDiscovery() {
	s.discoveries.mu.Lock()
	defer s.discoveries.mu.Unlock()
	if s.discoveries.wake != nil {
		select {
		case s.discoveries.wake <- struct{}{}:
		default:
		}
	}
}

func (s *ShelfService) closeAgentDiscovery() {
	s.discoveries.mu.Lock()
	cancel, done := s.discoveries.cancel, s.discoveries.done
	s.discoveries.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	s.discoveries.mu.Lock()
	if s.discoveries.done == done {
		s.discoveries.cancel, s.discoveries.done, s.discoveries.wake = nil, nil, nil
	}
	s.discoveries.mu.Unlock()
}
