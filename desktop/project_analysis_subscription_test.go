package main

import (
	"context"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAnalysisRefusesLogicalSubscriptionRecipients(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "copilot", "gemini", "antigravity"} {
		t.Run(agent, func(t *testing.T) {
			s, _, calls := analysisServiceFixture(t, nil)
			ctx := context.Background()
			old, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
			if err != nil {
				t.Fatal(err)
			}
			s.connections.store.Close()
			store, err := models.NewStore(filepath.Join(t.TempDir(), "subscription"), nil, modelTestVerifier(func(context.Context, models.Target, *credentials.Secret) (models.Capabilities, error) {
				return models.Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			s.connections.store = store
			if err = store.ClaimSubscriptionOwnership(); err != nil {
				t.Fatal(err)
			}
			prepared, err := store.Prepare(ctx, 0, models.ConnectionInput{ProviderName: agent, Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/" + agent, ModelID: agent + "/fixture", ModelName: "Fixture"})
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			saved, err := store.Save(ctx, prepared, "save-subscription")
			if err != nil {
				t.Fatal(err)
			}
			verified, err := store.VerifyModel(ctx, saved.Revision, "verify-subscription", saved.ModelID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetMode(ctx, verified.Receipt.Revision, "use-subscription", "agent", saved.ModelID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.PreviewProjectAnalysis("root:品牌项目", nil); err == nil || !strings.Contains(err.Error(), "实际发送地址") {
				t.Fatal("logical subscription endpoint exposed for consent", err)
			}
			// Emulate a preview retained by an older producer, with an otherwise
			// matching current lease. Confirm must independently refuse it.
			lease, err := store.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			recipient, err := lease.Recipient()
			lease.Close()
			if err != nil {
				t.Fatal(err)
			}
			s.analyses.mu.Lock()
			s.analyses.previews[old.Token].spec.Recipient = recipient
			s.analyses.mu.Unlock()
			if _, err = s.ConfirmProjectAnalysis("root:品牌项目", old.Token, "old-preview", true, false); err == nil || !strings.Contains(err.Error(), "实际发送地址") {
				t.Fatal("old preview bypassed recipient restriction", err)
			}
			history, err := s.ProjectAnalysisHistory("root:品牌项目", 0)
			if err != nil || history.Total != 0 || calls.Load() != 0 {
				t.Fatal("blocked recipient reserved or sent", err)
			}
		})
	}
}
