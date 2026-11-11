package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelAccountRuntimePrivateLifecycle(t *testing.T) {
	binary := os.Getenv("WORKSHELF_TEST_MODEL_BINARY")
	if binary == "" {
		t.Skip("set WORKSHELF_TEST_MODEL_BINARY to the task-owned model runtime")
	}
	root := t.TempDir()
	s, _, _ := modelTestService(t, modelTestVerifier(modelTestReady))
	s.connections.accounts.binary = binary
	s.connections.accounts.directory = filepath.Join(root, "accounts")
	t.Cleanup(s.connections.accounts.close)
	state, err := s.ModelAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Accounts) != 0 || len(state.Capabilities) != 5 {
		t.Fatalf("unexpected empty account state: %d/%d", len(state.Accounts), len(state.Capabilities))
	}
	data, _ := json.Marshal(state)
	if strings.Contains(string(data), s.connections.accounts.token) || strings.Contains(string(data), s.connections.accounts.url) {
		t.Fatal("private runtime details exposed")
	}
	client := &http.Client{}
	resp, err := client.Get(s.connections.accounts.url + "/control/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("unauthenticated request accepted")
	}
	req, _ := http.NewRequest("GET", s.connections.accounts.url+"/control/state", nil)
	req.Header.Set("Authorization", "Bearer "+s.connections.accounts.token)
	req.Header.Set("Origin", "http://localhost")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("browser origin accepted")
	}
	if _, err = s.StartModelAccountLogin("unknown"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err = s.ModelAccountLoginStatus("../state"); err == nil {
		t.Fatal("invalid flow accepted")
	}
	if _, err = s.ModelAccountLoginStatus("unknown-flow"); err == nil {
		t.Fatal("foreign flow accepted")
	}
	first := s.connections.accounts.cmd.Process.Pid
	if _, err = s.ModelAccounts(); err != nil || s.connections.accounts.cmd.Process.Pid != first {
		t.Fatal("runtime not reused", err)
	}
	previousDone := s.connections.accounts.done
	s.connections.accounts.stop(false)
	select {
	case <-previousDone:
	default:
		t.Fatal("quiesced helper not joined")
	}
	if _, err = s.ModelAccounts(); err != nil || s.connections.accounts.cmd.Process.Pid == first {
		t.Fatal("quiesced runtime was not restarted independently", err)
	}
	done := s.connections.accounts.done
	s.connections.accounts.close()
	select {
	case <-done:
	default:
		t.Fatal("helper not joined")
	}
	if _, err = s.ModelAccounts(); err == nil {
		t.Fatal("closed runtime restarted")
	}
	fi, err := os.Stat(s.connections.accounts.directory)
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Fatal("account directory permissions", err)
	}
}

func TestModelLoginProjection(t *testing.T) {
	var accounts ModelAccountState
	if err := json.Unmarshal([]byte(`{"accounts":[{"agent":"codex","user":"fixture","lapsed":"expired"}]}`), &accounts); err != nil || accounts.Accounts[0].Lapsed != "expired" {
		t.Fatal("expired account decode", err)
	}
	state := safeModelLogin(ModelLoginState{URL: "file:///private/secret", Error: "token=secret"})
	if state.URL != "" || strings.Contains(state.Error, "secret") {
		t.Fatal("unsafe login projection")
	}
	for _, id := range []string{"", "../foo", "flow?token=x", "flow#fragment", "flow\\secret"} {
		if validModelFlow(id) {
			t.Fatal("invalid flow accepted", id)
		}
	}
}

func TestModelRuntimeRejectsLinkedSettings(t *testing.T) {
	binary := os.Getenv("WORKSHELF_TEST_MODEL_BINARY")
	if binary == "" {
		t.Skip("isolated runtime test only")
	}
	root := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(foreign, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(root, "settings.json")); err != nil {
		t.Fatal(err)
	}
	s, _, _ := modelTestService(t, modelTestVerifier(modelTestReady))
	s.connections.accounts.binary = binary
	s.connections.accounts.directory = root
	t.Cleanup(s.connections.accounts.close)
	if _, err := s.ModelAccounts(); err == nil {
		t.Fatal("linked settings accepted")
	}
	b, _ := os.ReadFile(foreign)
	if string(b) != "private fixture" {
		t.Fatal("foreign settings changed")
	}
}
