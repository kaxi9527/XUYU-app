//go:build darwin && cgo

package credentials

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Explicit opt-in writes only synthetic content under a fresh test namespace.
// It never reads, changes or enumerates user credentials or keychain settings.
func TestNativeKeychainRoundTrip(t *testing.T) {
	if os.Getenv("WORKSHELF_TEST_KEYCHAIN") != "1" {
		t.Skip("native synthetic credential round requires opt-in")
	}
	ref, _ := NewReference()
	namespace := "qa-" + strings.TrimPrefix(ref, "cred_")
	vault, err := NewVault(namespace)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	secret, _ := NewSecret([]byte("workshelf-test-only-not-a-real-api-key"))
	defer secret.Destroy()
	if err = vault.Create(ctx, ref, secret); err != nil {
		t.Fatal("native synthetic create", err)
	}
	t.Cleanup(func() {
		err := vault.Delete(context.Background(), ref)
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Error("test credential cleanup failed", err)
		}
	})
	if err = vault.Create(ctx, ref, secret); !errors.Is(err, ErrExists) {
		t.Fatal("duplicate credential overwrote", err)
	}
	read, err := vault.Read(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Destroy()
	bytes, err := read.Bytes()
	if err != nil || string(bytes) != "workshelf-test-only-not-a-real-api-key" {
		t.Fatal("wrong secret contents", err)
	}
	Wipe(bytes)
	other, _ := NewVault(namespace + "-other")
	if _, err = other.Read(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatal("namespace isolation", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = vault.Delete(cancelled, ref); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	readAgain, err := vault.Read(ctx, ref)
	if err != nil {
		t.Fatal("cancelled delete removed secret", err)
	}
	readAgain.Destroy()
	if err = vault.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = vault.Read(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete not effective", err)
	}
	t.Log("synthetic keychain create/read/no-overwrite/isolation/cancel/delete passed; owned credential removed")
}
