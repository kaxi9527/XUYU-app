package models

import (
	"context"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

// Recipient is a non-secret snapshot for project-specific outbound consent.
// Epoch/version bind it to the selected connection and account generation.
type Recipient struct {
	Epoch           uint64                 `json:"epoch"`
	ProviderID      string                 `json:"providerID"`
	ProviderVersion uint64                 `json:"providerVersion"`
	ProviderName    string                 `json:"providerName"`
	Protocol        Protocol               `json:"protocol"`
	BaseURL         string                 `json:"baseURL"`
	ModelRecordID   string                 `json:"modelRecordID"`
	ModelID         string                 `json:"modelID"`
	ModelName       string                 `json:"modelName"`
	Options         Options                `json:"options"`
	Subscription    *SubscriptionRecipient `json:"subscription,omitempty"`
}

func (r Recipient) Digest() string { return digest("project-recipient-v1", r) }
func (r Recipient) Valid() bool {
	if r.Subscription != nil && (r.Protocol != Subscription || !r.Subscription.Valid() || r.Subscription.ModelID != r.ModelID || r.Subscription.Agent != SubscriptionAgent(r.BaseURL)) {
		return false
	}
	u, err := normalizeEndpoint(r.BaseURL)
	_, optionsErr := NormalizeOptions(r.Protocol, r.Options)
	return err == nil && u == r.BaseURL && optionsErr == nil && validProtocol(r.Protocol) && r.Epoch > 0 && r.Epoch < 1<<53 && r.ProviderVersion > 0 && r.ProviderVersion < 1<<53 && label(r.ProviderID, 128) && label(r.ProviderName, 120) && label(r.ModelRecordID, 128) && label(r.ModelID, 256) && label(r.ModelName, 120)
}
func (l *Lease) Recipient() (Recipient, error) {
	if l == nil || l.store == nil {
		return Recipient{}, ErrStopped
	}
	if err := l.Check(); err != nil {
		return Recipient{}, err
	}
	c, _, err := l.store.readCatalog()
	if err != nil {
		return Recipient{}, err
	}
	if c.Settings.Mode != "agent" || c.Settings.Epoch != l.Epoch || c.Settings.ActiveModelID != l.ModelID {
		return Recipient{}, ErrStopped
	}
	m := findModel(&c, l.ModelID)
	if m == nil || m.Verification == nil {
		return Recipient{}, ErrUnverified
	}
	p := findProvider(&c, m.ProviderID)
	if p == nil {
		return Recipient{}, ErrUnverified
	}
	out := Recipient{Epoch: l.Epoch, ProviderID: p.ID, ProviderVersion: p.Version, ProviderName: p.Name, Protocol: p.Protocol, BaseURL: p.BaseURL, ModelRecordID: m.ID, ModelID: m.ModelID, ModelName: m.Name, Options: p.Options}
	if p.Protocol == Subscription {
		resolver, ok := l.store.verifier.(SubscriptionResolver)
		if !ok {
			return out, nil
		} // old logical receipts remain readable, never executable
		err = l.WithConnection(func(ctx context.Context, target Target, _ *credentials.Secret) error {
			if target.Protocol != out.Protocol || target.BaseURL != out.BaseURL || target.ModelID != out.ModelID {
				return ErrStopped
			}
			proof, e := resolver.SubscriptionRecipient(ctx, target)
			if e != nil {
				return e
			}
			out.Subscription = &proof
			return out.RequireLiveProof()
		})
		if err != nil {
			return Recipient{}, err
		}
	}
	return out, nil
}

// BeginForConfirmedRecipient is only for a trusted controller holding durable,
// unexpired consent for an exact request or batch. Equality is NOT consent.
// It issues one lease after explicit resume, without arming unrelated requests
// or changing the persisted epoch on an application restart. No public RPC may
// expose this method as an arbitrary-recipient authorization shortcut.
func (s *Store) BeginForConfirmedRecipient(ctx context.Context, expected Recipient) (*Lease, error) {
	return s.beginForRecipient(ctx, expected)
}

// BeginForContinuousRecipient is for a server-side controller holding a
// durable user grant for the recipient, purpose and current data scope. The
// controller must recheck that grant before every request and publication.
func (s *Store) BeginForContinuousRecipient(ctx context.Context, expected Recipient) (*Lease, error) {
	return s.beginForRecipient(ctx, expected)
}

// PreviewContinuousRecipient reads the selected Codex account for a user
// preparing a new continuous grant; it does not arm ordinary model requests.
func (s *Store) PreviewContinuousRecipient(ctx context.Context) (Recipient, error) {
	var selected Recipient
	err := s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		m := findModel(&c, c.Settings.ActiveModelID)
		if c.Settings.Mode != "agent" || m == nil || m.Verification == nil {
			return ErrStopped
		}
		p := findProvider(&c, m.ProviderID)
		if p == nil || p.Protocol != Subscription || SubscriptionAgent(p.BaseURL) != "codex" || m.Verification.ProviderVersion != p.Version {
			return ErrUnverified
		}
		selected = Recipient{Epoch: c.Settings.Epoch, ProviderID: p.ID, ProviderVersion: p.Version, ProviderName: p.Name, Protocol: p.Protocol, BaseURL: p.BaseURL, ModelRecordID: m.ID, ModelID: m.ModelID, ModelName: m.Name, Options: p.Options}
		return nil
	})
	if err != nil {
		return Recipient{}, err
	}
	resolver, ok := s.verifier.(SubscriptionResolver)
	if !ok {
		return Recipient{}, ErrUnverified
	}
	if err = s.ClaimSubscriptionOwnership(); err != nil {
		return Recipient{}, err
	}
	target := Target{Protocol: selected.Protocol, BaseURL: selected.BaseURL, ModelID: selected.ModelID, Options: selected.Options}
	s.subscriptionUse.RLock()
	if s.subscriptionChanges.Load() > 0 {
		s.subscriptionUse.RUnlock()
		return Recipient{}, ErrAccountChanging
	}
	proof, err := resolver.SubscriptionRecipient(ctx, target)
	if err != nil {
		caps, e := s.verifier.Verify(ctx, target, nil)
		if e != nil || !validCapabilities(caps) || caps.Text != "supported" {
			s.subscriptionUse.RUnlock()
			return Recipient{}, ErrUnverified
		}
		proof, err = resolver.SubscriptionRecipient(ctx, target)
	}
	s.subscriptionUse.RUnlock()
	if err != nil || !proof.Valid() {
		return Recipient{}, ErrUnverified
	}
	selected.Subscription = &proof
	if err = s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		return recipientMatchesCatalog(&c, selected)
	}); err != nil {
		return Recipient{}, err
	}
	return selected, selected.RequireLiveProof()
}

// RefreshContinuousRecipient renews a helper-local proof for an existing
// continuous grant. The selected account and destination must remain exact.
func (s *Store) RefreshContinuousRecipient(ctx context.Context, expected Recipient) (Recipient, error) {
	if expected.RequireLiveProof() != nil || expected.Subscription == nil || expected.Subscription.Agent != "codex" {
		return Recipient{}, ErrUnverified
	}
	resolver, ok := s.verifier.(SubscriptionResolver)
	if !ok {
		return Recipient{}, ErrUnverified
	}
	if err := s.ClaimSubscriptionOwnership(); err != nil {
		return Recipient{}, err
	}
	if err := s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		return recipientMatchesCatalog(&c, expected)
	}); err != nil {
		return Recipient{}, err
	}
	target := Target{Protocol: expected.Protocol, BaseURL: expected.BaseURL, ModelID: expected.ModelID, Options: expected.Options}
	s.subscriptionUse.RLock()
	if s.subscriptionChanges.Load() > 0 {
		s.subscriptionUse.RUnlock()
		return Recipient{}, ErrAccountChanging
	}
	caps, err := s.verifier.Verify(ctx, target, nil)
	if err == nil && (!validCapabilities(caps) || caps.Text != "supported") {
		err = ErrUnverified
	}
	var proof SubscriptionRecipient
	if err == nil {
		proof, err = resolver.SubscriptionRecipient(ctx, target)
	}
	s.subscriptionUse.RUnlock()
	if err != nil || !proof.Valid() {
		return Recipient{}, ErrUnverified
	}
	oldProof := *expected.Subscription
	oldProof.ProofID = proof.ProofID
	if oldProof != proof {
		return Recipient{}, ErrUnverified
	}
	expected.Subscription = &proof
	lease, err := s.BeginForContinuousRecipient(ctx, expected)
	if err != nil {
		return Recipient{}, err
	}
	lease.Close()
	return expected, nil
}

func recipientMatchesCatalog(c *catalog, expected Recipient) error {
	if c.Settings.Mode != "agent" || c.Settings.Epoch != expected.Epoch || c.Settings.ActiveModelID != expected.ModelRecordID {
		return ErrStopped
	}
	m := findModel(c, c.Settings.ActiveModelID)
	if m == nil || m.Verification == nil {
		return ErrUnverified
	}
	p := findProvider(c, m.ProviderID)
	if p == nil || m.Verification.ProviderVersion != p.Version {
		return ErrUnverified
	}
	actual := Recipient{Epoch: c.Settings.Epoch, ProviderID: p.ID, ProviderVersion: p.Version, ProviderName: p.Name, Protocol: p.Protocol, BaseURL: p.BaseURL, ModelRecordID: m.ID, ModelID: m.ModelID, ModelName: m.Name, Options: p.Options}
	base := expected
	base.Subscription = nil
	if actual.Digest() != base.Digest() {
		return ErrStopped
	}
	return nil
}

func (s *Store) beginForRecipient(ctx context.Context, expected Recipient) (*Lease, error) {
	if expected.Subscription != nil {
		proof := *expected.Subscription
		expected.Subscription = &proof
	}
	if expected.RequireLiveProof() != nil {
		return nil, ErrUnverified
	}
	var lease *Lease
	err := s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		if err := recipientMatchesCatalog(&c, expected); err != nil {
			return err
		}
		if expected.Protocol == Subscription {
			if s.subscriptionChanges.Load() > 0 {
				return ErrAccountChanging
			}
			if err := s.ClaimSubscriptionOwnership(); err != nil {
				return err
			}
		}
		child, cancel := context.WithCancel(ctx)
		lease = &Lease{Context: child, Epoch: c.Settings.Epoch, ModelID: expected.ModelRecordID, store: s}
		s.leaseMu.Lock()
		s.leases[lease] = cancel
		s.leaseMu.Unlock()
		return nil
	})
	if err == nil && expected.Protocol == Subscription {
		actual, e := lease.Recipient()
		if e != nil || actual.Digest() != expected.Digest() {
			lease.Close()
			if e != nil {
				return nil, e
			}
			return nil, ErrStopped
		}
	}
	return lease, err
}

// Publish serializes acceptance with model configuration changes. The callback
// may commit a local receipt; it must not call this model Store or do networking.
func (l *Lease) Publish(ctx context.Context, fn func() error) error {
	if l == nil || l.store == nil {
		return ErrStopped
	}
	return l.store.locked(ctx, func() error {
		if err := l.Check(); err != nil {
			return err
		}
		if l.store.subscriptionChanges.Load() > 0 {
			return ErrAccountChanging
		}
		return fn()
	})
}
