package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

type ModelCleanupResult struct {
	Pending *int   `json:"pending,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Cleanup is independently retryable and only touches previously journaled,
// inactive references. It never invents a persisted configuration receipt.
func (s *ShelfService) RetryModelCredentialCleanup() ModelCleanupResult {
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	if err != nil {
		b.mu.Unlock()
		return ModelCleanupResult{Error: modelPublicError(err).Error()}
	}
	if b.cleanupCancel != nil {
		b.mu.Unlock()
		return ModelCleanupResult{Error: "正在清理旧凭据，请稍候"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	b.cleanupCancel = cancel
	b.active.Add(1)
	b.mu.Unlock()
	defer func() { cancel(); b.mu.Lock(); b.cleanupCancel = nil; b.mu.Unlock(); b.active.Done() }()
	err = store.Cleanup(ctx)
	result := ModelCleanupResult{}
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	state, readErr := store.Read(readCtx)
	if readErr == nil {
		result.Pending = &state.PendingCredentialCleanup
	}
	if err != nil {
		result.Error = modelPublicError(err).Error()
	} else if readErr != nil {
		result.Error = modelPublicError(readErr).Error()
	}
	return result
}

// The identity is made only from non-secret action fields. A repeated in-flight
// verification shares one request; it cannot silently spend twice on a probe.
func (s *ShelfService) modelAction(kind, id string, identity any, action func(context.Context, *models.Store) (models.Receipt, error)) ModelActionResult {
	return s.runModelAction(kind, id, identity, func(ctx context.Context, store *models.Store) ModelActionResult {
		receipt, err := action(ctx, store)
		return modelActionOutcome(store, id, receipt, err)
	})
}

func (s *ShelfService) runModelAction(kind, id string, identity any, action func(context.Context, *models.Store) ModelActionResult) ModelActionResult {
	if !modelOperationID(id) {
		return ModelActionResult{Error: "操作标识无效"}
	}
	encoded, _ := json.Marshal(identity)
	key := string(encoded)
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	if err != nil {
		b.mu.Unlock()
		return ModelActionResult{Error: modelPublicError(err).Error()}
	}
	if b.jobs == nil {
		b.jobs = map[string]*modelRun{}
	}
	for key, run := range b.jobs {
		if run.completed && time.Now().After(run.expires) {
			delete(b.jobs, key)
		}
	}
	if run := b.jobs[id]; run != nil {
		b.mu.Unlock()
		if run.kind != kind || run.identity != key {
			return ModelActionResult{Error: models.ErrReused.Error()}
		}
		<-run.done
		return run.result
	}
	active := 0
	stopping := false
	for _, run := range b.jobs {
		if !run.completed {
			active++
			stopping = stopping || run.kind == "tool"
		}
	}
	if kind == "tool" && stopping {
		b.mu.Unlock()
		return ModelActionResult{Error: "正在切回工具模式，请稍候"}
	}
	// Stopping must remain available even when all probe slots are occupied.
	if active >= modelJobLimit && kind != "tool" {
		b.mu.Unlock()
		return ModelActionResult{Error: "已有多个模型操作在进行，请稍后再试"}
	}
	if len(b.jobs) >= 128 {
		for key, run := range b.jobs {
			if run.completed {
				delete(b.jobs, key)
				break
			}
		}
	}
	if kind == "tool" {
		for _, run := range b.jobs {
			if run.kind == "verify" || run.kind == "list" || run.kind == "draft-list" || run.kind == "draft-verify" {
				run.cancel()
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	run := &modelRun{operationID: id, kind: kind, identity: key, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	b.jobs[id] = run
	b.active.Add(1)
	b.mu.Unlock()
	defer b.active.Done()
	defer cancel()
	result := action(ctx, store)
	b.mu.Lock()
	run.result, run.completed = result, true
	run.expires = time.Now().Add(5 * time.Minute)
	close(run.done)
	b.mu.Unlock()
	return result
}

func (s *ShelfService) ListConnectionModels(providerID string, revision uint64, cursor, requestID string) ModelActionResult {
	if len(cursor) > 2048 {
		return ModelActionResult{Error: "模型列表游标无效"}
	}
	result := s.runModelAction("list", requestID, []any{providerID, revision, cursor}, func(ctx context.Context, store *models.Store) ModelActionResult {
		page, err := store.DiscoverModels(ctx, revision, providerID, cursor)
		if err == nil {
			return ModelActionResult{Models: &page, Revision: revision}
		}
		result := ModelActionResult{Error: modelPublicError(err).Error()}
		var problem *models.ConnectionProblem
		if errors.As(err, &problem) && problem != nil && problem.Valid() {
			copy := *problem
			result.Problem = &copy
		}
		return result
	})
	// Cached duplicate results obey the current configuration revision too.
	if result.Models != nil {
		state, err := s.ModelConnections()
		if err != nil {
			return ModelActionResult{Error: modelPublicError(err).Error()}
		}
		if state.State.Revision != revision {
			return ModelActionResult{Error: models.ErrConflict.Error()}
		}
	}
	return result
}

func (s *ShelfService) CancelModelDiscovery(requestID string) bool {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.jobs[requestID]
	if run == nil || run.kind != "list" || run.completed {
		return false
	}
	run.cancel()
	return true
}

func (s *ShelfService) VerifyModelConnection(modelID string, revision uint64, operationID string) ModelActionResult {
	return s.modelAction("verify", operationID, []any{revision, modelID}, func(ctx context.Context, store *models.Store) (models.Receipt, error) {
		result, err := store.VerifyModel(ctx, revision, operationID, modelID)
		return result.Receipt, err
	})
}

func (s *ShelfService) CancelModelVerification(operationID string) bool {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.jobs[operationID]
	if run == nil || run.kind != "verify" || run.completed {
		return false
	}
	run.cancel()
	return true
}

func (s *ShelfService) SetWorkShelfMode(mode, modelID string, revision uint64, operationID string) ModelActionResult {
	return s.modelAction(mode, operationID, []any{revision, mode, modelID}, func(ctx context.Context, store *models.Store) (models.Receipt, error) {
		return store.SetMode(ctx, revision, operationID, mode, modelID)
	})
}

func (s *ShelfService) DeleteModelProvider(providerID string, revision uint64, operationID string) ModelActionResult {
	return s.modelAction("delete", operationID, []any{revision, providerID}, func(ctx context.Context, store *models.Store) (models.Receipt, error) {
		return store.DeleteProvider(ctx, revision, operationID, providerID)
	})
}
