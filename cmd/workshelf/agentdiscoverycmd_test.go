package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/agentdiscovery"
)

func TestAgentDiscoveryCLIDailyAndManual(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file-lock writes unsupported")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "checks")
	rules := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules, []byte("keep existing rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, sub := range []string{"daily", "daily", "inventory"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"agents", sub, "--home", home, "--state-dir", state, "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%s: %d %s", sub, code, stderr.String())
		}
		var got agentdiscovery.Result
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Ran != (i != 1) || got.Receipt.Revision != []uint64{1, 1, 2}[i] {
			t.Fatalf("unexpected result %+v", got)
		}
		if got.Receipt.Scan.Candidates[0].Status != "needs_rule" {
			t.Fatal("claimed rules delivered")
		}
	}
	b, err := os.ReadFile(rules)
	if err != nil || string(b) != "keep existing rules" {
		t.Fatal("changed agent rules")
	}
	var out, errs bytes.Buffer
	if code := run([]string{"agents", "daily", "--home", home, "--state-dir", state, "unexpected"}, &out, &errs); code == 0 {
		t.Fatal("accepted unexpected argument")
	}
}
