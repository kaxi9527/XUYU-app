package models

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

const MaxPromptBytes = 64 << 10
const MaxGeneratedBytes = 128 << 10
const MaxGenerationTokens = 8192

var ErrRequestConsumed = errors.New("这个模型请求已使用，请先核对请求记录")

// GenerationRequest is server-only. A verified connection is not authorization
// to send project data; the project controller must approve the exact input first.
// No browser RPC accepts this type. There are no tools, retries or fallbacks here.
type GenerationRequest struct {
	System              string `json:"system"`
	Input               string `json:"input"`
	MaxOutputTokens     int    `json:"maxOutputTokens"`
	AllowUncappedOutput bool   `json:"allowUncappedOutput,omitempty"`
}

func (r GenerationRequest) Validate() error {
	if strings.TrimSpace(r.Input) == "" || !utf8.ValidString(r.Input) || !utf8.ValidString(r.System) || strings.ContainsRune(r.Input, 0) || strings.ContainsRune(r.System, 0) || len(r.System)+len(r.Input) > MaxPromptBytes || r.MaxOutputTokens < 1 || r.MaxOutputTokens > MaxGenerationTokens {
		return &ConnectionProblem{Code: "invalid_request"}
	}
	return nil
}

// Usage contains only values reported by the provider. Nil is unknown, not zero.
// Token categories have provider-specific inclusion rules; do not add them to
// calculate a bill. No price or monetary spending guarantee is inferred here.
type Usage struct {
	OpenRouterDebit   *OpenRouterDebit `json:"openRouterDebit,omitempty"`
	InputTokens       *int64           `json:"inputTokens,omitempty"`
	OutputTokens      *int64           `json:"outputTokens,omitempty"`
	TotalTokens       *int64           `json:"totalTokens,omitempty"`
	CachedInputTokens *int64           `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  *int64           `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   *int64           `json:"reasoningTokens,omitempty"`
}

func (u Usage) Valid() bool {
	for _, n := range []*int64{u.InputTokens, u.OutputTokens, u.TotalTokens, u.CachedInputTokens, u.CacheWriteTokens, u.ReasoningTokens} {
		if n != nil && (*n < 0 || *n > 1_000_000_000) {
			return false
		}
	}
	return u.OpenRouterDebit.valid()
}

type GenerationResult struct {
	Text  string `json:"text"`
	Usage Usage  `json:"usage"`
}

func (r GenerationResult) Validate() error {
	if strings.TrimSpace(r.Text) == "" || !utf8.ValidString(r.Text) || strings.ContainsRune(r.Text, 0) || len(r.Text) > MaxGeneratedBytes || !r.Usage.Valid() {
		return &ConnectionProblem{Code: "invalid_response"}
	}
	return nil
}

type Generator interface {
	Generate(context.Context, Target, *credentials.Secret, GenerationRequest) (GenerationResult, error)
}

// Generate permits one outbound call per lease. A durable request/budget ledger
// belongs to the project controller; a new lease is not a safe retry receipt.
func (l *Lease) Generate(request GenerationRequest) (GenerationResult, error) {
	return l.generate(nil, request)
}

// GenerateForRecipient carries the server-confirmed project snapshot through
// the adapter to the final signed send boundary. Equality is not authorization:
// callers still need the durable reservation and explicit user consent.
func (l *Lease) GenerateForRecipient(expected Recipient, request GenerationRequest) (GenerationResult, error) {
	if expected.Subscription != nil {
		copy := *expected.Subscription
		expected.Subscription = &copy
	}
	if err := expected.RequireLiveProof(); err != nil {
		return GenerationResult{}, err
	}
	actual, err := l.Recipient()
	if err != nil {
		return GenerationResult{}, err
	}
	if actual.Digest() != expected.Digest() {
		return GenerationResult{}, ErrStopped
	}
	return l.generate(&expected, request)
}
func (l *Lease) generate(expected *Recipient, request GenerationRequest) (GenerationResult, error) {
	if err := request.Validate(); err != nil {
		return GenerationResult{}, err
	}
	if l == nil || l.store == nil {
		return GenerationResult{}, ErrStopped
	}
	adapter, ok := l.store.verifier.(Generator)
	if !ok {
		return GenerationResult{}, &ConnectionProblem{Code: "unsupported"}
	}
	if !l.generated.CompareAndSwap(false, true) {
		return GenerationResult{}, ErrRequestConsumed
	}
	if err := l.Check(); err != nil {
		return GenerationResult{}, err
	}
	ctx, cancel := context.WithTimeout(l.Context, 30*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	joined := make(chan struct{})
	// The local mode path cancels leases immediately. Poll the authoritative
	// catalog as well so another process cannot leave this request running.
	go func() {
		defer close(joined)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := l.Check(); err != nil {
					stopped <- err
					cancel()
					return
				}
			}
		}
	}()
	var result GenerationResult
	err := l.WithConnection(func(_ context.Context, target Target, key *credentials.Secret) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if target.Protocol == Subscription {
			if expected == nil || expected.Subscription == nil {
				return &ConnectionProblem{Code: "recipient_unverified"}
			}
			resolver, ok := l.store.verifier.(SubscriptionResolver)
			if !ok {
				return &ConnectionProblem{Code: "recipient_unverified"}
			}
			proof, e := resolver.SubscriptionRecipient(ctx, target)
			if e != nil {
				return e
			}
			if proof != *expected.Subscription {
				return ErrStopped
			}
			target.Subscription = &proof
		}
		var err error
		result, err = adapter.Generate(ctx, target, key, request)
		return err
	})
	requestErr := ctx.Err()
	cancel()
	<-joined
	select {
	case reason := <-stopped:
		return GenerationResult{}, reason
	default:
	}
	if check := l.Check(); check != nil {
		return GenerationResult{}, check
	}
	if requestErr != nil {
		return GenerationResult{}, requestErr
	}
	if err != nil {
		return GenerationResult{}, err
	}
	if err = result.Validate(); err != nil {
		return GenerationResult{}, err
	}
	return result, nil
}
