//go:build go1.24 && unix

package projectmemory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeScanFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRealScanLifecycleAndLostAcknowledgement(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	path := filepath.Join(scope.Roots[0], "draft.txt")
	writeScanFile(t, path, "version one")
	r, err := s.Observe(context.Background(), 1, "scan-1", scope.Digest())
	if err != nil || !strings.Contains(eventKinds(r), "first_observed") {
		t.Fatal(r, err)
	}
	v := readMemory(t, s)
	var original File
	for _, f := range v.State.Files {
		if f.Fact.Path == path {
			original = f
		}
	}
	if original.Fact.SHA256 != digest([]byte("version one")) || !v.State.ObservationComplete {
		t.Fatal(v)
	}
	writeScanFile(t, path, "version two changed")
	retry, err := s.Observe(context.Background(), 1, "scan-1", scope.Digest())
	if err != nil || retry.Record.Revision != r.Record.Revision || readMemory(t, s).State.Revision != 2 {
		t.Fatal(retry, err)
	}
	r, err = s.Observe(context.Background(), 2, "scan-2", scope.Digest())
	if err != nil || !strings.Contains(eventKinds(r), "content_changed") {
		t.Fatal(r, err)
	}
	destination := filepath.Join(scope.Roots[0], "final.txt")
	if err = os.Rename(path, destination); err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, path, "different object at reused path")
	r, err = s.Observe(context.Background(), 3, "scan-3", scope.Digest())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		if !strings.Contains(eventKinds(r), "location_changed") {
			t.Fatal(r)
		}
		for _, f := range readMemory(t, s).State.Files {
			if f.Fact.Path == destination && f.ID != original.ID {
				t.Fatalf("lost identity: %+v", f)
			}
		}
	}
	if err = os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	r, err = s.Observe(context.Background(), 4, "scan-4", scope.Digest())
	if err != nil || !strings.Contains(eventKinds(r), "no_longer_seen") {
		t.Fatal(r, err)
	}
	for _, e := range r.Record.Events {
		if e.Actor != "unknown" {
			t.Fatal(e)
		}
	}
	if _, err = s.SetTracking(context.Background(), 5, "pause", scope, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Observe(context.Background(), 6, "paused-scan", scope.Digest()); err == nil {
		t.Fatal("scanned while paused")
	}
	if readMemory(t, s).State.Revision != 6 {
		t.Fatal("failed scan mutated history")
	}
}

func TestScanExclusionsLinksExactFilesAndBudgets(t *testing.T) {
	s, scope := memoryFixture(t)
	root := scope.Roots[0]
	writeScanFile(t, filepath.Join(root, "ordinary.txt"), "ordinary content")
	writeScanFile(t, filepath.Join(root, ".env"), "private-value-not-a-real-secret")
	writeScanFile(t, filepath.Join(root, ".codex", "history.json"), "private history")
	writeScanFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "dependency")
	writeScanFile(t, filepath.Join(root, "journal", "events", "1.json"), "own output")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeScanFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	o, err := Scan(context.Background(), scope, ScanOptions{Exclude: []string{filepath.Join(root, "journal")}})
	if err != nil || o.Complete {
		t.Fatal(o, err)
	}
	for _, f := range o.Files {
		if strings.Contains(f.Path, "history.json") || strings.Contains(f.Path, "index.js") || strings.Contains(f.Path, "journal") {
			t.Fatalf("read excluded entry: %+v", f)
		}
		if filepath.Base(f.Path) == ".env" && (f.HashState != "metadata_only" || f.SHA256 != "") {
			t.Fatal("hashed secret", f)
		}
		if filepath.Base(f.Path) == "alias.txt" && (f.Kind != "link" || f.SHA256 != "") {
			t.Fatal("followed link", f)
		}
	}
	// A single registered file grants no access to its siblings.
	exact := Scope{Files: []string{filepath.Join(root, "ordinary.txt")}}
	o, err = Scan(context.Background(), exact, ScanOptions{MaxHashBytes: 1})
	if err != nil || !o.Complete || len(o.Files) != 1 || o.Files[0].HashState != "metadata_only" {
		t.Fatal(o, err)
	}
	o, err = Scan(context.Background(), scope, ScanOptions{MaxFiles: 2})
	if err != nil || o.Complete || len(o.Files) > 2 {
		t.Fatal(o, err)
	}
	initialize(t, s, scope)
	observe(t, s, observation(scope, 1, fact(scope, "absent.txt", "old", "old")), "prior")
	o.StartedAt, o.EndedAt = stamp(time.Now()), stamp(time.Now())
	r := observe(t, s, o, "partial")
	if strings.Contains(eventKinds(r), "no_longer_seen") {
		t.Fatal("budget invented absence", r)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Scan(cancelled, scope, ScanOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestScanOfflineAndAncestorLinkDoNotReadTargets(t *testing.T) {
	_, scope := memoryFixture(t)
	root := scope.Roots[0]
	outside := t.TempDir()
	writeScanFile(t, filepath.Join(outside, "private.txt"), "private")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	o, err := Scan(context.Background(), Scope{Files: []string{filepath.Join(root, "linked", "private.txt")}}, ScanOptions{})
	if err != nil || o.Complete || len(o.Files) != 0 {
		t.Fatal(o, err)
	}
	if err = os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	o, err = Scan(context.Background(), scope, ScanOptions{})
	if err != nil || o.Complete || len(o.Files) != 0 || o.Coverage[0].Status != "offline" {
		t.Fatal(o, err)
	}
}
