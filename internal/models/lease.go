package models

import (
	"context"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

// WithConnection is the server-side protocol adapter's only lease-bound secret
// access. Secrets are destroyed after the callback; no key is returned to UI.
func (l *Lease) WithConnection(fn func(context.Context, Target, *credentials.Secret) error) error {
	if err := l.Check(); err != nil {
		return err
	}
	c, _, err := l.store.readCatalog()
	if err != nil {
		return err
	}
	if c.Settings.Epoch != l.Epoch || c.Settings.Mode != "agent" || c.Settings.ActiveModelID != l.ModelID {
		return ErrStopped
	}
	m := findModel(&c, l.ModelID)
	if m == nil || m.Verification == nil {
		return ErrUnverified
	}
	p := findProvider(&c, m.ProviderID)
	if p == nil {
		return ErrUnverified
	}
	if p.Protocol == Subscription {
		if err := l.store.ClaimSubscriptionOwnership(); err != nil {
			return err
		}
		l.store.subscriptionUse.RLock()
		defer l.store.subscriptionUse.RUnlock()
		if l.store.subscriptionChanges.Load() > 0 {
			return ErrAccountChanging
		}
	}
	var secret *credentials.Secret
	if p.SecretRef != "" {
		if l.store.vault == nil {
			return credentials.ErrUnavailable
		}
		secret, err = l.store.vault.Read(l.Context, p.SecretRef)
		if err != nil {
			return err
		}
		defer secret.Destroy()
	}
	if err = l.Check(); err != nil {
		return err
	}
	return fn(l.Context, Target{Protocol: p.Protocol, BaseURL: p.BaseURL, ModelID: m.ModelID, Options: p.Options}, secret)
}
