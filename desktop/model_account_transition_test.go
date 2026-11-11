package main

import (
	"context"
	"encoding/json"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func transitionFixture(t *testing.T) (*ShelfService, string) {
	t.Helper()
	s, _, _ := modelTestService(t, modelTestVerifier(modelTestReady))
	p, err := s.PreviewModelConnection(ModelConnectionInput{ProviderName: "Fixture subscription", Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/codex", ModelID: "codex/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	saved := s.SaveModelConnection(p.Token, "transition-save")
	if saved.Error != "" || saved.Receipt == nil {
		t.Fatal("fixture save", saved.Error)
	}
	return s, saved.Receipt.ModelID
}
func blockVerification(t *testing.T, s *ShelfService, id, operation string) {
	t.Helper()
	state, err := s.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	result := s.VerifyModelConnection(id, state.State.Revision, operation)
	if result.Error == "" {
		t.Fatal("verification allowed during account change")
	}
	if _, err := s.connections.store.Begin(context.Background()); err == nil {
		t.Fatal("lease allowed during account change")
	}
}
func fakeAccountServer(t *testing.T, s *ShelfService, h http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	b := &s.connections.accounts
	b.cmd = &exec.Cmd{}
	b.done = make(chan struct{})
	b.url = server.URL
	b.token = "fixture"
	t.Cleanup(func() { b.mu.Lock(); b.cmd = nil; b.mu.Unlock() })
}
func TestAccountSwitchBlocksNewVerification(t *testing.T) {
	s, id := transitionFixture(t)
	entered, respond := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fakeAccountServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-respond
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	done := make(chan error, 1)
	go func() { done <- s.SetModelAccount("codex", "fixture", true, false) }()
	defer func() {
		once.Do(func() { close(respond) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("switch not joined")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no account request")
	}
	blockVerification(t, s, id, "verify-during-switch")
	once.Do(func() { close(respond) })
}
func TestLoginBlocksUntilTerminalStatusObserved(t *testing.T) {
	s, id := transitionFixture(t)
	var mu sync.Mutex
	state := "waiting"
	fakeAccountServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(ModelLoginState{ID: "fixture-login", Agent: "codex", State: state})
	})
	login, err := s.StartModelAccountLogin("codex")
	if err != nil {
		t.Fatal(err)
	}
	blockVerification(t, s, id, "verify-while-waiting")
	mu.Lock()
	state = "done"
	mu.Unlock() // Helper already changed credentials, UI has not polled yet.
	blockVerification(t, s, id, "verify-before-poll")
	if _, err = s.ModelAccountLoginStatus(login.ID); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	result := s.VerifyModelConnection(id, catalog.State.Revision, "verify-after-login")
	if result.Error != "" || result.Receipt == nil || result.Receipt.Verified == nil || !*result.Receipt.Verified {
		t.Fatal("verification remained blocked", result.Error)
	}
	if s.connections.accounts.cmd != nil {
		t.Fatal("terminal helper not quiesced")
	}
}
