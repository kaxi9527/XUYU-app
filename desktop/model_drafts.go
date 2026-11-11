package main

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"strings"
	"time"
)

type ModelDraftView struct {
	Token      string                   `json:"token"`
	ExpiresAt  string                   `json:"expiresAt"`
	Connection models.ConnectionPreview `json:"connection"`
	Verified   bool                     `json:"verified"`
}
type modelDraftEntry struct {
	draft                     *models.Draft
	expires                   time.Time
	timer                     *time.Timer
	generation                uint64
	busy, disposed, completed bool
	cancel                    context.CancelFunc
}

func (s *ShelfService) CreateModelDraft(in ModelConnectionInput) (ModelDraftView, error) {
	if len(in.APIKey) > credentials.MaxSecretBytes {
		return ModelDraftView{}, credentials.ErrInvalid
	}
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.getStore()
	if err != nil {
		return ModelDraftView{}, modelPublicError(err)
	}
	for token, e := range b.drafts {
		if time.Now().After(e.expires) {
			b.releaseDraftLocked(token)
		}
	}
	for token, e := range b.entries {
		if e.run != nil && e.run.completed || time.Now().After(e.expires) && e.run == nil {
			b.releasePreview(token, e)
		}
	}
	if len(b.drafts)+len(b.entries) >= modelPreviewLimit {
		return ModelDraftView{}, errors.New("已有多个连接正在编辑，请关闭一个后再试")
	}
	var key *credentials.Secret
	if in.APIKey != "" {
		bytes := []byte(in.APIKey)
		key, err = credentials.NewSecret(bytes)
		credentials.Wipe(bytes)
		in.APIKey = ""
		if err != nil {
			return ModelDraftView{}, modelPublicError(err)
		}
		defer key.Destroy()
		if !b.credentialStorage {
			return ModelDraftView{}, credentials.ErrUnavailable
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	draft, err := store.PrepareDraft(ctx, in.Revision, models.ConnectionInput{ProviderID: in.ProviderID, ModelRecordID: in.ModelRecordID, ProviderName: in.ProviderName, Protocol: in.Protocol, BaseURL: in.BaseURL, ModelID: in.ModelID, ModelName: in.ModelName, Credential: key, RemoveCredential: in.RemoveCredential, Options: in.Options})
	if err != nil {
		return ModelDraftView{}, modelPublicError(err)
	}
	token, err := versionToken()
	if err != nil {
		draft.Close()
		return ModelDraftView{}, modelPublicError(err)
	}
	ttl := b.ttl
	if ttl <= 0 || ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}
	entry := &modelDraftEntry{draft: draft, expires: time.Now().Add(ttl)}
	if b.drafts == nil {
		b.drafts = map[string]*modelDraftEntry{}
	}
	b.drafts[token] = entry
	entry.timer = time.AfterFunc(ttl, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.drafts[token] == entry {
			b.releaseDraftLocked(token)
		}
	})
	view, err := draftView(token, entry)
	if err != nil {
		b.releaseDraftLocked(token)
	}
	return view, err
}
func draftView(token string, e *modelDraftEntry) (ModelDraftView, error) {
	preview, verified, err := e.draft.Preview()
	if err != nil {
		return ModelDraftView{}, modelPublicError(err)
	}
	return ModelDraftView{Token: token, ExpiresAt: e.expires.UTC().Format(time.RFC3339Nano), Connection: preview, Verified: verified}, nil
}

// b.mu is held. Destruction waits until the in-flight request has released its
// core draft lock; cancellation does not claim a save was rolled back.
func (b *modelConnections) releaseDraftLocked(token string) {
	e := b.drafts[token]
	if e == nil {
		return
	}
	e.disposed = true
	e.timer.Stop()
	if e.cancel != nil {
		e.cancel()
	}
	if !e.busy {
		e.draft.Close()
		delete(b.drafts, token)
	}
}
func (s *ShelfService) DiscardModelDraft(token string) bool {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.drafts[token] == nil {
		return false
	}
	b.releaseDraftLocked(token)
	return true
}
func (s *ShelfService) SelectModelDraft(token, modelID, name string) (ModelDraftView, error) {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.drafts[token]
	if e == nil || e.disposed || e.completed || b.closed || time.Now().After(e.expires) {
		return ModelDraftView{}, models.ErrDraftExpired
	}
	if e.busy {
		return ModelDraftView{}, errors.New("正在处理这个连接，请等待或取消请求")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.draft.SelectModel(ctx, modelID, name); err != nil {
		return ModelDraftView{}, modelPublicError(err)
	}
	e.generation++
	return draftView(token, e)
}

func (s *ShelfService) draftAction(kind, token, id string, identity any, work func(context.Context, *models.Store, *models.Draft) ModelActionResult) ModelActionResult {
	b := &s.connections
	b.mu.Lock()
	e := b.drafts[token]
	if e == nil || e.disposed || time.Now().After(e.expires) {
		b.mu.Unlock()
		return ModelActionResult{Error: models.ErrDraftExpired.Error()}
	}
	generation := e.generation
	b.mu.Unlock()
	return s.runModelAction(kind, id, []any{token, generation, identity}, func(parent context.Context, store *models.Store) ModelActionResult {
		b.mu.Lock()
		if b.drafts[token] != e || e.disposed || e.completed || time.Now().After(e.expires) {
			b.mu.Unlock()
			return ModelActionResult{Error: models.ErrDraftExpired.Error()}
		}
		if e.generation != generation {
			b.mu.Unlock()
			return ModelActionResult{Error: models.ErrConflict.Error()}
		}
		if e.busy {
			b.mu.Unlock()
			return ModelActionResult{Error: "正在处理这个连接，请等待或取消请求"}
		}
		ctx, cancel := context.WithCancel(parent)
		e.busy = true
		e.cancel = cancel
		b.mu.Unlock()
		result := work(ctx, store, e.draft)
		cancel()
		b.mu.Lock()
		e.busy = false
		e.cancel = nil
		if kind == "draft-save" && result.Receipt != nil {
			e.completed = true
			e.draft.Close()
		}
		if e.disposed || b.closed || time.Now().After(e.expires) {
			b.releaseDraftLocked(token)
		}
		b.mu.Unlock()
		return result
	})
}
func draftError(err error) ModelActionResult {
	r := ModelActionResult{Error: modelPublicError(err).Error()}
	var problem *models.ConnectionProblem
	if errors.As(err, &problem) && problem != nil && problem.Valid() {
		copy := *problem
		r.Problem = &copy
	}
	return r
}
func (s *ShelfService) ListModelDraft(token, cursor, requestID string) ModelActionResult {
	return s.draftAction("draft-list", token, requestID, cursor, func(ctx context.Context, _ *models.Store, d *models.Draft) ModelActionResult {
		page, err := d.Discover(ctx, cursor)
		if err != nil {
			return draftError(err)
		}
		return ModelActionResult{Models: &page}
	})
}
func (s *ShelfService) VerifyModelDraft(token, requestID string) ModelActionResult {
	return s.draftAction("draft-verify", token, requestID, nil, func(ctx context.Context, _ *models.Store, d *models.Draft) ModelActionResult {
		result, err := d.Verify(ctx)
		if err != nil {
			return draftError(err)
		}
		response := ModelActionResult{DraftVerification: &result, Problem: result.Problem}
		if !result.Verified && result.Problem != nil {
			response.Error = result.Problem.Error()
		}
		return response
	})
}
func (s *ShelfService) SaveModelDraft(token, operationID string) ModelActionResult {
	return s.draftAction("draft-save", token, operationID, true, func(ctx context.Context, store *models.Store, d *models.Draft) ModelActionResult {
		receipt, err := store.SaveVerifiedDraft(ctx, d, operationID, true)
		return modelActionOutcome(store, operationID, receipt, err)
	})
}
func (s *ShelfService) CancelModelDraftRequest(requestID string) bool {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.jobs[requestID]
	if run == nil || !strings.HasPrefix(run.kind, "draft-") || run.completed {
		return false
	}
	run.cancel()
	return true
}
