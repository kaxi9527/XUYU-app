package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/agents"
)

func TestAgentEnrollmentDesktopBridge(t *testing.T) {
	if !agents.AutomaticEnrollmentAvailable() {
		t.Skip("secure enrollment unsupported")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(home, "checks")
	store, err := agentdiscovery.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	svc := &ShelfService{discoveries: agentDiscoveryConnection{store: store, scanner: agentdiscovery.Scanner{Home: home}}}
	state, err := svc.AgentEnrollmentStatus()
	if err != nil || state.Receipt.Revision != 0 || state.BackgroundRunning {
		t.Fatalf("%+v %v", state, err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatal("status created state")
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := svc.CheckAgentDiscovery(false)
	if err != nil || !result.Ran {
		t.Fatal(err)
	}
	preview, err := svc.PreviewAgentEnrollment("codex")
	if err != nil || preview.Preview.Status != "create" {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "AGENTS.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preview changed client")
	}
	r, err := svc.SetAgentEnrollmentPolicy(preview.Revision, true, []string{"codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Enrollments) != 1 || r.Enrollments[0].Status != "rule_written_pending_load" {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, agents.Segment("codex")) {
		t.Fatal("bridge did not deliver rule", err)
	}
	if _, err := svc.ApplyAgentEnrollment(preview.Revision, "codex", preview.Preview.Digest); err == nil {
		t.Fatal("stale preview accepted")
	}
	state, err = svc.AgentEnrollmentStatus()
	if err != nil || state.BackgroundRunning || state.Receipt.Policy == nil || !state.Receipt.Policy.Enabled {
		t.Fatal(err)
	}
	if _, err := svc.SetAgentEnrollmentPolicy(state.Receipt.Revision, false, []string{"codex"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckAgentDiscovery(true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "user rules" {
		t.Fatal("disabled bridge wrote client")
	}
}
