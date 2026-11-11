package projectcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"strings"
	"testing"
)

func TestScopeTreeLargeProjectGroupsExactCoverage(t *testing.T) {
	v, profile := fixture()
	v.State.Files = v.State.Files[2:]
	for i := 0; i < 2267; i++ {
		folder := "素材"
		if i%3 == 0 {
			folder = "需求/客户"
		}
		if i%7 == 0 {
			folder = ""
		}
		p := "/projects/one/"
		if folder != "" {
			p += folder + "/"
		}
		p += fmt.Sprintf("文件-%04d.txt", i)
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%04d", i), Status: "present", LastEvent: "event-1", Fact: projectmemory.Fact{Path: p, Kind: "file", Size: 1, HashState: "metadata_only", ModifiedAt: v.State.LastObservedAt}})
	}
	index, err := NewScopeIndex(v, profile)
	if err != nil {
		t.Fatal(err)
	}
	root, err := index.Page("", "", 0)
	if err != nil || root.CanPreview || root.TotalFiles != 2267 {
		t.Fatal("large project unavailable or mislabeled", root.TotalFiles, err)
	}
	if root.Excluded["sensitive"] != 1 || root.Excluded["environment"] != 1 {
		t.Fatal("exclusions lost")
	}
	used := map[string]bool{}
	groups := 0
	var visit func(string)
	visit = func(parent string) {
		for offset := 0; ; offset += ScopePageSize {
			page, e := index.Page(parent, index.SHA, offset)
			if e != nil {
				t.Fatal(e)
			}
			if len(page.Rows) > ScopePageSize {
				t.Fatal("unbounded page")
			}
			for _, row := range page.Rows {
				if row.Kind == "directory" {
					visit(row.ID)
					continue
				}
				p, e := index.Select(index.SHA, row.ID)
				if e != nil {
					t.Fatal(e)
				}
				groups++
				if len(p.Body.Files) > ScopeGroupFiles || len(p.Body.Notes) != len(v.State.Notes) {
					t.Fatal("large group or trimmed notes")
				}
				expected, e := Build(v, profile, p.Body.SelectedIDs, Limits{})
				if e != nil || expected.SHA256 != p.SHA256 {
					t.Fatal("scope packet diverged from existing packet builder", e)
				}
				if _, e = p.Bytes(); e != nil {
					t.Fatal(e)
				}
				for _, f := range p.Body.Files {
					if used[f.ID] {
						t.Fatal("duplicate file")
					}
					used[f.ID] = true
				}
			}
			if offset+len(page.Rows) >= page.Total {
				break
			}
		}
	}
	visit("")
	if len(used) != 2267 || groups < 57 {
		t.Fatal("incomplete grouped coverage", len(used), groups)
	}
}

func TestScopeSelectionBoundToFactsRulesAndProject(t *testing.T) {
	v, profile := fixture()
	index, err := NewScopeIndex(v, profile)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := index.Page("", "", 0)
	if !page.CanPreview {
		t.Fatal("small project cannot preview")
	}
	p, err := index.Select(index.SHA, page.ID)
	if err != nil || len(p.Body.Files) != 2 {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../../../etc", "part-unknown-0", page.ID + "x"} {
		if _, err = index.Select(index.SHA, id); err == nil {
			t.Fatal("forged scope accepted")
		}
	}
	if _, err = index.Select(strings.Repeat("f", 64), page.ID); !errors.Is(err, ErrScopeChanged) {
		t.Fatal("wrong snapshot accepted")
	}
	for _, kind := range []string{"project", "revision", "note", "rule", "file", "pause"} {
		data, _ := json.Marshal(v)
		var changed projectmemory.View
		_ = json.Unmarshal(data, &changed)
		p2 := profile
		switch kind {
		case "project":
			changed.State.Project.ID = "another"
		case "revision":
			changed.State.Revision++
		case "note":
			changed.State.Notes[0].Text = "new decision"
		case "rule":
			p2.Name = "another"
		case "file":
			changed.State.Files[0].Fact.Size++
		case "pause":
			changed.State.Enabled = false
		}
		next, e := NewScopeIndex(changed, p2)
		if e == nil {
			if _, e = next.Select(index.SHA, page.ID); e == nil {
				t.Fatal("stale scope accepted", kind)
			}
		}
	}
	profile.Roles["source"] = "参考"
	v.State.Notes[0].Text = "changed"
	again, e := index.Select(index.SHA, page.ID)
	if e != nil || again.SHA256 != p.SHA256 {
		t.Fatal("scope aliases mutable input", e)
	}
}

func TestScopeAutoGroupsRespectActualBytesAndKeepNotes(t *testing.T) {
	v, profile := fixture()
	v.State.Files = nil
	for i := 0; i < 90; i++ {
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%d", i), Status: "present", LastEvent: "event", Fact: projectmemory.Fact{Path: fmt.Sprintf("/projects/one/%s-%d.txt", strings.Repeat("名", 200), i), Kind: "file", Size: 1, ModifiedAt: v.State.LastObservedAt, HashState: "metadata_only"}})
	}
	for i := 0; i < 4; i++ {
		v.State.Notes = append(v.State.Notes, projectmemory.Note{ID: fmt.Sprintf("note-%d", i), Kind: "user_decision", Text: strings.Repeat("x", 10000), EventID: "note-event"})
	}
	index, e := NewScopeIndex(v, profile)
	if e != nil {
		t.Fatal(e)
	}
	page, e := index.Page("", "", 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, row := range page.Rows {
		p, e := index.Select(index.SHA, row.ID)
		if e != nil || len(p.Body.Notes) != len(v.State.Notes) || row.Files >= 40 {
			t.Fatal("adaptive group failed", row.Files, e)
		}
		count += row.Files
	}
	if count != 90 {
		t.Fatal("adaptive split lost files")
	}
	v.State.Notes = append(v.State.Notes, projectmemory.Note{ID: "too-much", Kind: "user_decision", Text: strings.Repeat("z", 12000), EventID: "note-event"})
	if _, e = NewScopeIndex(v, profile); !errors.Is(e, ErrScopeTooLarge) {
		t.Fatal("critical notes silently trimmed", e)
	}
}

func TestScopePagesAndExactMembers(t *testing.T) {
	v, profile := fixture()
	v.State.Files = nil
	v.State.Scope = projectmemory.Scope{Files: []string{}}
	for i := 0; i < 60; i++ {
		p := fmt.Sprintf("/external/%d/report.txt", i)
		v.State.Scope.Files = append(v.State.Scope.Files, p)
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%d", i), Status: "present", LastEvent: "event", Fact: projectmemory.Fact{Path: p, Kind: "file", Size: 1, ModifiedAt: v.State.LastObservedAt, HashState: "metadata_only"}})
	}
	index, e := NewScopeIndex(v, profile)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := index.Page("", "", 0)
	if len(root.Rows) != 1 || root.Rows[0].Name != "单独登记的文件" {
		t.Fatal("exact files became per-file approval rows")
	}
	rows, e := index.Page(root.Rows[0].ID, index.SHA, 0)
	if e != nil || len(rows.Rows) != 2 || rows.Files != 60 {
		t.Fatal("exact group counts", e)
	}
	v.State.Scope = projectmemory.Scope{Roots: []string{"/external"}}
	index, e = NewScopeIndex(v, profile)
	if e != nil {
		t.Fatal(e)
	}
	first, _ := index.Page("", "", 0)
	second, e := index.Page("", index.SHA, 50)
	if e != nil || len(first.Rows) != 50 || len(second.Rows) != 10 {
		t.Fatal("page boundary", e)
	}
}
