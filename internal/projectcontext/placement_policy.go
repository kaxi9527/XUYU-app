package projectcontext

import (
	"encoding/json"
	"reflect"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// PlacementDecisionSHA uses exactly the same user-note projection as the
// placement resolver. Agent notes and unrelated revision increments are not a
// new user rule or a reason to invalidate the remaining files in a batch.
func PlacementDecisionSHA(state projectmemory.State) string {
	notes := []Note{}
	for _, note := range state.Notes {
		if note.Kind == "user_decision" {
			notes = append(notes, Note{note.ID, note.Kind, note.Text, note.Agent, note.Model, note.EventID})
		}
	}
	data, _ := json.Marshal(notes)
	return hash(data)
}

func ValidatePlacementPolicy(state projectmemory.State, profile worktype.Profile, proposal PlacementProposal) error {
	if !state.Enabled || state.Project != proposal.Project || state.Scope.Digest() != proposal.Scope.Digest() || profile.Validate() != nil || !reflect.DeepEqual(profile, proposal.Profile) || PlacementDecisionSHA(state) != proposal.DecisionSHA {
		return ErrContext
	}
	return nil
}
