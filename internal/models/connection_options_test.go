package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

func TestConnectionOptionsValidateAndInvalidate(t *testing.T) {
	for _, in := range []Options{{AuthHeader: "Host"}, {AuthHeader: "Cookie"}, {AuthHeader: "X-Forwarded-Host"}, {AuthHeader: "X-Key\r\n"}, {AuthHeader: "Content-Length"}, {ChatTokenField: "skip-limit"}, {ProbeTokens: 1}, {ProbeTokens: 4097}, {AnthropicVersion: "2026-10-21"}} {
		if _, err := NormalizeOptions(Chat, in); err == nil {
			t.Fatalf("invalid options accepted: %+v", in)
		}
	}
	if _, err := NormalizeOptions(Anthropic, Options{AnthropicVersion: "2026-13-01"}); err == nil {
		t.Fatal("invalid version date")
	}
	defaults, err := NormalizeOptions(Chat, Options{})
	if err != nil || defaults.ChatTokenField != "max_completion_tokens" || defaults.ProbeTokens != 256 || defaults.AuthHeader != "Authorization" {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	s, vault := fixture(t)
	ctx := context.Background()
	saved := save(t, s, prepare(t, s, 0, "", "", "synthetic"), "save")
	verified, err := s.VerifyModel(ctx, saved.Revision, "verify", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.SetMode(ctx, verified.Receipt.Revision, "start", "agent", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	input := ConnectionInput{ProviderID: saved.ProviderID, ModelRecordID: saved.ModelID, ProviderName: "测试提供商", Protocol: Chat, BaseURL: "http://127.0.0.1:12345/v1", ModelID: "test-model", ModelName: "测试模型", Options: Options{AuthHeader: "X-Custom-Key", ChatTokenField: "max_tokens", ProbeTokens: 512}}
	prepared, err := s.Prepare(ctx, started.Revision, input)
	if !errors.Is(err, ErrCredentialScope) {
		t.Fatal("changed auth header reused the saved key", err)
	}
	key, err := credentials.NewSecret([]byte("explicitly-reentered-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer key.Destroy()
	input.Credential = key
	prepared, err = s.Prepare(ctx, started.Revision, input)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	updated := save(t, s, prepared, "options")
	state, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != updated.Revision || state.Settings.Mode != "tool" || state.Models[0].Verification != nil || state.Providers[0].Options != prepared.Preview().Options || len(vault.data) != 1 {
		t.Fatalf("options did not invalidate or retained wrong key: %+v", state)
	}
	select {
	case <-lease.Context.Done():
	default:
		t.Fatal("options change did not cancel lease")
	}
}

func TestLegacyOptionsReadAndSafeFailureReceipt(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	saved := save(t, s, prepare(t, s, 0, "", "", ""), "save")
	path := filepath.Join(s.directory, "connections.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if json.Unmarshal(data, &old) != nil {
		t.Fatal("fixture decode")
	}
	for _, provider := range old["providers"].([]any) {
		delete(provider.(map[string]any), "options")
	}
	data, _ = json.Marshal(old)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := s.Read(ctx)
	if err != nil || state.Providers[0].Options.ProbeTokens != 256 {
		t.Fatalf("legacy optional options: %+v %v", state, err)
	}
	s.verifier = verifierFunc(func(context.Context, Target, *credentials.Secret) (Capabilities, error) {
		return Capabilities{}, errors.New("upstream-private-key-and-url")
	})
	failed, err := s.VerifyModel(ctx, saved.Revision, "fail", saved.ModelID)
	if err != nil || failed.Verified || failed.Receipt.Problem == nil || failed.Receipt.Problem.Code != "verification_failed" {
		t.Fatalf("safe failure %+v %v", failed, err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "upstream-private-key-and-url") {
		t.Fatal("raw error persisted")
	}
	stored, err := s.Operation(ctx, "fail")
	if err != nil || stored.Problem == nil || stored.Problem.Code != "verification_failed" {
		t.Fatalf("failure recovery: %+v %v", stored, err)
	}
	s.verifier = verifierFunc(func(context.Context, Target, *credentials.Secret) (Capabilities, error) {
		return Capabilities{}, &ConnectionProblem{Code: "rate_limited", HTTPStatus: 429, RetryAfterSeconds: 45}
	})
	limited, err := s.VerifyModel(ctx, failed.Receipt.Revision, "limited", saved.ModelID)
	if err != nil || limited.Receipt.Problem == nil || limited.Receipt.Problem.RetryAfterSeconds != 45 {
		t.Fatalf("classified failure: %+v %v", limited, err)
	}
}

func TestLongCustomModelIDKeepsIdentity(t *testing.T) {
	s, _ := fixture(t)
	id := strings.Repeat("m", 180)
	prepared, err := s.Prepare(context.Background(), 0, ConnectionInput{ProviderName: "Custom", Protocol: Chat, BaseURL: "http://localhost:1234/v1", ModelID: id})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	preview := prepared.Preview()
	if preview.ModelID != id || len([]rune(preview.ModelName)) != 120 {
		t.Fatal("display fallback changed model identity")
	}
}

type discoveryFixture struct {
	list func(context.Context, Target, *credentials.Secret, string) (ModelPage, error)
}

func (d discoveryFixture) Verify(ctx context.Context, t Target, s *credentials.Secret) (Capabilities, error) {
	return passingVerifier(ctx, t, s)
}
func (d discoveryFixture) ListModels(ctx context.Context, t Target, s *credentials.Secret, cursor string) (ModelPage, error) {
	return d.list(ctx, t, s, cursor)
}

func TestModelDiscoveryRejectsLateResultAndDestroysSecret(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	saved := save(t, s, prepare(t, s, 0, "", "", "synthetic"), "save")
	entered, release := make(chan struct{}), make(chan struct{})
	var retained *credentials.Secret
	s.verifier = discoveryFixture{list: func(ctx context.Context, target Target, secret *credentials.Secret, cursor string) (ModelPage, error) {
		retained = secret
		if target.ModelID != "" || target.BaseURL != "http://127.0.0.1:12345/v1" || cursor != "page" {
			t.Error("wrong discovery scope")
		}
		close(entered)
		<-release
		return ModelPage{Models: []AvailableModel{{ID: "remote", Name: "Remote"}}}, nil
	}}
	done := make(chan error, 1)
	go func() { _, err := s.DiscoverModels(ctx, saved.Revision, saved.ProviderID, "page"); done <- err }()
	<-entered
	if _, err := s.SetMode(ctx, saved.Revision, "stop", "tool", ""); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("late list accepted: %v", err)
	}
	if _, err := retained.Bytes(); !errors.Is(err, credentials.ErrInvalid) {
		t.Fatal("discovery retained usable secret")
	}
	state, err := s.Read(ctx)
	if err != nil || state.Models[0].Verification != nil || len(state.Models) != 1 {
		t.Fatal("listing silently registered or verified model")
	}
}

func TestModelDiscoveryCancellationAndClosedStore(t *testing.T) {
	s, _ := fixture(t)
	saved := save(t, s, prepare(t, s, 0, "", "", ""), "save")
	entered := make(chan struct{})
	s.verifier = discoveryFixture{list: func(ctx context.Context, _ Target, _ *credentials.Secret, _ string) (ModelPage, error) {
		close(entered)
		<-ctx.Done()
		return ModelPage{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.DiscoverModels(ctx, saved.Revision, saved.ProviderID, ""); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.Close()
	if _, err := s.DiscoverModels(context.Background(), saved.Revision, saved.ProviderID, ""); !errors.Is(err, ErrStopped) {
		t.Fatalf("closed discovery: %v", err)
	}
}
