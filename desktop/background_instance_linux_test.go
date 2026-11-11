//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestBackgroundInstanceIDIsValidDBusName(t *testing.T) {
	// This path's hash starts with a digit, which D-Bus rejects as an element's first character.
	t.Setenv("WORKSHELF_CONFIG", "/tmp/xuyu-test-config")
	id := backgroundInstanceID()
	parts := strings.Split(id, ".")
	last := parts[len(parts)-1]
	if last == "" || last[0] < 'a' || last[0] > 'z' {
		t.Fatalf("single-instance ID is not a valid D-Bus name: %s", id)
	}
}
