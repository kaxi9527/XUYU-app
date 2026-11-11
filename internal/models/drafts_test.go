package models

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type draftAdapter struct {
	verify func(context.Context, Target, *credentials.Secret) (Capabilities, error)
	list   func(context.Context, Target, *credentials.Secret, string) (ModelPage, error)
}

func (a draftAdapter) Verify(c context.Context, t Target, k *credentials.Secret) (Capabilities, error) {
	return a.verify(c, t, k)
}
func (a draftAdapter) ListModels(c context.Context, t Target, k *credentials.Secret, x string) (ModelPage, error) {
	return a.list(c, t, k, x)
}
func draftInput(t *testing.T) ConnectionInput {
	t.Helper()
	secret, err := credentials.NewSecret([]byte("draft-fixture-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secret.Destroy)
	return ConnectionInput{ProviderName: "Draft fixture", Protocol: Chat, BaseURL: "http://127.0.0.1:12345/v1", Credential: secret}
}
func makeDraft(t *testing.T, s *Store, revision uint64, in ConnectionInput) *Draft {
	t.Helper()
	d, err := s.PrepareDraft(context.Background(), revision, in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}
func verifyDraft(t *testing.T, d *Draft, id string) {
	t.Helper()
	if err := d.SelectModel(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	result, err := d.Verify(context.Background())
	if err != nil || !result.Verified {
		t.Fatal("draft not verified", err, result)
	}
}
func TestDraftDiscoveryAndVerificationDoNotSave(t *testing.T) {
	s, vault := fixture(t)
	ctx := context.Background()
	probes := 0
	s.verifier = draftAdapter{verify: func(c context.Context, target Target, key *credentials.Secret) (Capabilities, error) {
		probes++
		b, _ := key.Bytes()
		defer credentials.Wipe(b)
		if string(b) != "draft-fixture-secret" || target.ModelID != "fixture-model" {
			t.Fatal("wrong temporary probe")
		}
		return passingVerifier(c, target, key)
	}, list: func(_ context.Context, target Target, key *credentials.Secret, cursor string) (ModelPage, error) {
		if target.ModelID != "" || key == nil {
			t.Fatal("list requires no guessed model ID")
		}
		return ModelPage{Models: []AvailableModel{{ID: "fixture-model", Name: "Fixture"}}}, nil
	}}
	d := makeDraft(t, s, 0, draftInput(t))
	page, err := d.Discover(ctx, "")
	if err != nil || len(page.Models) != 1 {
		t.Fatal("list", err)
	}
	verifyDraft(t, d, page.Models[0].ID)
	state, err := s.Read(ctx)
	if err != nil || state.Revision != 0 || len(state.Providers) != 0 || len(vault.data) != 0 {
		t.Fatal("draft persisted state", err)
	}
	if _, err = os.Stat(filepath.Join(s.directory, "connections.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("draft wrote catalog", err)
	}
	receipt, err := s.SaveVerifiedDraft(ctx, d, "save-and-use", true)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SaveVerifiedDraft(ctx, d, "save-and-use", true)
	if err != nil || replay.Revision != receipt.Revision {
		t.Fatal("save replay", err)
	}
	state, err = s.Read(ctx)
	if err != nil || state.Revision != 1 || state.Settings.Mode != "agent" || state.Settings.ActiveModelID != receipt.ModelID || state.Models[0].Verification == nil || len(vault.data) != 1 || probes != 1 {
		t.Fatal("not one atomic verified activation", err)
	}
	bytes, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	if strings.Contains(string(bytes), "draft-fixture-secret") {
		t.Fatal("secret persisted in catalog")
	}
	lease, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}
func TestDraftSelectionExpiryAndOwnershipRejectOldProof(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	d := makeDraft(t, s, 0, draftInput(t))
	verifyDraft(t, d, "first")
	if err := d.SelectModel(ctx, "second", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveVerifiedDraft(ctx, d, "stale-model-proof", true); !errors.Is(err, ErrUnverified) {
		t.Fatal("changed model retained proof", err)
	}
	verifyDraft(t, d, "second")
	other, _ := fixture(t)
	if _, err := other.SaveVerifiedDraft(ctx, d, "foreign-proof", true); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign proof accepted", err)
	}
	d.expires = time.Now().Add(-time.Second)
	if _, err := s.SaveVerifiedDraft(ctx, d, "expired-proof", true); !errors.Is(err, ErrDraftExpired) {
		t.Fatal("expired draft saved", err)
	}
	secret := d.prepared.secret
	d.Close()
	if _, err := secret.Bytes(); err == nil {
		t.Fatal("discard retained secret")
	}
}
func TestDraftReceiverChangesNeverInheritSavedKey(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	saved := save(t, s, prepare(t, s, 0, "", "", "saved-fixture"), "existing")
	base := ConnectionInput{ProviderID: saved.ProviderID, ProviderName: "Existing", Protocol: Chat, BaseURL: "http://127.0.0.1:12345/v1", ModelID: "test-model"}
	for _, change := range []func(*ConnectionInput){func(i *ConnectionInput) { i.BaseURL = "https://other.example/v1" }, func(i *ConnectionInput) { i.Protocol = Responses }, func(i *ConnectionInput) { i.Options.AuthHeader = "X-Different-Key" }} {
		in := base
		change(&in)
		if _, err := s.PrepareDraft(ctx, saved.Revision, in); !errors.Is(err, ErrCredentialScope) {
			t.Fatal("draft receiver inherited key", err)
		}
		if _, err := s.Prepare(ctx, saved.Revision, in); !errors.Is(err, ErrCredentialScope) {
			t.Fatal("legacy save bypassed scope", err)
		}
		in.RemoveCredential = true
		d := makeDraft(t, s, saved.Revision, in)
		d.Close()
	}
	base.Options.ProbeTokens = 512
	d := makeDraft(t, s, saved.Revision, base)
	s.verifier = verifierFunc(func(c context.Context, target Target, key *credentials.Secret) (Capabilities, error) {
		b, _ := key.Bytes()
		defer credentials.Wipe(b)
		if string(b) != "saved-fixture" {
			t.Fatal("same-receiver key unavailable")
		}
		return passingVerifier(c, target, key)
	})
	verifyDraft(t, d, "test-model")
}
func TestDraftFailureCancellationAndConflictPreserveState(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	saved := save(t, s, prepare(t, s, 0, "", "", "old-key"), "existing")
	verified, err := s.VerifyModel(ctx, saved.Revision, "old-verified", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.SetMode(ctx, verified.Receipt.Revision, "old-active", "agent", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	in := draftInput(t)
	in.ProviderID = saved.ProviderID
	in.ModelRecordID = saved.ModelID
	in.ModelID = "new-model"
	d := makeDraft(t, s, active.Revision, in)
	s.verifier = verifierFunc(func(context.Context, Target, *credentials.Secret) (Capabilities, error) {
		return Capabilities{}, &ConnectionProblem{Code: "authentication"}
	})
	result, err := d.Verify(ctx)
	if err != nil || result.Verified || result.Problem.Code != "authentication" {
		t.Fatal("wrong failure", err)
	}
	if _, err = s.SaveVerifiedDraft(ctx, d, "failed-proof", true); !errors.Is(err, ErrUnverified) {
		t.Fatal("failed proof saved", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = d.Verify(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.directory, "connections.json"))
	if string(before) != string(after) {
		t.Fatal("failed draft changed active connection")
	}
	s.verifier = verifierFunc(passingVerifier)
	verifyDraft(t, d, "new-model")
	s.replace = func(old, next string) error {
		if filepath.Base(next) == "connections.json" {
			return os.ErrPermission
		}
		return os.Rename(old, next)
	}
	if _, err = s.SaveVerifiedDraft(ctx, d, "write-failed", true); err == nil {
		t.Fatal("injected failure missed")
	}
	after, _ = os.ReadFile(filepath.Join(s.directory, "connections.json"))
	if string(before) != string(after) {
		t.Fatal("failed commit changed original")
	}
	s.replace = os.Rename
	if _, err = s.SetMode(ctx, active.Revision, "external-change", "tool", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveVerifiedDraft(ctx, d, "stale-revision", true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale proof survived revision", err)
	}
}
