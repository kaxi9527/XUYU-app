package models

import (
	"context"
	"errors"
)

// Removing a provider removes its model entries, stops an affected Agent and
// reserves its credential for cleanup before committing the metadata change.
func (s *Store) DeleteProvider(ctx context.Context, revision uint64, operationID, providerID string) (Receipt, error) {
	hash := digest("delete-provider", []any{revision, providerID})
	var result Receipt
	err := s.locked(ctx, func() error {
		c, original, err := s.readCatalog()
		if err != nil {
			return err
		}
		old, err := replay(c, operationID, hash)
		if err != nil {
			return err
		}
		if old != nil {
			result = *old
			return nil
		}
		if c.Revision != revision {
			return ErrConflict
		}
		provider := findProvider(&c, providerID)
		if provider == nil {
			return errors.New("提供商不存在")
		}
		if err = s.reserveSecrets(ctx, provider.SecretRef); err != nil {
			return err
		}
		providers := []Provider{}
		for _, p := range c.Providers {
			if p.ID != providerID {
				providers = append(providers, p)
			}
		}
		c.Providers = providers
		models := []Model{}
		for _, m := range c.Models {
			if m.ProviderID != providerID {
				models = append(models, m)
			} else if c.Settings.ActiveModelID == m.ID {
				c.Settings.Mode = "tool"
				c.Settings.ActiveModelID = ""
			}
		}
		c.Models = models
		result, err = s.commit(ctx, &c, original, Receipt{OperationID: operationID, Digest: hash, ProviderID: providerID}, true)
		if err == nil {
			_ = s.cleanup(ctx, c)
		}
		return err
	})
	return result, err
}
