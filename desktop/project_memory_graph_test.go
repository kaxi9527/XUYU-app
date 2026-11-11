package main

import (
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestMemoryGraphConnectsTypedFileEventsAndUndoReference(t *testing.T) {
	before := projectmemory.Fact{Path: "/project/draft.txt"}
	after := projectmemory.Fact{Path: "/project/文档/draft.txt"}
	page := projectmemory.EventPage{Project: projectmemory.Project{ID: "project", Name: "我的项目"}, Revision: 9, Next: "next-page", Items: []projectmemory.Event{
		{ID: "move", Kind: "placement_recorded", Source: "local_execution", Actor: "workshelf", Placement: &projectmemory.PlacementFact{TargetFileID: "file", Evidence: projectmemory.PlacementEvidence{ExpectedFile: projectmemory.File{ID: "file"}, Before: before, After: after}}},
		{ID: "undo", Kind: "placement_undone", Source: "local_execution", Actor: "workshelf", Undo: &projectmemory.UndoFact{Evidence: projectmemory.UndoEvidence{Original: projectmemory.UndoReference{FileID: "file", EventID: "move"}, Before: after, After: before}}},
	}}
	out := memoryGraphPage("root:我的项目", page)
	if out.Revision != 9 || out.Next != "next-page" {
		t.Fatal("lost pagination")
	}
	ids := map[string]bool{}
	for _, node := range out.Nodes {
		ids[node.ID] = true
		if node.Path != "" || node.Agent != "" {
			t.Fatal("invented live path or agent", node)
		}
		if node.Kind == "memory_event" && (!strings.Contains(node.Extra, "序屿执行") || !strings.Contains(node.Extra, before.Path) || !strings.Contains(node.Extra, after.Path)) {
			t.Fatal("missing facts", node)
		}
	}
	fileEdges, references := 0, 0
	for _, edge := range out.Edges {
		if !ids[edge.From] || !ids[edge.To] || edge.Directed {
			t.Fatal("broken or causal edge", edge)
		}
		if edge.Type == "memory_event" && strings.HasPrefix(edge.From, "memory-file:") {
			fileEdges++
		}
		if edge.Type == "memory_reference" {
			references++
		}
	}
	if fileEdges != 2 || references != 1 {
		t.Fatal(fileEdges, references)
	}
	page.Items = page.Items[1:]
	for _, edge := range memoryGraphPage("root:我的项目", page).Edges {
		if edge.Type == "memory_reference" {
			t.Fatal("invented off-page node")
		}
	}
}

func TestMemoryGraphCopyKeepsBothIdentitiesAndRecoveryActorUnknown(t *testing.T) {
	before := projectmemory.Fact{Path: "/project/a.txt"}
	after := projectmemory.Fact{Path: "/project/文档/a.txt"}
	event := projectmemory.Event{ID: "copy", Kind: "placement_recorded", Placement: &projectmemory.PlacementFact{TargetFileID: "copy", Evidence: projectmemory.PlacementEvidence{ExpectedFile: projectmemory.File{ID: "source"}, Before: before, After: after}}}
	files, _, _ := memoryEventFiles(event)
	if len(files) != 2 || files[0].ID == files[1].ID {
		t.Fatal("merged source/copy identities", files)
	}
	event = projectmemory.Event{ID: "review", Kind: "placement_reconciled", Source: "local_user", Reconciliation: &projectmemory.ReconciliationFact{TargetFileID: "source", Evidence: projectmemory.ReconciliationEvidence{ExpectedFile: projectmemory.File{ID: "source"}, Before: before, Target: &after}}}
	files, _, _ = memoryEventFiles(event)
	if len(files) != 1 || files[0].Fact.Path != after.Path {
		t.Fatal("missing recovered location")
	}
	out := memoryGraphPage("project", projectmemory.EventPage{Project: projectmemory.Project{ID: "project"}, Items: []projectmemory.Event{event}})
	for _, node := range out.Nodes {
		if node.Kind == "memory_event" && !strings.Contains(node.Extra, "原操作来源未确认") {
			t.Fatal("invented performer", node)
		}
	}
}
