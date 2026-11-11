package projectplacement

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func applyUndoRecord(v JournalView, r journalRecord) (JournalView, error) {
	if r.Undo == nil || !safeID(r.Undo.GroupID) || !goodSHA(r.Undo.ManifestSHA) || r.Copy != nil || r.Recovery != nil {
		return JournalView{}, ErrJournal
	}
	out := v
	out.UndoGroups = append([]UndoGroup{}, v.UndoGroups...)
	index := undoGroupIndex(v, r.Undo.GroupID)
	if r.Kind == "undo_confirmed" {
		p, ok := v.undoPlans[r.Undo.GroupID]
		if !ok || index >= 0 || len(v.UndoGroups) >= maxUndoGroups || r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil || r.Reason != "" || validateUndoPlan(v, p) != nil || digestJSON(p.Manifest) != r.Undo.ManifestSHA || p.Manifest.JournalHead != v.HeadSHA || r.Undo.Count != p.Manifest.Count || r.Undo.SelectionSHA != p.Manifest.SelectionSHA {
			return JournalView{}, ErrJournal
		}
		created, _ := time.Parse(time.RFC3339Nano, p.Manifest.CreatedAt)
		expires, _ := time.Parse(time.RFC3339Nano, p.Manifest.ExpiresAt)
		at, _ := time.Parse(time.RFC3339Nano, r.At)
		if at.Before(created) || !at.Before(expires) {
			return JournalView{}, ErrConsent
		}
		reserved := uint64(12*p.Manifest.Count + 4)
		manifestBytes, err := json.Marshal(p.Manifest)
		if err != nil {
			return JournalView{}, ErrJournal
		}
		planBytes := p.Manifest.Bytes + len(manifestBytes)
		total := planBytes
		for _, g := range v.UndoGroups {
			total += g.PlanBytes
		}
		if total > maxUndoTotalBytes || v.Revision+1+undoReserved(v)+reserved+4 > maxJournalRecords {
			return JournalView{}, ErrUndoBudget
		}
		g := UndoGroup{ID: r.Undo.GroupID, ManifestSHA: r.Undo.ManifestSHA, SelectionSHA: r.Undo.SelectionSHA, PlanBytes: planBytes, Status: "confirmed", ConfirmedAt: r.At, Reserved: reserved, Items: []UndoItem{}}
		for _, row := range p.Rows {
			if undoOwned(v, row.FileID) {
				return JournalView{}, ErrOperation
			}
			g.Items = append(g.Items, UndoItem{FileID: row.FileID, OriginalClaim: row.Original.Claim, Stage: "pending"})
		}
		out.UndoGroups = append(out.UndoGroups, g)
		return out, nil
	}
	if index < 0 || r.Undo.Count != 0 || r.Undo.SelectionSHA != "" {
		return JournalView{}, ErrJournal
	}
	g := v.UndoGroups[index]
	if g.ManifestSHA != r.Undo.ManifestSHA || g.Reserved == 0 {
		return JournalView{}, ErrJournal
	}
	g.Items = append([]UndoItem{}, g.Items...)
	g.Reserved--
	itemIndex := undoItemIndex(g, r.FileID)
	plan, ok := v.undoPlans[g.ID]
	if !ok {
		return JournalView{}, ErrJournal
	}
	row, rowOK := undoRow(plan, r.FileID)
	itemTransition := r.Kind != "undo_completed" && r.Kind != "undo_stopped"
	if itemTransition && (!rowOK || itemIndex < 0) {
		return JournalView{}, ErrJournal
	}
	switch r.Kind {
	case "undo_claimed":
		if g.Status != "confirmed" && g.Status != "running" || g.Items[itemIndex].Stage != "pending" || !safeID(r.Claim) || r.Memory != nil || r.Receipt != nil || r.Reason != "" {
			return JournalView{}, ErrJournal
		}
		confirmed, _ := time.Parse(time.RFC3339Nano, g.ConfirmedAt)
		at, _ := time.Parse(time.RFC3339Nano, r.At)
		if at.Before(confirmed) || !at.Before(confirmed.Add(2*time.Hour)) {
			return JournalView{}, ErrConsent
		}
		for _, i := range g.Items {
			if i.Stage == "claimed" || i.Stage == "memory_pending" || i.Stage == "uncertain" {
				return JournalView{}, ErrReconcile
			}
		}
		for _, group := range v.UndoGroups {
			for _, i := range group.Items {
				if i.Claim == r.Claim {
					return JournalView{}, ErrJournal
				}
			}
		}
		for _, i := range v.Items {
			if i.Claim == r.Claim {
				return JournalView{}, ErrJournal
			}
		}
		g.Items[itemIndex].Stage, g.Items[itemIndex].Claim, g.Items[itemIndex].ClaimedAt = "claimed", r.Claim, r.At
		g.Status = "running"
	case "undo_conflict":
		if g.Items[itemIndex].Stage != "pending" || g.Status != "confirmed" && g.Status != "running" || r.Claim != "" || r.Memory != nil || r.Receipt != nil || r.Reason == "" || len(r.Reason) > 2000 {
			return JournalView{}, ErrJournal
		}
		g.Items[itemIndex].Stage, g.Items[itemIndex].Reason = "conflict", r.Reason
	case "undo_physical":
		if g.Items[itemIndex].Stage != "claimed" || r.Claim != g.Items[itemIndex].Claim || r.Receipt == nil || r.Memory != nil || r.Reason != "" {
			return JournalView{}, ErrJournal
		}
		reverse, err := reverseRow(v, row)
		if err != nil || validatePhysical(reverse, *r.Receipt) != nil {
			return JournalView{}, ErrJournal
		}
		if r.Receipt.Outcome != "moved" && r.Receipt.Outcome != "unchanged" && r.Receipt.Outcome != "uncertain" {
			return JournalView{}, ErrJournal
		}
		g.Items[itemIndex].Receipt = r.Receipt
		g.Items[itemIndex].PhysicalSHA = digestJSON(r)
		g.Items[itemIndex].Reason = r.Receipt.Reason
		switch r.Receipt.Outcome {
		case "moved":
			g.Items[itemIndex].Stage = "memory_pending"
			if _, err = undoEvidenceWithGroup(v, g, g.Items[itemIndex]); err != nil {
				return JournalView{}, err
			}
		case "unchanged":
			g.Items[itemIndex].Stage = "not_started"
		case "uncertain":
			g.Items[itemIndex].Stage, g.Status = "uncertain", "needs_reconciliation"
		}
	case "undo_memory_saved":
		item := g.Items[itemIndex]
		if item.Stage != "memory_pending" || r.Claim != item.Claim || r.Receipt != nil || r.Memory == nil || r.Reason != "" || r.Memory.ProjectID != v.Intent.Preview.Authority.Project.ID || r.Memory.Operation != projectmemory.UndoOperationID(g.ID, item.Claim) || r.Memory.Revision == 0 || r.Memory.Revision >= 1<<53 || !goodSHA(r.Memory.RecordSHA) || r.Memory.EventID == "" {
			return JournalView{}, ErrJournal
		}
		g.Items[itemIndex].Memory, g.Items[itemIndex].Stage = r.Memory, "undone"
	case "undo_completed":
		if r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil || r.Reason != "" || g.Status != "confirmed" && g.Status != "running" {
			return JournalView{}, ErrJournal
		}
		for _, i := range g.Items {
			if i.Stage != "undone" && i.Stage != "conflict" && i.Stage != "not_started" {
				return JournalView{}, ErrReconcile
			}
		}
		g.Status, g.Reserved = "completed", 0
	case "undo_stopped":
		if r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil || r.Reason == "" || len(r.Reason) > 2000 || g.Status == "completed" || g.Status == "stopped" {
			return JournalView{}, ErrJournal
		}
		for _, i := range g.Items {
			if i.Stage == "claimed" || i.Stage == "memory_pending" || i.Stage == "uncertain" {
				return JournalView{}, ErrReconcile
			}
		}
		g.Status, g.Reserved = "stopped", 0
	default:
		return JournalView{}, ErrJournal
	}
	out.UndoGroups[index] = g
	return out, nil
}

func undoEvidenceWithGroup(v JournalView, g UndoGroup, item UndoItem) (projectmemory.UndoEvidence, error) {
	return undoEvidence(v, g, item)
}

func undoRecordKind(kind string) bool { return strings.HasPrefix(kind, "undo_") }
