package projectcontext

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func placementEvidence(p Packet) PlacementEvidence {
	data, _ := json.Marshal(adviceFor(p))
	return PlacementEvidence{Kind: "single", RequestID: "analysis-one", Packet: p, Advice: data, Recipient: models.Recipient{Epoch: 1, ProviderID: "provider", ProviderVersion: 1, ProviderName: "Fixture", Protocol: models.Chat, BaseURL: "https://fixture.invalid/v1", ModelRecordID: "model", ModelID: "fixture", ModelName: "Fixture"}}
}

func TestPlacementEntireBatch2267PreservesCoverageAndOriginalEvidence(t *testing.T) {
	v, profile := fixture()
	v.State.Files = nil
	for i := 0; i < 2267; i++ {
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%04d", i), Status: "present", LastEvent: "event-1", Fact: projectmemory.Fact{Path: fmt.Sprintf("/projects/one/docs/file-%04d.txt", i), Kind: "file", Size: 1, ModifiedAt: v.State.LastObservedAt, HashState: "metadata_only"}})
	}
	scope, err := NewScopeIndex(v, profile)
	if err != nil {
		t.Fatal(err)
	}
	root, err := scope.Page("", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := scope.PlanBatch(root.SHA, root.ID)
	if err != nil || len(plan.Packets) != 57 {
		t.Fatal("batch", err)
	}
	evidence := []PlacementEvidence{}
	for part, p := range plan.Packets {
		e := placementEvidence(p)
		e.Kind, e.RequestID, e.Part = "batch", "original-batch", part
		ids := []string{}
		for _, f := range p.Body.Files {
			ids = append(ids, f.ID)
		}
		e.Advice, _ = json.Marshal(Advice{ProjectID: p.Body.ProjectID, ContextSHA: p.SHA256, MemoryRevision: p.Body.MemoryRevision, Groups: []Group{{FolderID: p.Body.Folders[0].ID, FileIDs: ids, Reason: "使用已确认模板"}}, UncertainIDs: []string{}})
		evidence = append(evidence, e)
	}
	// Successful analysis appends its own memory note; it does not erase or
	// weaken the user's previous decisions or turn the model into an authority.
	v.State.Revision++
	v.HeadSHA = strings.Repeat("e", 64)
	v.State.Notes = append(v.State.Notes, projectmemory.Note{ID: "batch-result", Kind: "agent_proposal", Text: "批次完成", EventID: "event-8"})
	got, err := ResolvePlacements(v, profile, evidence)
	if err != nil || len(got.Suggestions) != 2267 || len(got.References) != 57 || got.UntouchedFiles != 0 || len(got.UncertainIDs) != 0 {
		t.Fatalf("whole batch: suggestions=%d refs=%d err=%v", len(got.Suggestions), len(got.References), err)
	}
	seen := map[string]bool{}
	for _, s := range got.Suggestions {
		if seen[s.File.ID] || s.SuggestedTarget != filepath.Join(v.State.Project.Path, "参考", filepath.Base(s.File.Fact.Path)) || got.References[s.Reference].RequestID != "original-batch" {
			t.Fatal("identity, destination or evidence mismatch")
		}
		seen[s.File.ID] = true
	}
	partial, err := ResolvePlacements(v, profile, evidence[1:])
	if err != nil || len(partial.Suggestions) != 2227 || partial.UntouchedFiles != 40 {
		t.Fatal("unanswered part was guessed", err)
	}
	v.State.Files[100].Fact.ModifiedAt = "2026-11-09T01:00:00Z"
	if _, err = ResolvePlacements(v, profile, evidence); err == nil {
		t.Fatal("changed batch member accepted")
	}
}

func TestPlacementKeepsProjectLocationAndEvidence(t *testing.T) {
	p, v, profile := packet(t)
	e := placementEvidence(p)
	v.State.Revision++
	v.HeadSHA = strings.Repeat("c", 64)
	v.State.Notes = append(v.State.Notes, projectmemory.Note{ID: "analysis-note", Kind: "agent_proposal", Text: "本次结果已写回", Agent: "workshelf", Model: "new-model", EventID: "event-4"})
	got, err := ResolvePlacements(v, profile, []PlacementEvidence{e})
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != v.State.Project || len(got.Suggestions) != 1 || got.Suggestions[0].File.ID != "file-one" || got.Suggestions[0].SuggestedTarget != filepath.Join(v.State.Project.Path, "参考", "notes.txt") || len(got.UncertainIDs) != 1 || got.UncertainIDs[0] != "file-two" {
		t.Fatalf("identity/destination drift: %+v", got)
	}
	if got.References[0].RequestID != e.RequestID || got.References[0].ContextSHA != p.SHA256 || got.References[0].AdviceSHA != hash(e.Advice) || got.MemoryHead != v.HeadSHA {
		t.Fatal("lost original evidence")
	}
	profile.Folders[0] = "changed"
	v.State.Scope.Roots[0] = "/elsewhere"
	v.State.Files[0].Fact.Path = "/elsewhere/private"
	if got.Profile.Folders[0] != "参考" || got.Scope.Roots[0] != "/projects/one" || got.Suggestions[0].File.Fact.Path != "/projects/one/notes.txt" {
		t.Fatal("proposal aliases mutable state")
	}
}

func TestPlacementRejectsStaleRulesFactsAndForeignScope(t *testing.T) {
	cases := []struct {
		name   string
		change func(*projectmemory.View, *worktype.Profile, *PlacementEvidence)
	}{
		{"project", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) { v.State.Project.ID = "other" }},
		{"project-root", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Project.Path = "/projects/other"
		}},
		{"paused", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) { v.State.Enabled = false }},
		{"scope", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Scope.Files = []string{"/projects/one/extra"}
		}},
		{"template", func(_ *projectmemory.View, p *worktype.Profile, _ *PlacementEvidence) {
			p.Folders[0] = "另一目录"
			p.Rules[0].Folder = "另一目录"
		}},
		{"user-decision", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Notes[0].Text = "保留原处，不要移动"
		}},
		{"new-decision", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Notes = append(v.State.Notes, projectmemory.Note{ID: "new", Kind: "user_decision", Text: "新增规则", EventID: "event-5"})
		}},
		{"changed-content", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Files[0].Fact.SHA256 = strings.Repeat("d", 64)
		}},
		{"changed-event", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Files[0].LastEvent = "event-new"
		}},
		{"changed-position", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Files[0].Fact.Path = "/projects/one/renamed.txt"
		}},
		{"missing", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) {
			v.State.Files[0].Status = "missing"
		}},
		{"untrusted-path", func(_ *projectmemory.View, _ *worktype.Profile, e *PlacementEvidence) {
			a := adviceFor(e.Packet)
			a.Groups[0].FolderID = "/private"
			e.Advice, _ = json.Marshal(a)
		}},
		{"future-revision", func(v *projectmemory.View, _ *worktype.Profile, _ *PlacementEvidence) { v.State.Revision-- }},
		{"part", func(_ *projectmemory.View, _ *worktype.Profile, e *PlacementEvidence) { e.Part = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, v, profile := packet(t)
			e := placementEvidence(p)
			tc.change(&v, &profile, &e)
			if _, err := ResolvePlacements(v, profile, []PlacementEvidence{e}); err == nil {
				t.Fatal("stale or invalid placement accepted")
			}
		})
	}
}

func TestPlacementPartialBatchNewFilesAndRepeatedEvidence(t *testing.T) {
	_, v, profile := packet(t)
	p, err := Build(v, profile, []string{"file-one"}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	a := Advice{ProjectID: p.Body.ProjectID, ContextSHA: p.SHA256, MemoryRevision: p.Body.MemoryRevision, Groups: []Group{{FolderID: "folder-1", FileIDs: []string{"file-one"}, Reason: "符合文件规则"}}, UncertainIDs: []string{}}
	e := placementEvidence(p)
	e.Kind = "batch"
	e.Part = 3
	e.Advice, _ = json.Marshal(a)
	got, err := ResolvePlacements(v, profile, []PlacementEvidence{e})
	if err != nil || got.UntouchedFiles != 1 || len(got.Suggestions) != 1 || got.References[0].Part != 3 {
		t.Fatalf("partial batch %v %+v", err, got)
	}
	if _, err = ResolvePlacements(v, profile, []PlacementEvidence{e, e}); err == nil {
		t.Fatal("duplicate receipt")
	}
	other := e
	other.RequestID = "different"
	if _, err = ResolvePlacements(v, profile, []PlacementEvidence{e, other}); err == nil {
		t.Fatal("same file in two receipts")
	}
	f := v.State.Files[0]
	f.ID = "new-file"
	f.Fact.Path = "/projects/one/new.txt"
	v.State.Files = append(v.State.Files, f)
	got, err = ResolvePlacements(v, profile, []PlacementEvidence{e})
	if err != nil || got.UntouchedFiles != 2 {
		t.Fatal("new files silently adopted", err)
	}
}
