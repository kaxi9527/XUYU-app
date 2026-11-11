package agentdiscovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticCheckDefaultOffDoesNotAllocate(t *testing.T) {
	s, scanner, now := newTestStore(t)
	r, err := s.CheckAutomatic(context.Background(), scanner, now)
	if err != nil || r.Ran || r.Receipt.Revision != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Stat(s.directory); !os.IsNotExist(err) {
		t.Fatal("default-off check created state", err)
	}
}

func TestAutomaticCheckRechecksDisabledPolicyUnderLock(t *testing.T) {
	s, scanner, now, r := enrollmentFixture(t)
	ctx := context.Background()
	r, err := s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := s.Read(ctx)
	if err != nil || !observed.Policy.Enabled {
		t.Fatal(err)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Another writer has disabled the policy after the worker's optimistic read.
	r.Policy.Enabled = false
	r, err = s.recordEnrollment(ctx, r, "enrollment_policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	scanner.Home = filepath.Join(scanner.Home, "must-not-be-scanned")
	result, err := s.checkAutomaticLocked(ctx, scanner, now.Add(48*time.Hour))
	if err != nil || result.Ran || result.Receipt.Revision != r.Revision || result.Receipt.Policy.Enabled {
		t.Fatalf("disabled guard failed: %+v %v", result, err)
	}
}
