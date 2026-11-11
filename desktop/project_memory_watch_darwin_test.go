//go:build darwin && cgo

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func waitRealtimeMemory(t *testing.T, s *ShelfService, key string, match func(ProjectMemoryStatus, []projectmemory.File) bool) ProjectMemoryStatus {
	t.Helper()
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.ProjectMemory(key)
		if err != nil {
			t.Fatal(err)
		}
		files, err := s.ProjectMemoryFiles(key, st.Revision, 0)
		if err == nil && match(st, files.Items) {
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	st, _ := s.ProjectMemory(key)
	t.Fatalf("file changes not recorded in time: revision=%d watching=%v error=%q", st.Revision, st.RealtimeWatching, st.BackgroundError)
	return ProjectMemoryStatus{}
}

func TestProjectMemoryRealtimeNativeLifecycle(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	enableAutomaticMemory(t, s, key)
	s.startProjectMemoryObservation()
	defer s.closeProjectMemoryObservation()
	waitRealtimeMemory(t, s, key, func(st ProjectMemoryStatus, _ []projectmemory.File) bool {
		return st.RealtimeWatching && st.Revision >= 3
	})
	file := filepath.Join(project, "实时成果.txt")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	var fileID string
	st := waitRealtimeMemory(t, s, key, func(_ ProjectMemoryStatus, files []projectmemory.File) bool {
		for _, f := range files {
			if filepath.Base(f.Fact.Path) == "实时成果.txt" && f.Status == "present" {
				fileID = f.ID
				return true
			}
		}
		return false
	})
	first := st.Revision
	if err := os.WriteFile(file, []byte("second longer content"), 0600); err != nil {
		t.Fatal(err)
	}
	st = waitRealtimeMemory(t, s, key, func(_ ProjectMemoryStatus, files []projectmemory.File) bool {
		for _, f := range files {
			if f.ID == fileID && f.Fact.Size == 21 {
				return true
			}
		}
		return false
	})
	if st.Revision <= first {
		t.Fatal("content change reused old snapshot")
	}
	renamed := filepath.Join(project, "改名成果.txt")
	if err := os.Rename(file, renamed); err != nil {
		t.Fatal(err)
	}
	waitRealtimeMemory(t, s, key, func(_ ProjectMemoryStatus, files []projectmemory.File) bool {
		for _, f := range files {
			if f.ID == fileID && filepath.Base(f.Fact.Path) == "改名成果.txt" && f.Status == "present" {
				return true
			}
		}
		return false
	})
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	st = waitRealtimeMemory(t, s, key, func(_ ProjectMemoryStatus, files []projectmemory.File) bool {
		for _, f := range files {
			if f.ID == fileID && f.Status == "missing" {
				return true
			}
		}
		return false
	})
	events, err := s.ProjectMemoryEvents(key, st.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.ProjectMemoryGraph(key, st.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	nodes := map[string]bool{}
	for _, n := range graph.Nodes {
		nodes[n.ID] = true
	}
	for _, e := range events.Items {
		if e.After != nil && e.After.ID == fileID || e.Before != nil && e.Before.ID == fileID {
			kinds[e.Kind] = true
			if !nodes["memory-event:"+st.ProjectID+":"+e.ID] {
				t.Fatal("graph omitted recorded file event", e.Kind)
			}
		}
	}
	for _, kind := range []string{"first_observed", "content_changed", "location_changed", "no_longer_seen"} {
		if !kinds[kind] {
			t.Fatal("missing lifecycle event", kind)
		}
	}
	if _, err = s.SetProjectMemoryAutomatic(key, false, st.Revision, "stop-native-watch", st.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	st, _ = s.ProjectMemory(key)
	if st.RealtimeWatching {
		t.Fatal("watch remained after revoke")
	}
	revision := st.Revision
	if err = os.WriteFile(file, []byte("after revoke"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	st, _ = s.ProjectMemory(key)
	if st.Revision != revision {
		t.Fatal("new observation after revoke")
	}
	s.closeProjectMemoryObservation()
	if len(s.memories.watches) != 0 {
		t.Fatal("native watchers leaked on shutdown")
	}
	t.Log("native create/edit/rename/remove -> same file identity, ledger and graph; revoke and shutdown passed")
}

func TestNativeMemoryWatchFiltersLooseFilesAndControl(t *testing.T) {
	root := t.TempDir()
	control := filepath.Join(root, "control")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "selected.txt")
	w, err := newMemoryNativeWatch(projectmemory.Scope{Files: []string{file}}, control)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = os.WriteFile(file, []byte("selected"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if w.Take() {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatal("selected loose file did not signal")
	}
	time.Sleep(500 * time.Millisecond)
	w.Take()
	if err = os.WriteFile(filepath.Join(root, "unselected.txt"), []byte("outside scope"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if w.Take() {
		t.Fatal("unselected sibling triggered file watcher")
	}
	w2, err := newMemoryNativeWatch(projectmemory.Scope{Roots: []string{root}}, control)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if err = os.WriteFile(filepath.Join(control, "projection.md"), []byte("own ledger"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if w2.Take() {
		t.Fatal("own ledger caused a scan loop")
	}
	selectedLog := filepath.Join(control, "activity.jsonl")
	w3, err := newMemoryNativeWatch(projectmemory.Scope{Roots: []string{root}, Files: []string{selectedLog}}, control)
	if err != nil {
		t.Fatal(err)
	}
	defer w3.Close()
	if err = os.WriteFile(selectedLog, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if !w3.Take() {
		t.Fatal("explicitly granted source inside control folder was excluded")
	}
}

func TestProjectMemoryRealtimeSourceAppend(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	file := versionFixtureFile(t, project, "source-output.md", "output")
	enableAutomaticMemory(t, s, key)
	log := filepath.Join(t.TempDir(), "sources.jsonl")
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := s.ProjectMemory(key)
	result := s.ApplyProjectMemoryAction(MemoryAction{Kind: "source_on", ProjectKey: key, Revision: st.Revision, OperationID: "watch-source", ScopeSHA: st.CurrentScopeSHA, SourceAdapter: "workshelf", SourcePath: log})
	if result.Error != "" {
		t.Fatal(result)
	}
	s.startProjectMemoryObservation()
	defer s.closeProjectMemoryObservation()
	waitRealtimeMemory(t, s, key, func(st ProjectMemoryStatus, _ []projectmemory.File) bool {
		return st.RealtimeWatching && len(st.Sources) == 1 && st.Sources[0].LastReadAt != ""
	})
	line := fmt.Sprintf("{\"agent\":\"codex\",\"path\":%q,\"session\":\"native-source\"}\n", file)
	if err := os.WriteFile(log, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	waitRealtimeMemory(t, s, key, func(st ProjectMemoryStatus, _ []projectmemory.File) bool { return st.Origins == 1 })
}

func TestProjectMemoryRealtimeNewSessionDirectory(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	file := versionFixtureFile(t, project, "source-output.md", "output")
	enableAutomaticMemory(t, s, key)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "initial.jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := s.ProjectMemory(key)
	result := s.ApplyProjectMemoryAction(MemoryAction{Kind: "source_on", ProjectKey: key, Revision: st.Revision, OperationID: "watch-directory", ScopeSHA: st.CurrentScopeSHA, SourceAdapter: "workshelf", SourcePath: dir, SourceDirectory: true})
	if result.Error != "" {
		t.Fatal(result)
	}
	s.startProjectMemoryObservation()
	defer s.closeProjectMemoryObservation()
	waitRealtimeMemory(t, s, key, func(st ProjectMemoryStatus, _ []projectmemory.File) bool {
		return st.RealtimeWatching && len(st.Sources) == 1 && st.Sources[0].ReadRecords == 1
	})
	sub := filepath.Join(dir, "new")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("{\"agent\":\"codex\",\"path\":%q,\"session\":\"new-session\"}\n", file)
	if err := os.WriteFile(filepath.Join(sub, "session.jsonl"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	waitRealtimeMemory(t, s, key, func(st ProjectMemoryStatus, _ []projectmemory.File) bool {
		return st.Origins == 1 && st.Sources[0].ReadRecords == 2
	})
}
