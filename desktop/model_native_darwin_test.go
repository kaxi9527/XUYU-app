//go:build darwin && cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestNativeModelDesktopBridge(t *testing.T) {
	if os.Getenv("WORKSHELF_TEST_KEYCHAIN") != "1" {
		t.Skip("native synthetic Keychain test is opt-in")
	}
	parent := t.TempDir()
	configPath := filepath.Join(parent, "settings", "config.json")
	t.Setenv("WORKSHELF_CONFIG", configPath)
	svc := &ShelfService{}
	t.Cleanup(svc.closeModels)
	t.Cleanup(func() {
		state, err := svc.ModelConnections()
		if err != nil {
			t.Errorf("cleanup state: %v", err)
			return
		}
		for _, provider := range state.State.Providers {
			fresh, err := svc.ModelConnections()
			if err != nil {
				t.Errorf("cleanup state: %v", err)
				return
			}
			result := svc.DeleteModelProvider(provider.ID, fresh.State.Revision, "native-cleanup-"+provider.ID)
			if result.Error != "" {
				t.Errorf("cleanup provider: %s", result.Error)
			}
		}
		result := svc.RetryModelCredentialCleanup()
		if result.Error != "" || result.Pending == nil || *result.Pending != 0 {
			t.Errorf("cleanup incomplete: %+v", result)
		}
	})
	state, err := svc.ModelConnections()
	if err != nil || !state.CredentialStorage || !state.ProtocolReady || state.State.Settings.Mode != "tool" {
		t.Fatalf("native state: %+v %v", state, err)
	}
	if _, err := os.Stat(filepath.Dir(configPath)); !os.IsNotExist(err) {
		t.Fatal("ordinary read created model directory")
	}
	saved := modelTestSave(t, svc)
	directory := filepath.Join(filepath.Dir(configPath), "models")
	data, err := os.ReadFile(filepath.Join(directory, "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Providers []struct {
			SecretRef string `json:"secretRef"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || len(metadata.Providers) != 1 {
		t.Fatal("missing native connection metadata")
	}
	ref := metadata.Providers[0].SecretRef
	namespace, err := models.NamespaceFor(directory)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credentials.NewVault(namespace)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := vault.Read(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	secret.Destroy()
	// The live service remains bound to the original profile, not a mutable env.
	otherConfig := filepath.Join(parent, "other", "config.json")
	t.Setenv("WORKSHELF_CONFIG", otherConfig)
	recovered, err := svc.ModelOperation(saved.OperationID)
	if err != nil || !recovered.Found || recovered.Receipt.ProviderID != saved.ProviderID {
		t.Fatalf("profile drift: %+v %v", recovered, err)
	}
	deleted := svc.DeleteModelProvider(saved.ProviderID, saved.Revision, "native-delete")
	if deleted.Error != "" || deleted.Receipt == nil {
		t.Fatalf("native delete: %+v", deleted)
	}
	if secret, err := vault.Read(context.Background(), ref); !errors.Is(err, credentials.ErrNotFound) {
		secret.Destroy()
		t.Fatalf("synthetic key still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(otherConfig)); !os.IsNotExist(err) {
		t.Fatal("service silently wrote new config profile")
	}
	t.Log("desktop bridge used real Keychain with a synthetic key; saved receipt recovered, bound profile preserved, owned key deleted and absence verified")
}
