//go:build darwin && cgo

package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

func TestNativeModelCredentialTransaction(t *testing.T) {
	if os.Getenv("WORKSHELF_TEST_KEYCHAIN") != "1" {
		t.Skip("native synthetic credential transaction requires opt-in")
	}
	directory := filepath.Join(t.TempDir(), "model-connections")
	namespace, err := NamespaceFor(directory)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credentials.NewVault(namespace)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(directory, vault, verifierFunc(passingVerifier))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	first := prepare(t, s, 0, "", "", "workshelf-native-fixture-key-one")
	defer first.Close()
	t.Cleanup(func() {
		if e := vault.Delete(ctx, first.secretRef); e != nil && !errors.Is(e, credentials.ErrNotFound) {
			t.Error("first synthetic credential cleanup failed", e)
		}
	})
	r := save(t, s, first, "native-create")
	second := prepare(t, s, r.Revision, r.ProviderID, r.ModelID, "workshelf-native-fixture-key-two")
	defer second.Close()
	t.Cleanup(func() {
		if e := vault.Delete(ctx, second.secretRef); e != nil && !errors.Is(e, credentials.ErrNotFound) {
			t.Error("second synthetic credential cleanup failed", e)
		}
	})
	r = save(t, s, second, "native-replace")
	if _, err = vault.Read(ctx, first.secretRef); err != credentials.ErrNotFound {
		t.Fatal("old credential not removed", err)
	}
	if _, err = s.DeleteProvider(ctx, r.Revision, "native-delete", r.ProviderID); err != nil {
		t.Fatal(err)
	}
	if _, err = vault.Read(ctx, second.secretRef); err != credentials.ErrNotFound {
		t.Fatal("new credential not removed", err)
	}
	state, err := s.Read(ctx)
	if err != nil || len(state.Providers) != 0 || state.PendingCredentialCleanup != 0 {
		t.Fatal(state, err)
	}
	t.Log("real Keychain plus atomic model metadata: create, replace, old-secret cleanup, delete and cleanup readback passed")
}
