package models

import (
	"context"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

type AvailableModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ModelPage struct {
	Models     []AvailableModel `json:"models"`
	NextCursor string           `json:"nextCursor,omitempty"`
	Truncated  bool             `json:"truncated"`
}
type Discoverer interface {
	ListModels(context.Context, Target, *credentials.Secret, string) (ModelPage, error)
}

// Listing is explicit and read-only; it never verifies or enables a model.
func (s *Store) DiscoverModels(ctx context.Context, revision uint64, providerID, cursor string) (ModelPage, error) {
	if s.closed.Load() {
		return ModelPage{}, ErrStopped
	}
	discoverer, ok := s.verifier.(Discoverer)
	if !ok {
		return ModelPage{}, &ConnectionProblem{Code: "list_unsupported"}
	}
	if len(cursor) > 2048 {
		return ModelPage{}, &ConnectionProblem{Code: "invalid_request"}
	}
	c, _, err := s.readCatalog()
	if err != nil {
		return ModelPage{}, err
	}
	if c.Revision != revision {
		return ModelPage{}, ErrConflict
	}
	p := findProvider(&c, providerID)
	if p == nil {
		return ModelPage{}, &ConnectionProblem{Code: "not_found"}
	}
	if p.Protocol == Subscription {
		if err := s.ClaimSubscriptionOwnership(); err != nil {
			return ModelPage{}, err
		}
		if s.subscriptionChanges.Load() > 0 {
			return ModelPage{}, ErrAccountChanging
		}
	}
	var secret *credentials.Secret
	if p.SecretRef != "" {
		if s.vault == nil {
			return ModelPage{}, credentials.ErrUnavailable
		}
		secret, err = s.vault.Read(ctx, p.SecretRef)
		if err != nil {
			return ModelPage{}, err
		}
		defer secret.Destroy()
	}
	if err = ctx.Err(); err != nil {
		return ModelPage{}, err
	}
	if s.closed.Load() {
		return ModelPage{}, ErrStopped
	}
	page, err := discoverer.ListModels(ctx, Target{Protocol: p.Protocol, BaseURL: p.BaseURL, Options: p.Options}, secret, cursor)
	if err != nil {
		return ModelPage{}, err
	}
	if err = ctx.Err(); err != nil {
		return ModelPage{}, err
	}
	latest, _, err := s.readCatalog()
	if err != nil {
		return ModelPage{}, err
	}
	if latest.Revision != revision || s.closed.Load() {
		return ModelPage{}, ErrConflict
	}
	return page, nil
}
