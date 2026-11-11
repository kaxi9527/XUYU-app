package projectcontext

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// PlacementEvidence is loaded from the project's successful analysis receipts,
// never accepted as an RPC input. A validated suggestion is still not permission
// or proof that the files have remained unchanged on disk.
type PlacementEvidence struct {
	Kind      string
	RequestID string
	Part      int
	Packet    Packet
	Advice    json.RawMessage
	Recipient models.Recipient
}

type PlacementReference struct {
	Kind       string           `json:"kind"`
	RequestID  string           `json:"requestID"`
	Part       int              `json:"part"`
	ContextSHA string           `json:"contextSHA"`
	AdviceSHA  string           `json:"adviceSHA"`
	Recipient  models.Recipient `json:"recipient"`
}

type PlacementSuggestion struct {
	File            projectmemory.File `json:"file"`
	Folder          string             `json:"folder"`
	SuggestedTarget string             `json:"suggestedTarget"`
	Reason          string             `json:"reason"`
	Reference       int                `json:"reference"`
}

type PlacementProposal struct {
	Project        projectmemory.Project `json:"project"`
	Scope          projectmemory.Scope   `json:"scope"`
	MemoryRevision uint64                `json:"memoryRevision"`
	MemoryHead     string                `json:"memoryHead"`
	Profile        worktype.Profile      `json:"profile"`
	DecisionSHA    string                `json:"decisionSHA"`
	References     []PlacementReference  `json:"references"`
	Suggestions    []PlacementSuggestion `json:"suggestions"`
	UncertainIDs   []string              `json:"uncertainIDs"`
	UntouchedFiles int                   `json:"untouchedFiles"`
}

func userDecisions(notes []Note) []Note {
	out := []Note{}
	for _, n := range notes {
		if n.Kind == "user_decision" {
			out = append(out, n)
		}
	}
	return out
}

// ResolvePlacements maps opaque model IDs back to current authoritative files.
// It performs no I/O, collision resolution, authorization or file operations.
// The physical preview must independently validate regular files, engineering
// boundaries, source identity/content, target availability and active writers.
func ResolvePlacements(view projectmemory.View, profile worktype.Profile, evidence []PlacementEvidence) (PlacementProposal, error) {
	if len(evidence) == 0 || len(evidence) > MaxBatchParts || !filepath.IsAbs(view.State.Project.Path) || filepath.Clean(view.State.Project.Path) != view.State.Project.Path || !view.State.Scope.Allows(view.State.Project.Path) {
		return PlacementProposal{}, ErrContext
	}
	current, err := buildBody(view, profile, nil)
	if err != nil {
		return PlacementProposal{}, err
	}
	decisions := userDecisions(current.Notes)
	d, _ := json.Marshal(decisions)
	out := PlacementProposal{Project: view.State.Project, Scope: view.State.Scope, MemoryRevision: view.State.Revision, MemoryHead: view.HeadSHA, Profile: profile, DecisionSHA: hash(d), References: []PlacementReference{}, Suggestions: []PlacementSuggestion{}, UncertainIDs: []string{}}
	currentFiles := map[string]File{}
	actualFiles := map[string]projectmemory.File{}
	for _, f := range current.Files {
		currentFiles[f.ID] = f
	}
	for _, f := range view.State.Files {
		actualFiles[f.ID] = f
	}
	used := map[string]bool{}
	refs := map[string]map[int]bool{}
	for _, e := range evidence {
		if (e.Kind != "single" && e.Kind != "batch") || !id(e.RequestID) || len(e.RequestID) > 64 || e.Part < 0 || e.Part >= MaxBatchParts || e.Kind == "single" && e.Part != 0 || !e.Recipient.Valid() {
			return PlacementProposal{}, ErrContext
		}
		key := e.Kind + ":" + e.RequestID
		if refs[key] == nil {
			refs[key] = map[int]bool{}
		}
		if refs[key][e.Part] {
			return PlacementProposal{}, ErrContext
		}
		refs[key][e.Part] = true
		p := e.Packet
		a, err := p.ValidateAdvice(e.Advice)
		if err != nil {
			return PlacementProposal{}, err
		}
		if p.Body.ProjectID != out.Project.ID || p.Body.ProjectName != out.Project.Name || p.Body.ScopeSHA != out.Scope.Digest() || p.Body.MemoryRevision > out.MemoryRevision || !reflect.DeepEqual(p.Body.Profile, profile) || !reflect.DeepEqual(userDecisions(p.Body.Notes), decisions) || !reflect.DeepEqual(p.Body.Folders, current.Folders) {
			return PlacementProposal{}, ErrContext
		}
		for _, old := range p.Body.Files {
			f, ok := currentFiles[old.ID]
			if !ok || f != old || used[old.ID] {
				return PlacementProposal{}, ErrContext
			}
			used[old.ID] = true
		}
		folders := map[string]string{}
		for _, f := range p.Body.Folders {
			folders[f.ID] = f.Path
		}
		ref := len(out.References)
		out.References = append(out.References, PlacementReference{e.Kind, e.RequestID, e.Part, p.SHA256, hash(e.Advice), e.Recipient})
		for _, group := range a.Groups {
			folder := folders[group.FolderID]
			for _, fileID := range group.FileIDs {
				f := actualFiles[fileID]
				target := filepath.Join(out.Project.Path, folder, filepath.Base(f.Fact.Path))
				if _, ok := relative(out.Project.Path, target); !ok || excluded(target) != "" {
					return PlacementProposal{}, ErrContext
				}
				out.Suggestions = append(out.Suggestions, PlacementSuggestion{f, folder, target, group.Reason, ref})
			}
		}
		out.UncertainIDs = append(out.UncertainIDs, a.UncertainIDs...)
	}
	for _, f := range current.Files {
		if f.Kind == "file" && f.Status == "present" && !used[f.ID] {
			out.UntouchedFiles++
		}
	}
	sort.Slice(out.Suggestions, func(i, j int) bool { return out.Suggestions[i].File.ID < out.Suggestions[j].File.ID })
	sort.Strings(out.UncertainIDs)
	// Detach maps, slices and optional recipient proof from mutable callers.
	data, err := json.Marshal(out)
	if err != nil {
		return PlacementProposal{}, ErrContext
	}
	var detached PlacementProposal
	if json.Unmarshal(data, &detached) != nil {
		return PlacementProposal{}, ErrContext
	}
	return detached, nil
}
