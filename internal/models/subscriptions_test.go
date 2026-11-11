package models

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSubscriptionAccountChangeInvalidatesModel(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	p, err := s.Prepare(ctx, 0, ConnectionInput{ProviderName: "订阅测试", Protocol: Subscription, BaseURL: SubscriptionEndpoint + "/codex", ModelID: "codex/fixture", ModelName: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	saved := save(t, s, p, "save-subscription")
	verified, err := s.VerifyModel(ctx, saved.Revision, "verify-subscription", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMode(ctx, verified.Receipt.Revision, "select-subscription", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := s.InvalidateSubscriptions(ctx, "codex"); err != nil {
		t.Fatal(err)
	}
	state, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Settings.Mode != "tool" || state.Models[0].Verification != nil {
		t.Fatal("old account verification retained")
	}
	select {
	case <-lease.Context.Done():
	default:
		t.Fatal("old account lease remains active")
	}
	if _, err := s.Prepare(ctx, state.Revision, ConnectionInput{ProviderName: "外部地址", Protocol: Subscription, BaseURL: "https://example.test", ModelID: "codex/fixture", ModelName: "Fixture"}); err == nil {
		t.Fatal("external subscription address accepted")
	}
}

func TestSubscriptionOwnerProcess(t *testing.T) {
	directory := os.Getenv("WORKSHELF_OWNER_FIXTURE")
	if directory == "" {
		return
	}
	s, err := NewStore(directory, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !errors.Is(s.ClaimSubscriptionOwnership(), ErrAccountOwned) {
		t.Fatal("another process acquired subscription ownership")
	}
}

func TestSubscriptionOwnerExcludesOtherStoresAndProcesses(t *testing.T) {
	s, _ := fixture(t)
	if err := s.ClaimSubscriptionOwnership(); err != nil {
		t.Fatal(err)
	}
	other, err := NewStore(s.directory, s.vault, s.verifier)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if !errors.Is(other.ClaimSubscriptionOwnership(), ErrAccountOwned) {
		t.Fatal("second store acquired account ownership")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSubscriptionOwnerProcess$")
	cmd.Env = append(os.Environ(), "WORKSHELF_OWNER_FIXTURE="+s.directory)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child ownership: %s %v", out, err)
	}
	s.Close()
	if err := other.ClaimSubscriptionOwnership(); err != nil {
		t.Fatal("released owner not reusable", err)
	}
}

func TestSubscriptionChangeBlocksVerificationAndDrainsLeases(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	p, err := s.Prepare(ctx, 0, ConnectionInput{ProviderName: "订阅", Protocol: Subscription, BaseURL: SubscriptionEndpoint + "/codex", ModelID: "codex/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	saved := save(t, s, p, "save-change")
	verified, err := s.VerifyModel(ctx, saved.Revision, "verify-before", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetMode(ctx, verified.Receipt.Revision, "mode-before", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	entered, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		_ = lease.WithConnection(func(c context.Context, _ Target, _ *credentials.Secret) error {
			close(entered)
			<-c.Done()
			return c.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("lease callback not started")
	}
	release, err := s.BeginSubscriptionChange(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("old callback was not drained")
	}
	state, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyModel(ctx, state.Revision, "verify-during", saved.ModelID); !errors.Is(err, ErrAccountChanging) {
		t.Fatal("verification during change", err)
	}
	if _, err = s.SetMode(ctx, state.Revision, "mode-during", "agent", saved.ModelID); err == nil {
		t.Fatal("enabled during change")
	}
	if _, err = s.Begin(ctx); err == nil {
		t.Fatal("new lease during change")
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	state, err = s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Models[0].Verification != nil {
		t.Fatal("old verification restored")
	}
	if _, err = s.VerifyModel(ctx, state.Revision, "verify-after", saved.ModelID); err != nil {
		t.Fatal("verification after change", err)
	}
}
