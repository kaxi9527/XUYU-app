package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrAccountChanging = errors.New("订阅账号正在授权或切换，请完成或取消后再测试和使用模型")
var ErrAccountOwned = errors.New("另一个 WorkShelf 窗口正在使用模型与账号，请关闭那个窗口后重试")

// This lifetime OS lock protects the shared account/catalog pair across both
// Store objects and processes. It is never removed; unlinking a live lock file
// would allow a second owner. Read-only catalog access needs no ownership.
func (s *Store) ClaimSubscriptionOwnership() error {
	s.subscriptionOwnerMu.Lock()
	defer s.subscriptionOwnerMu.Unlock()
	if s.closed.Load() {
		return ErrStopped
	}
	if _, err := s.directoryReady(true); err != nil {
		return err
	}
	path := filepath.Join(s.directory, ".subscription-owner.lock")
	if s.subscriptionOwner != nil {
		opened, e := s.subscriptionOwner.Stat()
		current, e2 := os.Lstat(path)
		if e != nil || e2 != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
			return ErrConflict
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	owner, err := lockFile(ctx, path)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrAccountOwned
		}
		return err
	}
	opened, e := owner.Stat()
	current, e2 := os.Lstat(path)
	if e != nil || e2 != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		unlockFile(owner)
		return ErrConflict
	}
	s.subscriptionOwner = owner
	return nil
}

// Block the entire account mutation interval, including asynchronous OAuth.
// Drain lease callbacks before changing credentials; invalidation alone has a
// window in which another request could re-verify the previous account.
func (s *Store) BeginSubscriptionChange(ctx context.Context, agent string) (func() error, error) {
	if err := s.ClaimSubscriptionOwnership(); err != nil {
		return nil, err
	}
	s.subscriptionChanges.Add(1)
	s.stopLeases()
	s.subscriptionUse.Lock()
	err := s.InvalidateSubscriptions(ctx, agent)
	s.subscriptionUse.Unlock()
	if err != nil {
		s.subscriptionChanges.Add(-1)
		return nil, err
	}
	var mu sync.Mutex
	released := false
	return func() error {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.InvalidateSubscriptions(ctx, agent); err != nil {
			return err
		}
		released = true
		s.subscriptionChanges.Add(-1)
		return nil
	}, nil
}

// Subscription credentials are managed by the private authorization runtime.
// A login/account change must revoke previous verification and active leases.
// No account identifiers or credentials are copied into the model catalog.
func (s *Store) InvalidateSubscriptions(ctx context.Context, agent string) error {
	return s.locked(ctx, func() error {
		c, original, err := s.readCatalog()
		if err != nil {
			return err
		}
		affected := map[string]bool{}
		for i := range c.Providers {
			p := &c.Providers[i]
			if p.Protocol == Subscription && (agent == "" || SubscriptionAgent(p.BaseURL) == agent) {
				p.Version++
				affected[p.ID] = true
			}
		}
		if len(affected) == 0 {
			return nil
		}
		for i := range c.Models {
			m := &c.Models[i]
			if affected[m.ProviderID] {
				m.Verification = nil
				if c.Settings.ActiveModelID == m.ID {
					c.Settings.Mode = "tool"
					c.Settings.ActiveModelID = ""
				}
			}
		}
		id, err := identifier("account-change-")
		if err != nil {
			return err
		}
		_, err = s.commit(ctx, &c, original, Receipt{OperationID: id, Digest: digest("account-change", []any{agent, c.Revision})}, true)
		return err
	})
}
