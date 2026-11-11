package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

type memoryVault struct {
	mu                     sync.Mutex
	data                   map[string][]byte
	failCreate, failDelete bool
}

func newMemoryVault() *memoryVault { return &memoryVault{data: map[string][]byte{}} }
func (v *memoryVault) Create(ctx context.Context, id string, s *credentials.Secret) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failCreate {
		return credentials.ErrLocked
	}
	if _, ok := v.data[id]; ok {
		return credentials.ErrExists
	}
	b, e := s.Bytes()
	if e == nil {
		v.data[id] = b
	}
	return e
}
func (v *memoryVault) Read(ctx context.Context, id string) (*credentials.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.data[id]
	if !ok {
		return nil, credentials.ErrNotFound
	}
	return credentials.NewSecret(b)
}
func (v *memoryVault) Delete(ctx context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failDelete {
		return credentials.ErrLocked
	}
	b, ok := v.data[id]
	if !ok {
		return credentials.ErrNotFound
	}
	credentials.Wipe(b)
	delete(v.data, id)
	return nil
}

type verifierFunc func(context.Context, Target, *credentials.Secret) (Capabilities, error)

func (f verifierFunc) Verify(ctx context.Context, t Target, s *credentials.Secret) (Capabilities, error) {
	return f(ctx, t, s)
}
func passingVerifier(context.Context, Target, *credentials.Secret) (Capabilities, error) {
	return Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
}
func fixture(t *testing.T) (*Store, *memoryVault) {
	t.Helper()
	vault := newMemoryVault()
	s, e := NewStore(filepath.Join(t.TempDir(), "models"), vault, verifierFunc(passingVerifier))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s, vault
}
func prepare(t *testing.T, s *Store, revision uint64, providerID, modelID, key string) *Prepared {
	t.Helper()
	in := ConnectionInput{ProviderID: providerID, ModelRecordID: modelID, ProviderName: "测试提供商", Protocol: Chat, BaseURL: "http://127.0.0.1:12345/v1/", ModelID: "test-model", ModelName: "测试模型"}
	if key != "" {
		secret, e := credentials.NewSecret([]byte(key))
		if e != nil {
			t.Fatal(e)
		}
		defer secret.Destroy()
		in.Credential = secret
	}
	p, e := s.Prepare(context.Background(), revision, in)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	return p
}
func save(t *testing.T, s *Store, p *Prepared, op string) Receipt {
	t.Helper()
	r, e := s.Save(context.Background(), p, op)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestEmptyReadSaveSecretAndIdempotency(t *testing.T) {
	s, v := fixture(t)
	ctx := context.Background()
	state, e := s.Read(ctx)
	if e != nil || state.Settings.Mode != "tool" || state.Revision != 0 {
		t.Fatal(state, e)
	}
	if _, e = os.Stat(s.directory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("empty read created storage")
	}
	p := prepare(t, s, 0, "", "", "synthetic-api-key")
	if _, e = os.Stat(s.directory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("prepare wrote storage")
	}
	r := save(t, s, p, "save-first")
	replay, e := s.Save(ctx, p, "save-first")
	if e != nil || replay != r {
		t.Fatal(replay, e)
	}
	state, e = s.Read(ctx)
	if e != nil || len(state.Providers) != 1 || !state.Providers[0].HasCredential || state.Providers[0].BaseURL != "http://127.0.0.1:12345/v1" {
		t.Fatal(state, e)
	}
	jsonState, _ := json.Marshal(state)
	if strings.Contains(string(jsonState), "cred_") {
		t.Fatal("private reference returned to UI")
	}
	entries, _ := os.ReadDir(s.directory)
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(s.directory, entry.Name()))
		if strings.Contains(string(data), "synthetic-api-key") {
			t.Fatal("credential leaked to disk")
		}
	}
	if len(v.data) != 1 {
		t.Fatal("wrong credential count")
	}
	other := prepare(t, s, state.Revision, r.ProviderID, r.ModelID, "different-key")
	if _, e = s.Save(ctx, other, "save-first"); !errors.Is(e, ErrReused) {
		t.Fatal("operation reuse accepted", e)
	}
}

func TestVerifyModeStopAndRestartRequireExplicitResume(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	r := save(t, s, prepare(t, s, 0, "", "", "key"), "save")
	if _, e := s.SetMode(ctx, r.Revision, "unverified", "agent", r.ModelID); !errors.Is(e, ErrUnverified) {
		t.Fatal(e)
	}
	verified, e := s.VerifyModel(ctx, r.Revision, "verify", r.ModelID)
	if e != nil || !verified.Verified {
		t.Fatal(verified, e)
	}
	on, e := s.SetMode(ctx, verified.Receipt.Revision, "enable", "agent", r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.Begin(ctx)
	if e != nil || lease.Check() != nil {
		t.Fatal(e)
	}
	reopened, e := NewStore(s.directory, s.vault, s.verifier)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, e = reopened.Begin(ctx); !errors.Is(e, ErrStopped) {
		t.Fatal("restart auto-resumed Agent", e)
	}
	off, e := s.SetMode(ctx, on.Revision, "disable", "tool", "")
	if e != nil || off.Revision <= on.Revision {
		t.Fatal(off, e)
	}
	select {
	case <-lease.Context.Done():
	default:
		t.Fatal("running lease not cancelled")
	}
	if e = lease.Check(); !errors.Is(e, ErrStopped) {
		t.Fatal(e)
	}
	if _, e = s.Begin(ctx); !errors.Is(e, ErrStopped) {
		t.Fatal(e)
	}
	s.Close()
	if _, e = s.SetMode(ctx, off.Revision, "closed", "tool", ""); !errors.Is(e, ErrStopped) {
		t.Fatal("closed store accepted a write", e)
	}
}

func TestCredentialReplacementFailureRecoveryAndCleanupWarning(t *testing.T) {
	s, v := fixture(t)
	ctx := context.Background()
	first := prepare(t, s, 0, "", "", "first-key")
	r := save(t, s, first, "first")
	before, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	replacement := prepare(t, s, r.Revision, r.ProviderID, r.ModelID, "new-key")
	s.replace = func(a, b string) error {
		if filepath.Base(b) == "connections.json" {
			return errors.New("simulated disk failure")
		}
		return os.Rename(a, b)
	}
	if _, e := s.Save(ctx, replacement, "replace"); e == nil {
		t.Fatal("expected write failure")
	}
	after, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	if string(before) != string(after) {
		t.Fatal("failed replacement changed connection")
	}
	if len(v.data) != 2 {
		t.Fatal("expected journaled orphan before cleanup")
	}
	restarted, e := NewStore(s.directory, v, s.verifier)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	if e = restarted.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if len(v.data) != 1 || string(v.data[first.secretRef]) != "first-key" {
		t.Fatal("recovery removed current credential")
	}
	v.failDelete = true
	r2 := save(t, restarted, replacement, "replace")
	state, e := restarted.Read(ctx)
	if e != nil || state.PendingCredentialCleanup != 1 {
		t.Fatal(state, e)
	}
	if string(v.data[replacement.secretRef]) != "new-key" {
		t.Fatal("new credential missing")
	}
	v.failDelete = false
	if e = restarted.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if len(v.data) != 1 {
		t.Fatal("old credential not removed")
	}
	if _, e = restarted.DeleteProvider(ctx, r2.Revision, "delete", r.ProviderID); e != nil {
		t.Fatal(e)
	}
	state, e = restarted.Read(ctx)
	if e != nil || len(state.Models) != 0 || len(state.Providers) != 0 || len(v.data) != 0 {
		t.Fatal(state, e)
	}
}

func TestKeychainFailureAndCorruptConfigNeverFallBack(t *testing.T) {
	s, v := fixture(t)
	ctx := context.Background()
	p := prepare(t, s, 0, "", "", "test-only-secret")
	v.failCreate = true
	if _, e := s.Save(ctx, p, "keychain-failed"); !errors.Is(e, credentials.ErrLocked) {
		t.Fatal(e)
	}
	state, e := s.Read(ctx)
	if e != nil || state.Revision != 0 || len(state.Providers) != 0 {
		t.Fatal(state, e)
	}
	v.failCreate = false
	r := save(t, s, p, "retry")
	data := []byte(`{"schema":99}`)
	if e = os.WriteFile(filepath.Join(s.directory, "connections.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	if e = s.Cleanup(ctx); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	if len(v.data) != 1 {
		t.Fatal("corrupt config caused secret cleanup")
	}
	if _, e = s.SetMode(ctx, r.Revision, "corrupt", "tool", ""); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	if string(actual) != string(data) {
		t.Fatal("corrupt data overwritten")
	}
}

func TestConcurrentStoreSaveAndStaleVerification(t *testing.T) {
	s, v := fixture(t)
	ctx := context.Background()
	other, e := NewStore(s.directory, v, s.verifier)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	first, second := prepare(t, s, 0, "", "", "first"), prepare(t, other, 0, "", "", "second")
	errorsOut := make(chan error, 2)
	go func() { _, e := s.Save(ctx, first, "one"); errorsOut <- e }()
	go func() { _, e := other.Save(ctx, second, "two"); errorsOut <- e }()
	a, b := <-errorsOut, <-errorsOut
	if (a == nil) == (b == nil) || (!errors.Is(a, ErrConflict) && !errors.Is(b, ErrConflict)) {
		t.Fatal(a, b)
	}
	state, e := s.Read(ctx)
	if e != nil || len(state.Providers) != 1 || len(v.data) != 1 {
		t.Fatal(state, e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.verifier = verifierFunc(func(context.Context, Target, *credentials.Secret) (Capabilities, error) {
		close(entered)
		<-release
		return passingVerifier(ctx, Target{}, nil)
	})
	verified := make(chan error, 1)
	go func() { _, err := s.VerifyModel(ctx, state.Revision, "slow", state.Models[0].ID); verified <- err }()
	<-entered
	if _, e = other.SetMode(ctx, state.Revision, "changed-while-verifying", "tool", ""); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-verified; !errors.Is(e, ErrConflict) {
		t.Fatal("stale probe accepted", e)
	}
	state, _ = s.Read(ctx)
	if state.Models[0].Verification != nil {
		t.Fatal("stale capability persisted")
	}
}

func TestChangedProviderAndFailedProbeRevokeVerification(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	r := save(t, s, prepare(t, s, 0, "", "", "first"), "first")
	providerID := r.ProviderID
	v, e := s.VerifyModel(ctx, r.Revision, "verify", r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	r, e = s.SetMode(ctx, v.Receipt.Revision, "on", "agent", r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r2 := save(t, s, prepare(t, s, r.Revision, providerID, r.ModelID, "second"), "replace")
	state, _ := s.Read(ctx)
	if state.Settings.Mode != "tool" || state.Models[0].Verification != nil || lease.Check() == nil {
		t.Fatal("provider change left active verification")
	}
	v, e = s.VerifyModel(ctx, r2.Revision, "verify-new", r.ModelID)
	if e != nil || !v.Verified {
		t.Fatal(v, e)
	}
	s.verifier = verifierFunc(func(context.Context, Target, *credentials.Secret) (Capabilities, error) {
		return Capabilities{}, errors.New("upstream response containing potentially private text")
	})
	failed, e := s.VerifyModel(ctx, v.Receipt.Revision, "probe-fails", r.ModelID)
	if e != nil || failed.Verified {
		t.Fatal(failed, e)
	}
	state, _ = s.Read(ctx)
	if state.Models[0].Verification != nil {
		t.Fatal("failed probe preserved verification")
	}
	replay, e := s.VerifyModel(ctx, r2.Revision, "verify-new", r.ModelID)
	if e != nil || !replay.Verified {
		t.Fatal("old receipt lost its original outcome", e)
	}
}

func TestEndpointPolicyAndSymlinkProtection(t *testing.T) {
	for _, raw := range []string{"http://example.com/v1", "https://name:secret@example.com", "https://example.com/v1?key=secret", "https://example.com/#key", "file:///tmp/x", "http://localhost.evil/v1", "https://example.com/%2e%2e/x", "https://example.com/v1%2fprivate", "https://example.com:99999/v1"} {
		if _, e := normalizeEndpoint(raw); e == nil {
			t.Fatal("unsafe endpoint accepted", raw)
		}
	}
	for _, raw := range []string{"https://example.com/v1", "http://localhost:11434/v1", "http://127.0.0.1:1234/v1", "http://[::1]:1234/v1"} {
		if _, e := normalizeEndpoint(raw); e != nil {
			t.Fatal(raw, e)
		}
	}
	s, _ := fixture(t)
	p := prepare(t, s, 0, "", "", "")
	if e := os.MkdirAll(s.directory, 0700); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "untouched.json")
	os.WriteFile(outside, []byte("untouched"), 0600)
	os.Symlink(outside, filepath.Join(s.directory, "connections.json"))
	if _, e := s.Save(context.Background(), p, "symlink"); e == nil {
		t.Fatal("symlink accepted")
	}
	actual, _ := os.ReadFile(outside)
	if string(actual) != "untouched" {
		t.Fatal("outside file overwritten")
	}
}

func TestAddingModelPreservesOtherVerificationAndLeaseSecrets(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	r := save(t, s, prepare(t, s, 0, "", "", "fixture-secret"), "initial")
	v, e := s.VerifyModel(ctx, r.Revision, "verify", r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	on, e := s.SetMode(ctx, v.Receipt.Revision, "on", "agent", r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	in := ConnectionInput{ProviderID: r.ProviderID, ProviderName: "测试提供商", Protocol: Chat, BaseURL: "http://127.0.0.1:12345/v1", ModelID: "second-model", ModelName: "第二模型"}
	p, e := s.Prepare(ctx, on.Revision, in)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	r2 := save(t, s, p, "second-model")
	state, e := s.Read(ctx)
	if e != nil || len(state.Models) != 2 || state.Models[0].Verification == nil || state.Settings.Mode != "agent" {
		t.Fatal(state, e)
	}
	duplicate, e := s.Prepare(ctx, r2.Revision, in)
	if e != nil {
		t.Fatal(e)
	}
	defer duplicate.Close()
	if duplicate.modelID != p.modelID {
		t.Fatal("exact model duplicate")
	}
	lease, e := s.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var retained *credentials.Secret
	e = lease.WithConnection(func(ctx context.Context, target Target, secret *credentials.Secret) error {
		if target.ModelID != "test-model" {
			t.Fatal(target)
		}
		b, e := secret.Bytes()
		if e != nil || string(b) != "fixture-secret" {
			t.Fatal("lease secret incorrect", e)
		}
		credentials.Wipe(b)
		retained = secret
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = retained.Bytes(); !errors.Is(e, credentials.ErrInvalid) {
		t.Fatal("callback retained usable secret")
	}
	other, _ := fixture(t)
	if _, e = other.Save(ctx, p, "foreign-preview"); e == nil {
		t.Fatal("preview crossed configuration store")
	}
}
