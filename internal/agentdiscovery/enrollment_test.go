package agentdiscovery

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agents"
)

func enrollmentFixture(t *testing.T) (*Store, Scanner, time.Time, Receipt) {
	t.Helper()
	if !agents.AutomaticEnrollmentAvailable() {
		t.Skip("safe enrollment unavailable")
	}
	s, scanner, now := newTestStore(t)
	write(t, filepath.Join(scanner.Home, ".claude", "CLAUDE.md"), "claude originals")
	r, err := s.Check(context.Background(), scanner, now, false)
	if err != nil {
		t.Fatal(err)
	}
	return s, scanner, now, r.Receipt
}
func event(t *testing.T, r Receipt, agent string) EnrollmentEvent {
	t.Helper()
	e, ok := previousEnrollment(r, agent)
	if !ok {
		t.Fatal("missing event for", agent)
	}
	return e
}

func TestEnrollmentPolicyDailyFlowIgnoreAndDisable(t *testing.T) {
	s, scanner, now, r := enrollmentFixture(t)
	ctx := context.Background()
	before, _ := os.ReadFile(filepath.Join(scanner.Home, ".codex", "AGENTS.md"))
	got, err := s.ReconcileEnrollment(ctx)
	if err != nil || got.Revision != r.Revision {
		t.Fatal("default policy performed writes", err)
	}
	r, err = s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"codex", "claude"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReconcileEnrollment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := event(t, r, "codex")
	if e.Status != "rule_written_pending_load" || e.Intent.Backup == "" {
		t.Fatalf("%+v", e)
	}
	saved, _ := os.ReadFile(e.Intent.Backup)
	if !bytes.Equal(saved, before) {
		t.Fatal("original backup incorrect")
	}
	ignored, _ := os.ReadFile(filepath.Join(scanner.Home, ".claude", "CLAUDE.md"))
	if string(ignored) != "claude originals" {
		t.Fatal("ignored client changed")
	}
	stable, err := s.ReconcileEnrollment(ctx)
	if err != nil || stable.Revision != r.Revision {
		t.Fatal("unchanged enrollment repeated", err)
	}
	next, err := s.Check(ctx, scanner, now.Add(24*time.Hour), false)
	if err != nil || next.Receipt.Policy == nil || next.Receipt.Policy.Version != 1 {
		t.Fatal("daily scan dropped policy", err)
	}
	r = next.Receipt
	write(t, e.Intent.Path, "user removed the WorkShelf block")
	r, err = s.ReconcileEnrollment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event(t, r, "codex").Status != "rule_changed_needs_review" {
		t.Fatal("rule removal ignored")
	}
	b, _ := os.ReadFile(e.Intent.Path)
	if string(b) != "user removed the WorkShelf block" {
		t.Fatal("rule reinstalled against removal intent")
	}
	review, err := s.EnrollmentPreview(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Enroll(ctx, review.Revision, "codex", review.Preview.Digest)
	if err != nil || event(t, r, "codex").Status != "rule_written_pending_load" {
		t.Fatal(err)
	}
	r, err = s.SetEnrollmentPolicy(ctx, r.Revision, false, []string{"codex", "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReconcileEnrollment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(scanner.Home, ".claude", "CLAUDE.md"))
	if string(b) != "claude originals" {
		t.Fatal("disabled policy wrote")
	}
}

func TestConcurrentEnrollmentInstancesWriteOneRule(t *testing.T) {
	s, scanner, _, r := enrollmentFixture(t)
	ctx := context.Background()
	if _, err := s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"codex"}, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(s.directory)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := other.ReconcileEnrollment(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(scanner.Home, ".codex", "AGENTS.md"))
	if err != nil || bytes.Count(b, []byte(agents.Begin)) != 1 {
		t.Fatal("duplicate or missing block", err)
	}
	r, err = s.Read(ctx)
	if err != nil || r.Revision != 4 {
		t.Fatalf("expected scan, policy, intent, result only: revision=%d err=%v", r.Revision, err)
	}
}

func TestUnsupportedBuildPreservesExistingEnrollmentPolicy(t *testing.T) {
	if agents.AutomaticEnrollmentAvailable() {
		t.Skip("test exercises unavailable native adapter")
	}
	s, scanner, now := newTestStore(t)
	ctx := context.Background()
	result, err := s.Check(ctx, scanner, now, false)
	if err != nil {
		t.Fatal(err)
	}
	r := result.Receipt
	release, err := s.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.Policy = &EnrollmentPolicy{Version: 1, Enabled: true, Grants: []EnrollmentGrant{{Agent: "codex", RuleSHA: agents.EnrollmentRuleSHA("codex")}}}
	r, err = s.recordEnrollment(ctx, r, "fixture_native_policy", nil)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileEnrollment(ctx); err == nil {
		t.Fatal("unsupported build claimed enrollment")
	}
	after, err := s.Read(ctx)
	if err != nil || after.Revision != r.Revision || len(after.Enrollments) != 0 {
		t.Fatal("unsupported build changed enrollment facts", err)
	}
}

func TestEnrollmentRevisionAndRuleGrantCannotBeBypassed(t *testing.T) {
	s, scanner, _, r := enrollmentFixture(t)
	ctx := context.Background()
	review, err := s.EnrollmentPreview(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, review.Revision, "codex", review.Preview.Digest); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err := s.Enroll(ctx, r.Revision, "cursor", "anything"); err == nil {
		t.Fatal("unsupported tool accepted")
	}
	if _, err := s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"cursor"}, nil); err == nil {
		t.Fatal("unverified tool grant accepted")
	}
	release, err := s.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.Policy.Grants[0].RuleSHA = hash([]byte("previous rule text"))
	r, err = s.recordEnrollment(ctx, r, "fixture_old_policy", nil)
	release()
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReconcileEnrollment(ctx)
	if err != nil || event(t, r, "codex").Reason != "policy_rule_changed" {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(scanner.Home, ".codex", "AGENTS.md"))
	if string(b) != "existing rules" {
		t.Fatal("changed rule content silently authorized")
	}
}

func TestEnrollmentCrashHelper(t *testing.T) {
	mode := os.Getenv("WORKSHELF_TEST_ENROLL_CRASH")
	if mode == "" {
		return
	}
	s, err := NewStore(os.Getenv("WORKSHELF_TEST_ENROLL_STORE"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	release, err := s.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r, _, err := s.latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := agents.PrepareEnrollment(ctx, r.Scan.Home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(s.directory, "rule-backups")
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = agents.ApplyEnrollment(ctx, p, backup, func(i agents.EnrollmentIntent) error {
		e := EnrollmentEvent{Intent: i, Status: "writing"}
		r, err = s.recordEnrollment(ctx, r, "enrollment_intent", &e)
		if err != nil {
			return err
		}
		if mode == "before" {
			os.Exit(27)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(28)
}

func TestEnrollmentCrashRecoveryDoesNotNeedAnotherScan(t *testing.T) {
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			s, scanner, now, r := enrollmentFixture(t)
			ctx := context.Background()
			r, err := s.SetEnrollmentPolicy(ctx, r.Revision, true, []string{"codex"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestEnrollmentCrashHelper$")
			cmd.Env = append(os.Environ(), "WORKSHELF_TEST_ENROLL_CRASH="+mode, "WORKSHELF_TEST_ENROLL_STORE="+s.directory)
			err = cmd.Run()
			exit, ok := err.(*exec.ExitError)
			want := 27
			if mode == "after" {
				want = 28
			}
			if !ok || exit.ExitCode() != want {
				t.Fatalf("helper %v", err)
			}
			r, err = s.Read(ctx)
			if err != nil || event(t, r, "codex").Status != "writing" {
				t.Fatal(err)
			}
			// Disabling before recovery must prevent a new write, even when the prior
			// process died before touching the target.
			r, err = s.SetEnrollmentPolicy(ctx, r.Revision, false, []string{"codex"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			daily, err := s.Check(ctx, scanner, now.Add(time.Hour), false)
			if err != nil || daily.Ran {
				t.Fatal("unexpected rescan", err)
			}
			r, err = s.RecoverEnrollment(ctx)
			if err != nil {
				t.Fatal(err)
			}
			status := "not_written"
			if mode == "after" {
				status = "rule_present_pending_load"
			}
			if event(t, r, "codex").Status != status {
				t.Fatalf("%+v", event(t, r, "codex"))
			}
			b, _ := os.ReadFile(filepath.Join(scanner.Home, ".codex", "AGENTS.md"))
			wantCount := 0
			if mode == "after" {
				wantCount = 1
			}
			if bytes.Count(b, []byte(agents.Begin)) != wantCount {
				t.Fatal("recovery wrote or duplicated rules")
			}
		})
	}
}
