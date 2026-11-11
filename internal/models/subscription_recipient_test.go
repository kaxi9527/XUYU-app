package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

func subscriptionProofFixture() SubscriptionRecipient {
	return SubscriptionRecipient{Schema: 1, ProofID: strings.Repeat("a", 64), Agent: "copilot", AccountID: strings.Repeat("b", 64), AccountLabel: "Copilot · te••••st", ModelID: "copilot/fixture", Endpoint: "https://api.githubcopilot.com/chat/completions", WireModel: "fixture"}
}

type recipientGenerator struct {
	mu      sync.Mutex
	proof   SubscriptionRecipient
	missing bool
	calls   int
}

func (g *recipientGenerator) Verify(ctx context.Context, target Target, key *credentials.Secret) (Capabilities, error) {
	g.mu.Lock()
	if g.missing {
		g.missing = false
		g.proof.ProofID = strings.Repeat("c", 64)
	}
	g.mu.Unlock()
	return passingVerifier(ctx, target, key)
}
func (g *recipientGenerator) SubscriptionRecipient(context.Context, Target) (SubscriptionRecipient, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.missing {
		return SubscriptionRecipient{}, ErrUnverified
	}
	return g.proof, nil
}
func (g *recipientGenerator) Generate(_ context.Context, target Target, _ *credentials.Secret, _ GenerationRequest) (GenerationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if target.Subscription == nil || *target.Subscription != g.proof {
		return GenerationResult{}, ErrUnverified
	}
	g.calls++
	return GenerationResult{Text: "confirmed advice"}, nil
}
func activeSubscription(t *testing.T) (*Store, *recipientGenerator) {
	t.Helper()
	s, _ := fixture(t)
	g := &recipientGenerator{proof: subscriptionProofFixture()}
	s.verifier = g
	p, err := s.Prepare(context.Background(), 0, ConnectionInput{ProviderName: "Copilot", Protocol: Subscription, BaseURL: SubscriptionEndpoint + "/copilot", ModelID: g.proof.ModelID})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	saved := save(t, s, p, "subscription-save")
	v, err := s.VerifyModel(context.Background(), saved.Revision, "subscription-verify", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetMode(context.Background(), v.Receipt.Revision, "subscription-use", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	return s, g
}
func TestSubscriptionRecipientStrictDestinations(t *testing.T) {
	good := subscriptionProofFixture()
	if !good.Valid() {
		t.Fatal("fixture")
	}
	for _, endpoint := range []string{"https://api.githubcopilot.com/other", "https://api.githubcopilot.com/chat/completions?key=x", "https://api.githubcopilot.com/chat/completions?", "https://api.githubcopilot.com/%63hat/completions", "https://api.githubcopilot.com.evil.test/chat/completions", "http://api.githubcopilot.com/chat/completions", "https://user@api.githubcopilot.com/chat/completions"} {
		r := good
		r.Endpoint = endpoint
		if r.Valid() {
			t.Fatal("unsafe destination", endpoint)
		}
	}
	for _, model := range []string{"auto", "a/b", "a:b", " x", "a@b"} {
		r := good
		r.WireModel = model
		if r.Valid() {
			t.Fatal("unsafe wire model")
		}
	}
}
func TestHistoricalSubscriptionRecipientRemainsReadableButCannotSend(t *testing.T) {
	s, _ := activeSubscription(t)
	l, e := s.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	r, e := l.Recipient()
	if e != nil {
		t.Fatal(e)
	}
	r.Subscription = nil
	data, e := json.Marshal(r)
	if e != nil || strings.Contains(string(data), "\"subscription\":") {
		t.Fatal("legacy serialization changed")
	}
	var read Recipient
	if json.Unmarshal(data, &read) != nil || !read.Valid() || read.Digest() != r.Digest() {
		t.Fatal("history no longer readable")
	}
	if read.RequireLiveProof() == nil {
		t.Fatal("logical receipt executable")
	}
	if _, e = s.BeginForConfirmedRecipient(context.Background(), read); e == nil {
		t.Fatal("logical resume executable")
	}
}
func TestSubscriptionLeaseRequiresExactConfirmedProof(t *testing.T) {
	for _, change := range []string{"none", "retest", "endpoint", "wire-model", "account", "missing", "unbound"} {
		t.Run(change, func(t *testing.T) {
			s, g := activeSubscription(t)
			l, e := s.Begin(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			r, e := l.Recipient()
			if e != nil || r.RequireLiveProof() != nil || g.calls != 0 {
				t.Fatal("preview", e)
			}
			g.mu.Lock()
			switch change {
			case "retest":
				g.proof.ProofID = strings.Repeat("c", 64)
			case "endpoint":
				g.proof.Endpoint = "https://api.githubcopilot.com/responses"
			case "wire-model":
				g.proof.WireModel = "changed"
			case "account":
				g.proof.AccountID = strings.Repeat("c", 64)
			case "missing":
				g.missing = true
			}
			g.mu.Unlock()
			input := GenerationRequest{Input: "project evidence", MaxOutputTokens: 64}
			if change == "unbound" {
				_, e = l.Generate(input)
			} else {
				_, e = l.GenerateForRecipient(r, input)
			}
			if change == "none" {
				if e != nil || g.calls != 1 {
					t.Fatal(e)
				}
				if _, e = l.GenerateForRecipient(r, input); e == nil || g.calls != 1 {
					t.Fatal("repeated")
				}
			} else if e == nil || g.calls != 0 {
				t.Fatal("changed or unbound proof sent", e)
			}
		})
	}
}
func TestSubscriptionScopedResumeRechecksHelperLifetime(t *testing.T) {
	s, g := activeSubscription(t)
	l, e := s.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	r, e := l.Recipient()
	l.Close()
	if e != nil {
		t.Fatal(e)
	}
	resumed, e := s.BeginForConfirmedRecipient(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	resumed.Close()
	g.mu.Lock()
	g.missing = true
	g.mu.Unlock()
	if _, e = s.BeginForConfirmedRecipient(context.Background(), r); e == nil || g.calls != 0 {
		t.Fatal("helper restart accepted")
	}
}

func TestCodexContinuousGrantRevalidatesOnlySameRecipient(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("accountChanged=", changed), func(t *testing.T) {
			s, _ := fixture(t)
			proof := subscriptionProofFixture()
			proof.Agent, proof.ModelID, proof.WireModel, proof.Endpoint = "codex", "codex/fixture", "fixture", "https://chatgpt.com/backend-api/codex/responses"
			g := &recipientGenerator{proof: proof}
			s.verifier = g
			p, err := s.Prepare(context.Background(), 0, ConnectionInput{ProviderName: "Codex", Protocol: Subscription, BaseURL: SubscriptionEndpoint + "/codex", ModelID: proof.ModelID})
			if err != nil {
				t.Fatal(err)
			}
			saved := save(t, s, p, "codex-save")
			p.Close()
			verified, err := s.VerifyModel(context.Background(), saved.Revision, "codex-verify", saved.ModelID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.SetMode(context.Background(), verified.Receipt.Revision, "codex-use", "agent", saved.ModelID); err != nil {
				t.Fatal(err)
			}
			lease, err := s.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			old, err := lease.Recipient()
			lease.Close()
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			g.mu.Lock()
			g.missing = true
			if changed {
				g.proof.AccountID = strings.Repeat("d", 64)
			}
			g.mu.Unlock()
			restarted, err := NewStore(s.directory, nil, g)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if _, err = restarted.BeginForContinuousRecipient(context.Background(), old); err == nil {
				t.Fatal("old helper proof survived restart")
			}
			fresh, err := restarted.RefreshContinuousRecipient(context.Background(), old)
			if changed {
				if err == nil {
					t.Fatal("changed account renewed the grant")
				}
				return
			}
			if err != nil || fresh.Subscription.ProofID == old.Subscription.ProofID || fresh.Subscription.AccountID != old.Subscription.AccountID {
				t.Fatal("same account failed to renew", err)
			}
			if _, err = restarted.Begin(context.Background()); !errors.Is(err, ErrStopped) {
				t.Fatal("renewal armed unrelated requests", err)
			}
			g.mu.Lock()
			g.missing = true
			g.mu.Unlock()
			preview, err := restarted.PreviewContinuousRecipient(context.Background())
			if err != nil || preview.Subscription.AccountID != old.Subscription.AccountID {
				t.Fatal("selected account could not be previewed after restart", err)
			}
			if _, err = restarted.Begin(context.Background()); !errors.Is(err, ErrStopped) {
				t.Fatal("preview armed unrelated requests", err)
			}
			state, err := restarted.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.SetMode(context.Background(), state.Revision, "codex-stop", "tool", ""); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.RefreshContinuousRecipient(context.Background(), fresh); err == nil {
				t.Fatal("disabled model renewed the grant")
			}
		})
	}
}
