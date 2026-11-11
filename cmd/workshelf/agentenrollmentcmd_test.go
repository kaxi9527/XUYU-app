package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
	"github.com/kaxi9527/WorkShelf/internal/agents"
)

func TestAgentEnrollmentCLIRequiresYesAndHonorsPolicy(t *testing.T) {
	if !agents.AutomaticEnrollmentAvailable() {
		t.Skip("secure enrollment unsupported")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "work-shelf-checks")
	call := func(sub string, args ...string) []byte {
		t.Helper()
		var out, errs bytes.Buffer
		full := append([]string{"agents", sub, "--home", home, "--state-dir", state, "--json"}, args...)
		if code := run(full, &out, &errs); code != 0 {
			t.Fatalf("%s exit=%d %s", sub, code, errs.String())
		}
		return out.Bytes()
	}
	call("enroll", "--agent", "codex")
	call("policy", "--enable", "--agents", "codex")
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("dry run created policy or history")
	}
	path := filepath.Join(home, ".codex", "AGENTS.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("dry run wrote client")
	}
	var r agentdiscovery.Receipt
	if err := json.Unmarshal(call("policy", "--enable", "--agents", "codex", "--yes"), &r); err != nil {
		t.Fatal(err)
	}
	if r.Policy == nil || !r.Policy.Enabled || len(r.Enrollments) != 1 || r.Enrollments[0].Status != "rule_written_pending_load" {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, agents.Segment("codex")) {
		t.Fatal("approved policy did not write rule", err)
	}
	call("daily")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, b) {
		t.Fatal("daily duplicated enrollment")
	}
	if err := json.Unmarshal(call("policy", "--disable", "--yes"), &r); err != nil || r.Policy.Enabled {
		t.Fatal("disable failed", err)
	}
	if err := os.WriteFile(path, []byte("user removed rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	call("daily")
	after, _ = os.ReadFile(path)
	if string(after) != "user removed rules" {
		t.Fatal("disabled policy wrote rules")
	}
	call("enroll", "--agent", "codex", "--yes")
	after, _ = os.ReadFile(path)
	if !bytes.HasPrefix(after, []byte("user removed rules")) || bytes.Count(after, []byte(agents.Begin)) != 1 {
		t.Fatal("explicit enrollment failed")
	}
}
