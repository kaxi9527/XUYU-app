package projectcontext

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestBatchPlanEntireScopeAndPartialSummary(t *testing.T) {
	v, profile := fixture()
	v.State.Files = v.State.Files[2:]
	for i := 0; i < 2267; i++ {
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%04d", i), Status: "present", LastEvent: "event-1", Fact: projectmemory.Fact{Path: fmt.Sprintf("/projects/one/docs/file-%04d.txt", i), Kind: "file", Size: 1, HashState: "metadata_only", ModifiedAt: v.State.LastObservedAt}})
	}
	s, err := NewScopeIndex(v, profile)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.Page("", "", 0)
	plan, err := s.PlanBatch(root.SHA, root.ID)
	if err != nil || plan.TotalFiles != 2267 || len(plan.Packets) != 57 {
		t.Fatal("whole project plan", plan.TotalFiles, len(plan.Packets), err)
	}
	again, err := s.PlanBatch(root.SHA, root.ID)
	if err != nil || again.SHA != plan.SHA {
		t.Fatal("non-deterministic plan")
	}
	seen := map[string]bool{}
	responses := map[int]json.RawMessage{}
	for i, p := range plan.Packets {
		ids := []string{}
		for _, f := range p.Body.Files {
			if seen[f.ID] {
				t.Fatal("overlap")
			}
			seen[f.ID] = true
			ids = append(ids, f.ID)
		}
		if len(p.Body.Notes) != len(plan.Packets[0].Body.Notes) {
			t.Fatal("notes trimmed")
		}
		advice := Advice{ProjectID: p.Body.ProjectID, ContextSHA: p.SHA256, MemoryRevision: p.Body.MemoryRevision, Groups: []Group{}, UncertainIDs: ids}
		data, _ := json.Marshal(advice)
		responses[i] = data
	}
	summary, err := plan.Summary(responses)
	if err != nil || summary.AnalyzedFiles != 2267 || len(summary.UncertainIDs) != 2267 || len(summary.UnanalyzedIDs) != 0 {
		t.Fatal("whole summary", err)
	}
	delete(responses, 0)
	summary, err = plan.Summary(responses)
	if err != nil || summary.AnalyzedFiles != 2227 || len(summary.UnanalyzedIDs) != 40 {
		t.Fatal("partial coverage misrepresented", err)
	}
	responses[0] = responses[1]
	if _, err = plan.Summary(responses); err == nil {
		t.Fatal("foreign packet advice aggregated")
	}
	if _, err = s.PlanBatch("", root.ID); err == nil {
		t.Fatal("empty snapshot")
	}
	if _, err = s.PlanBatch(root.SHA, ""); err == nil {
		t.Fatal("empty scope expanded")
	}
	v.State.Files = append(v.State.Files, projectmemory.File{ID: "new-file", Status: "present", LastEvent: "event-1", Fact: projectmemory.Fact{Path: "/projects/one/new.txt", Kind: "file", HashState: "metadata_only", ModifiedAt: v.State.LastObservedAt}})
	changed, err := NewScopeIndex(v, profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = changed.PlanBatch(root.SHA, root.ID); err == nil {
		t.Fatal("stale scope")
	}
}
