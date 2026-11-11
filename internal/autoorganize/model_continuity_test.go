package autoorganize

import (
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestRenewalUpdatesBothGrantsForSameAccount(t *testing.T) {
	base := t.TempDir()
	cfg := config.Default(base)
	policy := Policy{Enabled: true, Roots: []string{base}, ObjectionEnabledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}
	if err := SavePolicy(cfg, policy); err != nil {
		t.Fatal(err)
	}
	proof := models.SubscriptionRecipient{Schema: 1, ProofID: strings.Repeat("a", 64), Agent: "codex", AccountID: strings.Repeat("d", 64), AccountLabel: "Codex", ModelID: "codex/fixture", Endpoint: "https://chatgpt.com/backend-api/codex/responses", WireModel: "fixture"}
	recipient := models.Recipient{Epoch: 1, ProviderID: "provider", ProviderVersion: 1, ProviderName: "Codex", Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/codex", ModelRecordID: "record", ModelID: proof.ModelID, ModelName: "fixture", Subscription: &proof}
	first := ModelConsent{Recipient: recipient, ScopeSHA: PolicyScopeSHA(policy), GrantedAt: time.Now()}
	if err := SetModelConsent(cfg, &first); err != nil {
		t.Fatal(err)
	}
	second := first
	secondProof := proof
	secondProof.ProofID = strings.Repeat("b", 64)
	second.Recipient.Subscription = &secondProof
	if err := SetObjectionModelConsent(cfg, &second); err != nil {
		t.Fatal(err)
	}
	fresh := recipient
	freshProof := proof
	freshProof.ProofID = strings.Repeat("c", 64)
	fresh.Subscription = &freshProof
	if err := RefreshContinuousRecipient(cfg, first.Recipient, fresh); err != nil {
		t.Fatal(err)
	}
	state, err := ReadModelState(cfg)
	if err != nil || state.Consent == nil || state.ObjectionConsent == nil {
		t.Fatal("grants missing", err)
	}
	if state.Consent.Recipient.Digest() != fresh.Digest() || state.ObjectionConsent.Recipient.Digest() != fresh.Digest() || state.Consent.GrantDigest != first.Recipient.Digest() || state.ObjectionConsent.GrantDigest != second.Recipient.Digest() {
		t.Fatal("same-account grants did not keep their original identities")
	}
	other := fresh
	otherProof := freshProof
	otherProof.AccountID = strings.Repeat("e", 64)
	other.Subscription = &otherProof
	if err := RefreshContinuousRecipient(cfg, fresh, other); err == nil {
		t.Fatal("different account renewed the grants")
	}
}
