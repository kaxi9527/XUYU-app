package agentdiscovery

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, Scanner, time.Time) {
	t.Helper()
	if !writesSupported() {
		t.Skip("unsupported file locks")
	}
	home := testHome(t)
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), "existing rules")
	s, err := NewStore(filepath.Join(testHome(t), "discovery"))
	if err != nil {
		t.Fatal(err)
	}
	return s, Scanner{Home: home}, time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
}

func TestDailyRestartCatchUpAndHistory(t *testing.T) {
	s, scan, now := newTestStore(t)
	if _, err := s.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.directory); !os.IsNotExist(err) {
		t.Fatal("read created storage")
	}
	first, err := s.Check(context.Background(), scan, now, false)
	if err != nil || !first.Ran || first.Receipt.Revision != 1 || len(first.Receipt.Changes) != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	b, err := os.ReadFile(filepath.Join(s.directory, receiptName(1)))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []time.Duration{time.Minute, 23 * time.Hour} {
		r, err := restarted.Check(context.Background(), scan, now.Add(offset), false)
		if err != nil || r.Ran || r.Receipt.Revision != 1 {
			t.Fatalf("repeated daily scan: %+v %v", r, err)
		}
	}
	r, err := restarted.Check(context.Background(), scan, now.Add(7*24*time.Hour), false)
	if err != nil || !r.Ran || r.Receipt.Revision != 2 || len(r.Receipt.Changes) != 0 || r.Receipt.PreviousSHA != hash(b) {
		t.Fatalf("catch-up %+v %v", r, err)
	}
	after, _ := os.ReadFile(filepath.Join(s.directory, receiptName(1)))
	if string(after) != string(b) {
		t.Fatal("history modified")
	}
	if !r.Receipt.Records[0].FirstSeen.Equal(now) {
		t.Fatal("first-seen identity reset")
	}
	if _, err := restarted.Check(context.Background(), scan, now, true); !errors.Is(err, ErrClock) {
		t.Fatal(err)
	}
}

func TestStateChangesUnknownAndReappearance(t *testing.T) {
	s, scan, now := newTestStore(t)
	if _, err := s.Check(context.Background(), scan, now, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(scan.Home, ".codex", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(scan.Home, ".codex")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Check(context.Background(), scan, now.Add(time.Hour), true)
	if err != nil || candidate(t, r.Receipt.Records, "codex").Status != "not_observed" {
		t.Fatalf("%+v %v", r, err)
	}
	write(t, filepath.Join(scan.Home, ".claude", "CLAUDE.md"), "new agent")
	outside := testHome(t)
	if err := os.Symlink(outside, filepath.Join(scan.Home, ".gemini")); err != nil {
		t.Fatal(err)
	}
	r, err = s.Check(context.Background(), scan, now.Add(2*time.Hour), true)
	if err != nil || candidate(t, r.Receipt.Records, "codex").Status != "unknown" {
		t.Fatalf("gap falsely confirms absence: %+v %v", r, err)
	}
	write(t, filepath.Join(scan.Home, ".codex", "AGENTS.md"), "restored")
	r, err = s.Check(context.Background(), scan, now.Add(3*time.Hour), true)
	if err != nil || candidate(t, r.Receipt.Records, "codex").Status != "needs_rule" || !candidate(t, r.Receipt.Records, "codex").FirstSeen.Equal(now) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestConcurrentDailyChecksAndCanceledLock(t *testing.T) {
	s, scan, now := newTestStore(t)
	var wg sync.WaitGroup
	var runs atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(s.directory)
			if err != nil {
				t.Error(err)
				return
			}
			r, err := other.Check(context.Background(), scan, now, false)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Ran {
				runs.Add(1)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("ran %d times", runs.Load())
	}
	f, err := os.OpenFile(filepath.Join(s.directory, "check.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := lockContext(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	defer unlock(f)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := s.Check(ctx, scan, now.Add(24*time.Hour), false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCorruptionSymlinkAndScopeNeverOverwrite(t *testing.T) {
	s, scan, now := newTestStore(t)
	if _, err := s.Check(context.Background(), scan, now, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), Scanner{Home: testHome(t)}, now.Add(time.Hour), true); err == nil {
		t.Fatal("shared store crossed homes")
	}
	p := filepath.Join(s.directory, receiptName(1))
	write(t, p, "{broken")
	if _, err := s.Check(context.Background(), scan, now.Add(24*time.Hour), false); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{broken" {
		t.Fatal("corrupt record overwritten")
	}
	link := filepath.Join(testHome(t), "link")
	if err := os.Symlink(s.directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(link); err == nil {
		t.Fatal("store symlink accepted")
	}
}

func TestWatchStopsWithoutLeakingOrWritingAfterCancel(t *testing.T) {
	s, scan, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Watch(ctx, scan, func(r Result) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r, err := s.Read(context.Background())
	if err != nil || r.Revision != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDailyProcessHelper(t *testing.T) {
	if os.Getenv("WORKSHELF_TEST_DISCOVERY_HELPER") != "1" {
		return
	}
	s, err := NewStore(os.Getenv("WORKSHELF_TEST_DISCOVERY_STORE"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Check(context.Background(), Scanner{Home: os.Getenv("WORKSHELF_TEST_DISCOVERY_HOME")}, time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC), false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ran {
		os.Exit(21)
	}
	os.Exit(0)
}

func TestDailyAcrossProcesses(t *testing.T) {
	s, scan, _ := newTestStore(t)
	var commands []*exec.Cmd
	for i := 0; i < 2; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDailyProcessHelper$")
		cmd.Env = append(os.Environ(), "WORKSHELF_TEST_DISCOVERY_HELPER=1", "WORKSHELF_TEST_DISCOVERY_STORE="+s.directory, "WORKSHELF_TEST_DISCOVERY_HOME="+scan.Home)
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
	}
	runs := 0
	for _, cmd := range commands {
		err := cmd.Wait()
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 21 {
			runs++
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Fatalf("cross-process scans: %d", runs)
	}
	r, err := s.Read(context.Background())
	if err != nil || r.Revision != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestPreviousReceiptTamperingAndReplacementLinkAreRejected(t *testing.T) {
	s, scan, now := newTestStore(t)
	if _, err := s.Check(context.Background(), scan, now, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), scan, now.Add(24*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.directory, receiptName(1))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON changed after the subsequent receipt committed: chain must fail.
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(context.Background(), scan, now.Add(48*time.Hour), false); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	latest := filepath.Join(s.directory, receiptName(2))
	saved := filepath.Join(testHome(t), "receipt.json")
	if err := os.Rename(latest, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, latest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
