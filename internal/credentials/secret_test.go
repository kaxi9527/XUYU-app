package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSecretDoesNotSerializeOrPrint(t *testing.T) {
	input := []byte("synthetic-sensitive-value")
	secret, err := NewSecret(input)
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Destroy()
	input[0] = 'x'
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%x", "%q"} {
		if text := fmt.Sprintf(format, secret); !strings.Contains(text, "redacted") || strings.Contains(text, "sensitive") {
			t.Fatal("secret formatting leaked")
		}
		if text := fmt.Sprintf(format, *secret); !strings.Contains(text, "redacted") {
			t.Fatal("secret value formatting leaked")
		}
	}
	if _, err = json.Marshal(struct{ Secret *Secret }{secret}); !errors.Is(err, ErrSerialization) {
		t.Fatal("secret serialized", err)
	}
	bytes, err := secret.Bytes()
	if err != nil || bytes[0] != 's' {
		t.Fatal("secret aliasing", err)
	}
	Wipe(bytes)
	bytes, err = secret.Bytes()
	if err != nil || bytes[0] != 's' {
		t.Fatal("returned slice aliases secret", err)
	}
	Wipe(bytes)
	secret.Destroy()
	if _, err = secret.Bytes(); !errors.Is(err, ErrInvalid) {
		t.Fatal("destroyed value readable")
	}
}
func TestSecretLimitsReferencesAndConcurrentDestroy(t *testing.T) {
	for _, input := range [][]byte{nil, make([]byte, MaxSecretBytes+1)} {
		if _, err := NewSecret(input); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid length")
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		ref, err := NewReference()
		if err != nil || !ValidReference(ref) || seen[ref] {
			t.Fatal(ref, err)
		}
		seen[ref] = true
	}
	for _, namespace := range []string{"", "../other", "MixedCase", "a\x00b", strings.Repeat("a", 81)} {
		if _, err := NewVault(namespace); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid namespace")
		}
	}
	secret, _ := NewSecret([]byte("synthetic"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); bytes, _ := secret.Bytes(); Wipe(bytes); secret.Destroy() }()
	}
	wg.Wait()
}
