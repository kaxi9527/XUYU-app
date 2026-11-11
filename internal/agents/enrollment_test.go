package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enrollmentHome(t *testing.T) string {
	t.Helper()
	if !enrollmentSupported() {
		t.Skip("secure enrollment unsupported in this build")
	}
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func prepare(t *testing.T, home, agent string) *EnrollmentPlan {
	t.Helper()
	p, err := PrepareEnrollment(context.Background(), home, agent)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnrollmentPlansRealEntrypointsAndPreservesPreview(t *testing.T) {
	home := enrollmentHome(t)
	w(t, filepath.Join(home, ".codex", "AGENTS.md"), "normal-private-text")
	w(t, filepath.Join(home, ".codex", "AGENTS.override.md"), "override-private-text")
	p := prepare(t, home, "codex")
	if p.Preview().Path != filepath.Join(home, ".codex", "AGENTS.override.md") || p.Preview().Status != "append" {
		t.Fatalf("%+v", p.Preview())
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("override-private-text")) || strings.Contains(fmt.Sprintf("%+v", p), "override-private-text") {
		t.Fatal("original client text exposed")
	}
	before, _ := os.ReadFile(p.Preview().Path)
	if string(before) != "override-private-text" {
		t.Fatal("preview wrote client")
	}
	w(t, filepath.Join(home, ".codex", "AGENTS.override.md"), " ")
	if prepare(t, home, "codex").Preview().Path != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatal("empty override changed selection")
	}
	if prepare(t, home, "cursor").Preview().Reason != "adapter_requires_manual_setup" {
		t.Fatal("unverified global cursor path accepted")
	}
	if prepare(t, home, "claude").Preview().Reason != "profile_missing_or_inaccessible" {
		t.Fatal("uninitialized client profile created")
	}
	w(t, filepath.Join(home, ".gemini", "settings.json"), `{"context":{"fileName":["AGENTS.md"]},"credential":"not-exported"}`)
	if prepare(t, home, "gemini").Preview().Reason != "custom_context_filename" {
		t.Fatal("custom memory filename ignored")
	}
	w(t, filepath.Join(home, ".gemini", "settings.json"), `{"context":{"fileName":["GEMINI.md","AGENTS.md"]}}`)
	if prepare(t, home, "gemini").Preview().Status != "create" {
		t.Fatal("supported GEMINI name not accepted")
	}
}

func TestEnrollmentAppendAndCreateRequireDurableIntent(t *testing.T) {
	for _, agent := range AutomaticAgents() {
		t.Run(agent, func(t *testing.T) {
			home := enrollmentHome(t)
			backups := enrollmentHome(t)
			if err := os.Mkdir(filepath.Join(home, "."+agent), 0o700); err != nil {
				t.Fatal(err)
			}
			p := prepare(t, home, agent)
			intentCount := 0
			out, err := ApplyEnrollment(context.Background(), p, backups, func(i EnrollmentIntent) error {
				intentCount++
				b, e := os.ReadFile(i.Backup)
				if e != nil || len(b) != 0 {
					t.Fatal("backup not durable before intent")
				}
				if _, e := os.Stat(i.Path); !os.IsNotExist(e) {
					t.Fatal("target created before intent")
				}
				return nil
			})
			if err != nil || out.Status != "rule_written_pending_load" || intentCount != 1 {
				t.Fatalf("%+v %v", out, err)
			}
			b, _ := os.ReadFile(out.Path)
			if !bytes.Equal(b, Segment(agent)) {
				t.Fatal("wrong rule")
			}
			p = prepare(t, home, agent)
			out, err = ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error { t.Fatal("duplicate write"); return nil })
			if err != nil || out.Status != "already_present" {
				t.Fatal(err)
			}
			w(t, p.Preview().Path, "existing user content\r\n")
			p = prepare(t, home, agent)
			out, err = ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(out.Path)
			saved, _ := os.ReadFile(out.Backup)
			if !bytes.Equal(saved, []byte("existing user content\r\n")) || !bytes.Equal(b, append(saved, Segment(agent)...)) {
				t.Fatal("original content changed")
			}
		})
	}
}

func TestEnrollmentRejectsStalePlanFailedIntentAndConcurrentEdits(t *testing.T) {
	home := enrollmentHome(t)
	backups := enrollmentHome(t)
	path := w(t, filepath.Join(home, ".codex", "AGENTS.md"), "before")
	p := prepare(t, home, "codex")
	w(t, path, "after")
	if _, err := ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error { return nil }); !errors.Is(err, ErrEnrollmentChanged) {
		t.Fatal(err)
	}
	p = prepare(t, home, "codex")
	wantErr := errors.New("journal failure")
	if _, err := ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "after" {
		t.Fatal("failed intent still wrote")
	}
	p = prepare(t, home, "codex")
	_, err := ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error { w(t, path, "external work"); return nil })
	if !errors.Is(err, ErrEnrollmentChanged) {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "external work" {
		t.Fatal("external work overwritten")
	}
	p = prepare(t, home, "codex")
	_, err = ApplyEnrollment(context.Background(), p, backups, func(EnrollmentIntent) error {
		w(t, filepath.Join(home, ".codex", "AGENTS.override.md"), "new override")
		return nil
	})
	if !errors.Is(err, ErrEnrollmentChanged) {
		t.Fatal("changed entrypoint accepted")
	}
}

func TestEnrollmentLinksMalformedRulesAndBudgets(t *testing.T) {
	home := enrollmentHome(t)
	outside := enrollmentHome(t)
	w(t, filepath.Join(outside, "AGENTS.md"), "outside")
	if err := os.Symlink(outside, filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	if prepare(t, home, "codex").Preview().Status != "blocked" {
		t.Fatal("linked parent accepted")
	}
	w(t, filepath.Join(home, ".claude", "CLAUDE.md"), Begin)
	if prepare(t, home, "claude").Preview().Reason != "existing_rule_needs_review" {
		t.Fatal("partial block accepted")
	}
	if err := os.Remove(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(outside, "AGENTS.md"), filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if prepare(t, home, "claude").Preview().Status != "blocked" {
		t.Fatal("hardlinked rule accepted")
	}
	home = enrollmentHome(t)
	w(t, filepath.Join(home, ".codex", "AGENTS.md"), strings.Repeat("x", 32<<10))
	if prepare(t, home, "codex").Preview().Reason != "instruction_budget" {
		t.Fatal("known truncation ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareEnrollment(ctx, home, "codex"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
