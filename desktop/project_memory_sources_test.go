package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func finishSourcesAcceptance(t *testing.T, s *ShelfService, first, second, report string) {
	t.Helper()
	status, err := s.ProjectMemory("root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sources) != 1 || status.Sources[0].Enabled || status.Origins != 2 {
		t.Fatal("source grant/revoke results differ", status.Sources, status.Origins)
	}
	e, err := s.memoryEnvironment(context.Background(), "root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.State.Origins[0].Binding != "path_only" || v.State.Origins[1].Binding != "snapshot_match" {
		t.Fatal(v.State.Origins)
	}
	graph, err := s.ProjectMemoryGraph("root:品牌项目", status.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, n := range graph.Nodes {
		seen[n.Name] = true
	}
	if !seen["claude"] || !seen["browser-source-session"] || !seen["browser-source-task"] {
		t.Fatal("missing source graph references")
	}
	for path, want := range map[string]string{first: "first content", second: "second content"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatal("source flow changed artifact", path, err)
		}
	}
	bytes, _ := json.MarshalIndent(map[string]any{"origins": 2, "grantRevoked": true, "artifactBytesUnchanged": true, "graphToolSessionTask": true, "projectID": status.ProjectID, "revision": status.Revision}, "", "  ")
	if err = os.WriteFile(report, bytes, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectMemorySourceActionRecovery(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	key := "root:品牌项目"
	st, _ := s.ProjectMemory(key)
	if _, err := s.SetProjectMemoryTracking(key, true, st.Revision, "track-source-test", st.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	st, _ = s.ProjectMemory(key)
	path := filepath.Join(project, ".workshelf-origin.jsonl")
	action := MemoryAction{Kind: "source_on", ProjectKey: key, Revision: st.Revision, OperationID: "grant-source-test", ScopeSHA: st.CurrentScopeSHA, SourceAdapter: "workshelf", SourcePath: path}
	result := s.ApplyProjectMemoryAction(action)
	if result.Error != "" || result.Receipt == nil {
		t.Fatal(result)
	}
	if retry := s.ApplyProjectMemoryAction(action); retry.Error != "" || retry.Receipt == nil || retry.Receipt.Record.Revision != result.Receipt.Record.Revision {
		t.Fatal("grant replay changed state", retry)
	}
	st, _ = s.ProjectMemory(key)
	if len(st.Sources) != 1 || !st.Sources[0].Enabled {
		t.Fatal(st.Sources)
	}
	action.Kind = "source_off"
	action.Revision = st.Revision
	action.OperationID = "revoke-source-test"
	if result = s.ApplyProjectMemoryAction(action); result.Error != "" {
		t.Fatal(result)
	}
	st, _ = s.ProjectMemory(key)
	if st.Sources[0].Enabled {
		t.Fatal("revoke not durable")
	}
}
