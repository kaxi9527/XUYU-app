package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomaticGrantRetryRevokeAndLegacyDigest(t *testing.T) {
	s, scope := memoryFixture(t)
	ctx := context.Background()
	initialize(t, s, scope)
	legacy := readMemory(t, s)
	b, _ := json.Marshal(legacy.State)
	if legacy.State.Automatic || strings.Contains(string(b), "automatic") {
		t.Fatal("old state digest changed", string(b))
	}
	if _, err := s.ObserveAutomatic(ctx, 1, "not-authorized", scope.Digest()); err == nil {
		t.Fatal("manual grant became automatic")
	}
	if readMemory(t, s).State.Revision != 1 {
		t.Fatal("rejection wrote history")
	}
	file := filepath.Join(scope.Roots[0], "result.txt")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	on, err := s.SetAutomatic(ctx, 1, "auto-on", scope.Digest(), true, testNow)
	if err != nil || on.Record.Revision != 2 {
		t.Fatal(on, err)
	}
	retry, err := s.SetAutomatic(ctx, 1, "auto-on", scope.Digest(), true, testNow.Add(time.Hour))
	if err != nil || retry.Record.Revision != 2 {
		t.Fatal("lost acknowledgement retry", retry, err)
	}
	first, err := s.ObserveAutomatic(ctx, 2, "auto-scan-2", scope.Digest())
	if err != nil || first.Record.Revision != 3 {
		t.Fatal(first, err)
	}
	if err = os.WriteFile(file, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	retry, err = s.ObserveAutomatic(ctx, 2, "auto-scan-2", scope.Digest())
	if err != nil || retry.Record.Revision != 3 {
		t.Fatal(retry, err)
	}
	for _, f := range readMemory(t, s).State.Files {
		if f.Fact.Path == file && f.Fact.SHA256 != digest([]byte("first")) {
			t.Fatal("retry scanned again")
		}
	}
	if _, err = s.ObserveAutomatic(ctx, 3, "bad-scope", digest([]byte("other"))); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.SetAutomatic(ctx, 3, "auto-off", scope.Digest(), false, testNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveAutomatic(ctx, 4, "after-revoke", scope.Digest()); err == nil {
		t.Fatal("revoked grant accepted")
	}
	if _, err = s.Observe(ctx, 4, "manual-after-auto", scope.Digest()); err != nil {
		t.Fatal("manual grant removed", err)
	}
	if _, err = s.SetAutomatic(ctx, 5, "auto-on-again", scope.Digest(), true, testNow.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTracking(ctx, 6, "pause", scope, false, testNow.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveAutomatic(ctx, 7, "paused-auto", scope.Digest()); err == nil {
		t.Fatal("paused scan accepted")
	}
	if _, err = s.SetAutomatic(ctx, 7, "disable-while-paused", scope.Digest(), false, testNow.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	v := readMemory(t, s)
	if v.State.Automatic || v.State.AutomaticSince != "" || v.State.Enabled {
		t.Fatal(v.State)
	}
	// Rebuild without projections proves the grant is in the event chain.
	if err = os.Remove(filepath.Join(s.directory, "index.json")); err != nil {
		t.Fatal(err)
	}
	if rebuilt := readMemory(t, s); digestJSON(rebuilt.State) != digestJSON(v.State) {
		t.Fatal("grant lost during replay")
	}
}

func TestAutomaticBudgetCancellationAndNoFileMutation(t *testing.T) {
	s, scope := memoryFixture(t)
	ctx := context.Background()
	initialize(t, s, scope)
	if _, err := s.SetAutomatic(ctx, 1, "auto-on", scope.Digest(), true, testNow); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(scope.Roots[0], "result.txt")
	if err := os.WriteFile(file, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ObserveAutomatic(cancelled, 2, "cancel", scope.Digest()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A sparse abandoned temporary segment consumes the same storage budget.
	large, err := os.Create(filepath.Join(s.directory, "events", ".memory-budget.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err = large.Truncate(automaticByteLimit); err != nil {
		t.Fatal(err)
	}
	if err = large.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveAutomatic(ctx, 2, "budget", scope.Digest()); !errors.Is(err, ErrAutomaticBudget) {
		t.Fatal(err)
	}
	if readMemory(t, s).State.Revision != 2 {
		t.Fatal("rejected scan appended events")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "untouched" {
		t.Fatal(string(data), err)
	}
	if _, err = s.SetAutomatic(ctx, 2, "stop-full-budget", scope.Digest(), false, testNow); err != nil {
		t.Fatal("capacity blocked revocation", err)
	}
}

func TestAutomaticDoesNotPassCommittedPause(t *testing.T) {
	s, scope := memoryFixture(t)
	ctx := context.Background()
	initialize(t, s, scope)
	if _, err := s.SetAutomatic(ctx, 1, "auto-on", scope.Digest(), true, testNow); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.afterCommit = func() error { close(entered); <-release; return nil }
	paused := make(chan error, 1)
	go func() { _, err := s.SetTracking(ctx, 2, "pause", scope, false, testNow); paused <- err }()
	<-entered
	scanned := make(chan error, 1)
	go func() { _, err := s.ObserveAutomatic(ctx, 2, "stale-auto", scope.Digest()); scanned <- err }()
	close(release)
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	if err := <-scanned; !errors.Is(err, ErrConflict) {
		t.Fatal("scan passed pause", err)
	}
	if v := readMemory(t, s); v.State.Revision != 3 || v.State.Enabled {
		t.Fatal(v.State)
	}
}
