package agentdiscovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agents"
)

func testHome(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
func candidate(t *testing.T, cs []Candidate, id string) Candidate {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("candidate %q missing", id)
	return Candidate{}
}

func TestDiscoveryRequiresActualRuleAndDoesNotModifyClients(t *testing.T) {
	home := testHome(t)
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), "existing\n"+agents.Block("codex"))
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), agents.Begin)
	write(t, filepath.Join(home, ".new-tool", "AGENTS.md"), "custom rules")
	write(t, filepath.Join(home, "AGENTS.md"), "generic rules")
	write(t, filepath.Join(home, ".gemini", "credential.json"), "DO NOT READ")
	apps := filepath.Join(home, "Applications")
	if err := os.MkdirAll(filepath.Join(apps, "Codex.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := Scanner{Home: home, ApplicationRoots: []string{apps}}
	r, err := s.Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Candidates) != 5 || len(r.Gaps) != 0 {
		t.Fatalf("candidates=%d gaps=%v", len(r.Candidates), r.Gaps)
	}
	if c := candidate(t, r.Candidates, "codex"); c.Status != "rule_present" || len(c.Evidence) != 3 {
		t.Fatalf("%+v", c)
	}
	if candidate(t, r.Candidates, "claude").Status != "review_needed" {
		t.Fatal("partial rule claimed installed")
	}
	if candidate(t, r.Candidates, "gemini").Status != "needs_rule" {
		t.Fatal("directory-only client missed")
	}
	if c := candidate(t, r.Candidates, "shared_rules"); c.Kind != "rule_candidate" {
		t.Fatal("generic rule assigned an agent")
	}
	for _, c := range r.Candidates {
		if c.Name == ".new-tool" && (c.Status != "review_needed" || c.Kind != "rule_candidate") {
			t.Fatal("unknown client falsely identified")
		}
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if string(b) != agents.Begin {
		t.Fatal("scanner changed client rules")
	}
}

func TestDiscoveryGapsCancellationAndRuleReplacement(t *testing.T) {
	home := testHome(t)
	outside := testHome(t)
	write(t, filepath.Join(outside, "AGENTS.md"), agents.Block("codex"))
	if err := os.Symlink(outside, filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".unrecognized-link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), strings.Repeat("x", maxRuleBytes+1))
	s := Scanner{Home: home}
	r, err := s.Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Gaps) < 2 || candidate(t, r.Candidates, "codex").Status != "review_needed" {
		t.Fatal("inaccessible paths reported as covered")
	}
	unknownLinkGap := false
	for _, g := range r.Gaps {
		unknownLinkGap = unknownLinkGap || g.Code == "top_level_link_not_followed"
	}
	if !unknownLinkGap {
		t.Fatal("unknown linked scope silently skipped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Run(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := (Scanner{Home: home, ApplicationRoots: []string{"relative"}}).Run(context.Background(), time.Now()); err == nil {
		t.Fatal("relative root accepted")
	}
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), agents.Block("claude"))
	r, err = s.Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if candidate(t, r.Candidates, "claude").Status != "rule_present" {
		t.Fatal("valid rule not detected")
	}
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user removed rule")
	r, err = s.Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if candidate(t, r.Candidates, "claude").Status != "needs_rule" {
		t.Fatal("deleted integration retained")
	}
}

func TestDiscoveryEntryBudgetIsVisible(t *testing.T) {
	home := testHome(t)
	write(t, filepath.Join(home, "a"), "a")
	write(t, filepath.Join(home, "b"), "b")
	r, err := (Scanner{Home: home, MaxHomeEntries: 1}).Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range r.Gaps {
		found = found || g.Code == "home_entry_budget"
	}
	if !found {
		t.Fatal("truncated scan claimed complete")
	}
}
