package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryActionsRecoveryAndGraphDoNotMutateFiles(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	key := "root:品牌项目"
	file := versionFixtureFile(t, project, "设计稿.txt", "design")
	state, err := s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	start := MemoryAction{Kind: "enable", ProjectKey: key, OperationID: "start", ScopeSHA: state.CurrentScopeSHA}
	result := s.ApplyProjectMemoryAction(start)
	if result.Error != "" || result.Receipt == nil {
		t.Fatal(result)
	}
	scan := MemoryAction{Kind: "scan", ProjectKey: key, Revision: 1, OperationID: "scan", ScopeSHA: state.CurrentScopeSHA}
	result = s.ApplyProjectMemoryAction(scan)
	if result.Error != "" || result.Receipt == nil {
		t.Fatal(result)
	}
	status, err := s.ProjectMemoryOperation(key, "scan")
	if err != nil || !status.Found || status.Record.Revision != 2 {
		t.Fatal(status, err)
	}
	if again := s.ApplyProjectMemoryAction(scan); again.Receipt == nil || again.Receipt.Record.Revision != 2 {
		t.Fatal(again)
	}
	state, err = s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(cfg.Root, cfg.Names.Control, "项目记忆", state.ProjectID, "index.json")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.ProjectMemoryGraph(key, 2, "")
	if err != nil || len(graph.Nodes) < 3 || len(graph.Nodes) > 61 {
		t.Fatal(graph, err)
	}
	ids := map[string]bool{}
	for _, n := range graph.Nodes {
		ids[n.ID] = true
		if n.Agent != "" || n.Path != "" {
			t.Fatal("graph invented actor or exposed unchecked open path", n)
		}
	}
	for _, e := range graph.Edges {
		if !ids[e.From] || !ids[e.To] || e.Directed {
			t.Fatal("invalid or causal edge", e)
		}
	}
	after, err := os.ReadFile(index)
	if err != nil || string(before) != string(after) {
		t.Fatal("graph changed memory", err)
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != "design" {
		t.Fatal("file changed", err)
	}
	scan.Kind = "pause"
	if wrong := s.ApplyProjectMemoryAction(scan); wrong.Receipt != nil || wrong.Error == "" {
		t.Fatal("operation ID reused for another intent", wrong)
	}
}
