package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)

func memoryFixture(t *testing.T) (*Store, Scope) {
	t.Helper()
	dir := t.TempDir()
	project := Project{ID: "project_alpha", Name: "品牌项目", Path: filepath.Join(dir, "project")}
	if err := os.Mkdir(project.Path, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(filepath.Join(dir, "memory"), project)
	if err != nil {
		t.Fatal(err)
	}
	return s, Scope{Roots: []string{project.Path}}
}
func initialize(t *testing.T, s *Store, scope Scope) {
	t.Helper()
	r, err := s.Initialize(context.Background(), "enable", scope, testNow)
	if err != nil || r.ProjectionPending || r.Record.Revision != 1 {
		t.Fatal(r, err)
	}
}
func fact(scope Scope, name, key, content string) Fact {
	return Fact{Path: filepath.Join(scope.Roots[0], name), Kind: "file", Size: int64(len(content)), ModifiedAt: stamp(testNow), ObjectKey: key, SHA256: digest([]byte(content)), HashState: "verified"}
}
func observation(scope Scope, minute int, files ...Fact) Observation {
	at := stamp(testNow.Add(time.Duration(minute) * time.Minute))
	return Observation{ScopeSHA: scope.Digest(), StartedAt: at, EndedAt: at, Complete: true, Coverage: []Coverage{{Path: scope.Roots[0], Status: "complete"}}, Files: files}
}
func readMemory(t *testing.T, s *Store) View {
	t.Helper()
	v, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func observe(t *testing.T, s *Store, o Observation, id string) Result {
	t.Helper()
	v := readMemory(t, s)
	r, err := s.RecordObservation(context.Background(), v.State.Revision, id, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func eventKinds(r Result) string {
	a := []string{}
	for _, e := range r.Record.Events {
		a = append(a, e.Kind)
	}
	return strings.Join(a, ",")
}

func TestReadDoesNotCreateAndProjectsDoNotMix(t *testing.T) {
	s, scope := memoryFixture(t)
	v := readMemory(t, s)
	if v.State.Revision != 0 {
		t.Fatal(v)
	}
	if _, err := os.Stat(s.directory); !os.IsNotExist(err) {
		t.Fatal("read allocated storage", err)
	}
	initialize(t, s, scope)
	f := fact(scope, "草稿.txt", "obj-a", "one")
	observe(t, s, observation(scope, 1, f), "scan-one")
	other, err := NewStore(filepath.Dir(s.directory), Project{ID: "project_beta", Name: "同名也独立", Path: s.project.Path})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMemory(t, other); got.State.Revision != 0 || len(got.State.Files) != 0 {
		t.Fatal(got)
	}
	wrong, err := NewStore(filepath.Dir(s.directory), Project{ID: s.project.ID, Name: s.project.Name, Path: filepath.Dir(s.project.Path)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Read(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatal("same id rebound silently", err)
	}
}
func TestObservationChangesAndTombstones(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	f := fact(scope, "草稿.txt", "object-one", "first")
	r := observe(t, s, observation(scope, 1, f), "baseline")
	if eventKinds(r) != "first_observed,observation_completed" || r.Record.Events[0].Actor != "unknown" {
		t.Fatal(r)
	}
	id := readMemory(t, s).State.Files[0].ID
	f.SHA256 = digest([]byte("next!"))
	r = observe(t, s, observation(scope, 2, f), "change")
	if eventKinds(r) != "content_changed,observation_completed" {
		t.Fatal(r)
	}
	f.Path = filepath.Join(scope.Roots[0], "子目录/改名.txt")
	r = observe(t, s, observation(scope, 3, f), "move")
	if eventKinds(r) != "location_changed,observation_completed" || readMemory(t, s).State.Files[0].ID != id {
		t.Fatal(r)
	}
	r = observe(t, s, observation(scope, 4), "missing")
	if !strings.HasPrefix(eventKinds(r), "no_longer_seen,") || readMemory(t, s).State.Files[0].Status != "missing" {
		t.Fatal(r)
	}
	r = observe(t, s, observation(scope, 5, f), "return")
	if !strings.HasPrefix(eventKinds(r), "seen_again,") || readMemory(t, s).State.Files[0].ID != id {
		t.Fatal(r)
	}
	for _, e := range r.Record.Events {
		if e.Actor != "unknown" {
			t.Fatal("fabricated actor", e)
		}
	}
	history, err := s.History(context.Background(), 0, 100)
	if err != nil || len(history) != 6 {
		t.Fatal(len(history), err)
	}
}
func TestIncompleteScopeAndIdentityAmbiguityDoNotInventDeletesOrMoves(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	a := fact(scope, "a.txt", "shared-object", "same")
	b := a
	b.Path = filepath.Join(scope.Roots[0], "b.txt")
	observe(t, s, observation(scope, 1, a, b), "baseline")
	c := a
	c.Path = filepath.Join(scope.Roots[0], "c.txt")
	o := observation(scope, 2, c)
	o.Complete = false
	o.Coverage[0].Status = "limited"
	o.Coverage[0].Reason = "处理预算用完"
	r := observe(t, s, o, "partial")
	if eventKinds(r) != "first_observed,observation_completed" {
		t.Fatal(r)
	}
	v := readMemory(t, s)
	if len(v.State.Files) != 3 || v.State.ObservationComplete {
		t.Fatal(v)
	}
	for _, f := range v.State.Files {
		if f.Status != "present" {
			t.Fatal("partial scan manufactured absence", f)
		}
	}
	md, err := os.ReadFile(filepath.Join(s.directory, "memory.md"))
	if err != nil || !strings.Contains(string(md), "本轮覆盖不完整") {
		t.Fatal(err)
	}
	r = observe(t, s, observation(scope, 3, c), "complete")
	if strings.Contains(eventKinds(r), "location_changed") {
		t.Fatal("hardlink ambiguity inferred move", r)
	}
	bad := observation(scope, 4)
	bad.Coverage[0].Status = "offline"
	if _, err = s.RecordObservation(context.Background(), r.Record.Revision, "bad", bad); err == nil {
		t.Fatal("incomplete coverage claimed complete")
	}
}
func TestScopePauseRetainsMemoryAndModelSwitchDoesNotFork(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	observe(t, s, observation(scope, 1, fact(scope, "a.txt", "a", "old")), "baseline")
	for i, model := range []string{"model-a", "model-b"} {
		v := readMemory(t, s)
		if _, err := s.AddNote(context.Background(), v.State.Revision, fmt.Sprintf("note-%d", i), Note{Kind: "agent_proposal", Text: "项目的目录仍使用同一份记录", Agent: "codex", Model: model}, testNow.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	v := readMemory(t, s)
	if len(v.State.Notes) != 2 || len(v.State.Files) != 1 || v.State.Project.ID != s.project.ID {
		t.Fatal(v)
	}
	smaller := Scope{Files: []string{filepath.Join(scope.Roots[0], "other.txt")}}
	r, err := s.SetTracking(context.Background(), v.State.Revision, "scope", smaller, false, testNow.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	v = readMemory(t, s)
	if v.State.Enabled || v.State.Files[0].Status != "untracked" {
		t.Fatal(v)
	}
	if _, err = s.RecordObservation(context.Background(), r.Record.Revision, "paused", observation(scope, 180)); err == nil {
		t.Fatal("paused accepted scan")
	}
	if _, err = s.AddNote(context.Background(), r.Record.Revision, "fake-decision", Note{Kind: "user_decision", Text: "伪称用户决定", Model: "model-b"}, testNow); err == nil {
		t.Fatal("model claim became decision")
	}
}
func TestCommittedRecordSurvivesProjectionFailureAndRetry(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	s.afterCommit = func() error { return errors.New("injected interruption before projection") }
	o := observation(scope, 1, fact(scope, "a.txt", "one", "content"))
	r, err := s.RecordObservation(context.Background(), 1, "one-op", o)
	if err != nil || !r.ProjectionPending || r.Record.Revision != 2 {
		t.Fatal(r, err)
	}
	v := readMemory(t, s)
	if v.State.Revision != 2 || len(v.State.Files) != 1 || !v.ProjectionPending {
		t.Fatal(v)
	}
	before, err := os.ReadFile(filepath.Join(s.directory, "events", recordName(2)))
	if err != nil {
		t.Fatal(err)
	}
	s.afterCommit = nil
	r, err = s.RecordObservation(context.Background(), 1, "one-op", o)
	if err != nil || r.ProjectionPending || r.Record.Revision != 2 || readMemory(t, s).ProjectionPending {
		t.Fatal(r, err)
	}
	after, _ := os.ReadFile(filepath.Join(s.directory, "events", recordName(2)))
	if string(before) != string(after) {
		t.Fatal("retry rewrote history")
	}
	o.Complete = false
	if _, err = s.RecordObservation(context.Background(), 2, "one-op", o); !errors.Is(err, ErrOperationReused) {
		t.Fatal(err)
	}
	if records, err := s.History(context.Background(), 0, 100); err != nil || len(records) != 2 {
		t.Fatal(len(records), err)
	}
}
func TestCorruptionLinksAndCancellationPreserveOriginal(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	path := filepath.Join(s.directory, "index.json")
	if err := os.WriteFile(path, []byte("broken derived view"), 0600); err != nil {
		t.Fatal(err)
	}
	if !readMemory(t, s).ProjectionPending {
		t.Fatal("damaged derived view treated as current")
	}
	if _, err := s.RepairProjection(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(s.directory, "events", recordName(1))
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(record, []byte("damaged authoritative event"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(context.Background()); err == nil {
		t.Fatal("damaged log treated as empty")
	}
	if _, err = s.SetTracking(context.Background(), 1, "after-corruption", scope, false, testNow); err == nil {
		t.Fatal("wrote through corrupt log")
	}
	b, _ := os.ReadFile(record)
	if string(b) != "damaged authoritative event" {
		t.Fatal("corrupt evidence overwritten")
	}
	if err = os.WriteFile(record, before, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.SetTracking(ctx, 1, "cancelled", scope, false, testNow); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err = os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(s.directory, "memory.lock")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(s.directory, "memory.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTracking(context.Background(), 1, "linked-lock", scope, false, testNow); err == nil {
		t.Fatal("followed linked lock")
	}
	b, _ = os.ReadFile(outside)
	if string(b) != "preserve" {
		t.Fatal("outside modified")
	}
}
func TestTwoWritersAndIdempotentLostAcknowledgement(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	other, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, store := range []*Store{s, other} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			_, err := store.AddNote(context.Background(), 1, fmt.Sprintf("writer-%d", i), Note{Kind: "agent_proposal", Agent: "tool", Model: fmt.Sprint(i), Text: "keep both writers distinct"}, testNow)
			results <- err
		}(i, store)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || readMemory(t, s).State.Revision != 2 {
		t.Fatal(success, conflict)
	}
	data, err := os.ReadFile(filepath.Join(s.directory, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cached cache
	if err = json.Unmarshal(data, &cached); err != nil {
		t.Fatal(err)
	}
	if digestJSON(cached.State) == "" || readMemory(t, s).ProjectionPending {
		t.Fatal("projection did not match winning commit")
	}
}
