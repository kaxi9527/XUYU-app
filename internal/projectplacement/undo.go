package projectplacement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

var ErrUndoBudget = errors.New("这组撤销超出可核验或可记录的容量，请缩小选择；没有恢复文件")

const maxUndoGroups = 64
const maxUndoPlanBytes = 16 << 20
const maxUndoTotalBytes = 64 << 20

type UndoRow struct {
	FileID       string                      `json:"fileID"`
	Action       string                      `json:"action"` // restore, keep, conflict
	Reason       string                      `json:"reason"`
	Original     projectmemory.UndoReference `json:"original"`
	ExpectedFile projectmemory.File          `json:"expectedFile"`
	Source       RecoveryObject              `json:"source"`
	Destination  RecoveryObject              `json:"destination"`
	DirectoryIDs []int                       `json:"directoryIDs"`
}

type UndoSnapshot struct {
	OperationID    string      `json:"operationID"`
	IntentSHA      string      `json:"intentSHA"`
	JournalHead    string      `json:"journalHead"`
	MemoryRevision uint64      `json:"memoryRevision"`
	MemoryHead     string      `json:"memoryHead"`
	CreatedAt      string      `json:"createdAt"`
	ExpiresAt      string      `json:"expiresAt"`
	Rows           []UndoRow   `json:"rows"`
	Directories    []Directory `json:"directories"`
}

type UndoPreview struct {
	snapshot UndoSnapshot
	sha      string
}

func (p UndoPreview) SHA256() string { return p.sha }
func (p UndoPreview) Snapshot() UndoSnapshot {
	data, _ := json.Marshal(p.snapshot)
	var out UndoSnapshot
	_ = json.Unmarshal(data, &out)
	return out
}

type UndoChunkReference struct {
	Kind  string `json:"kind"`
	SHA   string `json:"sha"`
	Bytes int    `json:"bytes"`
	Count int    `json:"count"`
}

type UndoManifest struct {
	Schema         int                  `json:"schema"`
	GroupID        string               `json:"groupID"`
	OperationID    string               `json:"operationID"`
	IntentSHA      string               `json:"intentSHA"`
	JournalHead    string               `json:"journalHead"`
	PreviewSHA     string               `json:"previewSHA"`
	SelectionSHA   string               `json:"selectionSHA"`
	MemoryRevision uint64               `json:"memoryRevision"`
	MemoryHead     string               `json:"memoryHead"`
	CreatedAt      string               `json:"createdAt"`
	ExpiresAt      string               `json:"expiresAt"`
	Count          int                  `json:"count"`
	DirectoryCount int                  `json:"directoryCount"`
	Bytes          int                  `json:"bytes"` // serialized chunks, excluding the small root
	Chunks         []UndoChunkReference `json:"chunks"`
}

type undoChunk struct {
	Schema      int         `json:"schema"`
	Index       int         `json:"index"`
	Kind        string      `json:"kind"`
	Rows        []UndoRow   `json:"rows,omitempty"`
	Directories []Directory `json:"directories,omitempty"`
}

type undoPlan struct {
	Manifest    UndoManifest
	Rows        []UndoRow
	Directories []Directory
}

type UndoItem struct {
	FileID        string           `json:"fileID"`
	OriginalClaim string           `json:"originalClaim"`
	Stage         string           `json:"stage"` // pending, conflict, claimed, memory_pending, undone, not_started, uncertain
	Claim         string           `json:"claim,omitempty"`
	ClaimedAt     string           `json:"claimedAt,omitempty"`
	Receipt       *PhysicalReceipt `json:"receipt,omitempty"`
	PhysicalSHA   string           `json:"physicalSHA,omitempty"`
	Memory        *MemoryReceipt   `json:"memory,omitempty"`
	Reason        string           `json:"reason,omitempty"`
}

type UndoGroup struct {
	ID            string     `json:"id"`
	ManifestSHA   string     `json:"manifestSHA"`
	SelectionSHA  string     `json:"selectionSHA"`
	PlanBytes     int        `json:"planBytes"`
	Status        string     `json:"status"`
	ConfirmedAt   string     `json:"confirmedAt"`
	Reserved      uint64     `json:"reserved"`
	Items         []UndoItem `json:"items"`
	StopRequested bool       `json:"stopRequested"`
}

type undoTransition struct {
	GroupID      string `json:"groupID"`
	ManifestSHA  string `json:"manifestSHA"`
	Count        int    `json:"count,omitempty"`
	SelectionSHA string `json:"selectionSHA,omitempty"`
}

func (e *Executor) ReviewUndo(ctx context.Context, id, intentSHA string) (UndoPreview, error) {
	return e.reviewUndo(ctx, id, intentSHA)
}
func (e *Executor) ConfirmUndo(ctx context.Context, groupID string, preview UndoPreview, selected []string) (JournalView, error) {
	return e.confirmUndo(ctx, groupID, preview, selected)
}
func (e *Executor) ExecuteUndo(ctx context.Context, id, intentSHA, groupID, manifestSHA string) (JournalView, error) {
	return e.executeUndo(ctx, id, intentSHA, groupID, manifestSHA)
}

func undoGroupIndex(v JournalView, id string) int {
	for i, g := range v.UndoGroups {
		if g.ID == id {
			return i
		}
	}
	return -1
}
func undoItemIndex(g UndoGroup, id string) int {
	for i, item := range g.Items {
		if item.FileID == id {
			return i
		}
	}
	return -1
}
func undoRow(plan undoPlan, id string) (UndoRow, bool) {
	for _, r := range plan.Rows {
		if r.FileID == id {
			return r, true
		}
	}
	return UndoRow{}, false
}
func undoReserved(v JournalView) uint64 {
	var n uint64
	for _, g := range v.UndoGroups {
		n += g.Reserved
	}
	return n
}
func undoUnsettled(v JournalView) bool {
	for _, g := range v.UndoGroups {
		for _, i := range g.Items {
			if i.Stage == "claimed" || i.Stage == "memory_pending" || i.Stage == "uncertain" {
				return true
			}
		}
	}
	return false
}
func undoOwned(v JournalView, fileID string) bool {
	for _, g := range v.UndoGroups {
		for _, i := range g.Items {
			if i.FileID != fileID {
				continue
			}
			if i.Stage == "undone" || i.Stage == "claimed" || i.Stage == "memory_pending" || i.Stage == "uncertain" {
				return true
			}
			if i.Stage == "pending" && g.Status != "stopped" && g.Status != "completed" {
				return true
			}
		}
	}
	return false
}

func undoOriginal(v JournalView, item ItemState) (projectmemory.UndoReference, error) {
	r, ok := selectedRow(v, item.FileID)
	if !ok || r.Action != "move" || item.Memory == nil {
		return projectmemory.UndoReference{}, ErrReconcile
	}
	o := projectmemory.UndoReference{OperationID: v.Intent.ID, Claim: item.Claim, FileID: item.FileID, From: r.Before, EventID: item.Memory.EventID}
	if item.Stage == "moved" && item.Receipt != nil && item.Receipt.Outcome == "moved" && item.Receipt.After != nil {
		o.Kind, o.ProofSHA, o.To = "placement", item.PhysicalSHA, *item.Receipt.After
	} else if item.Stage == "reconciled" && item.Recovery != nil && item.Recovery.Review.Outcome == "at_target" && item.Recovery.Review.Target.Fact != nil {
		o.Kind, o.ProofSHA, o.To = "reconciliation", item.Recovery.ReviewSHA, *item.Recovery.Review.Target.Fact
	} else {
		return o, ErrReconcile
	}
	return o, nil
}

func reverseRow(v JournalView, r UndoRow) (Row, error) {
	original, ok := selectedRow(v, r.FileID)
	if !ok || r.Source.Fact == nil {
		return Row{}, ErrJournal
	}
	original.Source, original.Target, original.Before, original.Action = r.Source.Path, r.Destination.Path, *r.Source.Fact, "move"
	return original, nil
}

func undoEvidence(v JournalView, g UndoGroup, item UndoItem) (projectmemory.UndoEvidence, error) {
	p, ok := v.undoPlans[g.ID]
	if !ok {
		return projectmemory.UndoEvidence{}, ErrJournal
	}
	r, ok := undoRow(p, item.FileID)
	if !ok || item.Receipt == nil || item.Receipt.Outcome != "moved" || item.Receipt.After == nil {
		return projectmemory.UndoEvidence{}, ErrJournal
	}
	x := item.Receipt
	e := projectmemory.UndoEvidence{Project: v.Intent.Preview.Authority.Project, UndoID: g.ID, Claim: item.Claim, PlanSHA: g.ManifestSHA, PhysicalSHA: item.PhysicalSHA, Original: r.Original, Scope: v.Intent.Preview.Authority.Scope, ExpectedFile: r.ExpectedFile, Before: x.Before, After: *x.After, MemoryRevision: x.MemoryRevision, OccurredAt: x.OccurredAt}
	return e, e.Validate()
}

func (e *Executor) saveUndoMemory(s *journalSession, g UndoGroup, item UndoItem) error {
	proof, err := undoEvidence(s.view, g, item)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = e.fail("undo_before_memory"); err != nil {
		return err
	}
	var saved projectmemory.Result
	for attempt := 0; attempt < 4; attempt++ {
		v, readErr := e.memory.Read(ctx)
		if readErr != nil {
			return readErr
		}
		saved, err = e.memory.RecordUndo(ctx, v.State.Revision, proof, time.Now())
		if !errors.Is(err, projectmemory.ErrConflict) {
			break
		}
	}
	if err != nil {
		return err
	}
	if err = e.fail("undo_after_memory"); err != nil {
		return err
	}
	if saved.ProjectionPending || !goodSHA(saved.RecordSHA) || len(saved.Record.Events) != 1 || saved.Record.Events[0].Undo == nil {
		return ErrReconcile
	}
	if saved.Record.Events[0].Undo.Disposition == "history_only" {
		if _, err = e.journal.RequestUndoStop(ctx, s.view.Intent.ID, s.view.IntentSHA, g.ID, g.ManifestSHA, time.Now()); err != nil {
			return err
		}
	}
	return s.append(journalRecord{Kind: "undo_memory_saved", At: time.Now().UTC().Format(time.RFC3339Nano), FileID: item.FileID, Claim: item.Claim, Undo: &undoTransition{GroupID: g.ID, ManifestSHA: g.ManifestSHA}, Memory: &MemoryReceipt{ProjectID: proof.Project.ID, Operation: saved.Record.OperationID, Revision: saved.Record.Revision, RecordSHA: saved.RecordSHA, EventID: saved.Record.Events[0].ID}})
}

func makeUndoPlan(v JournalView, groupID string, p UndoPreview, selected []string) (undoPlan, []byte, [][]byte, error) {
	snapshot := p.Snapshot()
	if !safeID(groupID) || p.sha == "" || digestJSON(snapshot) != p.sha || snapshot.OperationID != v.Intent.ID || snapshot.IntentSHA != v.IntentSHA || len(selected) == 0 || len(selected) > maxItems {
		return undoPlan{}, nil, nil, ErrConsent
	}
	ids := append([]string{}, selected...)
	sort.Strings(ids)
	rows := []UndoRow{}
	used := map[int]bool{}
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			return undoPlan{}, nil, nil, ErrConsent
		}
		found := false
		for _, r := range snapshot.Rows {
			if r.FileID == id {
				if r.Action != "restore" {
					return undoPlan{}, nil, nil, ErrConsent
				}
				for _, d := range r.DirectoryIDs {
					if d < 0 || d >= len(snapshot.Directories) {
						return undoPlan{}, nil, nil, ErrConsent
					}
					used[d] = true
				}
				rows = append(rows, r)
				found = true
				break
			}
		}
		if !found {
			return undoPlan{}, nil, nil, ErrConsent
		}
	}
	dirs := []Directory{}
	remap := map[int]int{}
	for i, d := range snapshot.Directories {
		if used[i] {
			remap[i] = len(dirs)
			dirs = append(dirs, d)
		}
	}
	for i := range rows {
		for j, d := range rows[i].DirectoryIDs {
			rows[i].DirectoryIDs[j] = remap[d]
		}
	}
	m := UndoManifest{Schema: 1, GroupID: groupID, OperationID: v.Intent.ID, IntentSHA: v.IntentSHA, JournalHead: snapshot.JournalHead, PreviewSHA: p.sha, SelectionSHA: digestJSON(ids), MemoryRevision: snapshot.MemoryRevision, MemoryHead: snapshot.MemoryHead, CreatedAt: snapshot.CreatedAt, ExpiresAt: snapshot.ExpiresAt, Count: len(rows), DirectoryCount: len(dirs), Chunks: []UndoChunkReference{}}
	plan := undoPlan{Manifest: m, Rows: rows, Directories: dirs}
	chunks, err := encodeUndoChunks(&plan)
	if err != nil {
		return undoPlan{}, nil, nil, err
	}
	root, err := json.Marshal(plan.Manifest)
	if err != nil || len(root) > maxJournalBytes || len(root)+plan.Manifest.Bytes > maxUndoPlanBytes {
		return undoPlan{}, nil, nil, ErrUndoBudget
	}
	if err = validateUndoPlan(v, plan); err != nil {
		return undoPlan{}, nil, nil, err
	}
	return plan, root, chunks, nil
}

func encodeUndoChunks(p *undoPlan) ([][]byte, error) {
	chunks := [][]byte{}
	for _, kind := range []string{"directories", "rows"} {
		count := len(p.Directories)
		if kind == "rows" {
			count = len(p.Rows)
		}
		for from := 0; from < count; {
			chunk := undoChunk{Schema: 1, Index: len(chunks), Kind: kind}
			to := from
			var accepted []byte
			for ; to < count; to++ {
				if kind == "rows" {
					chunk.Rows = append(chunk.Rows, p.Rows[to])
				} else {
					chunk.Directories = append(chunk.Directories, p.Directories[to])
				}
				data, err := json.Marshal(chunk)
				if err != nil {
					return nil, err
				}
				if len(data) > maxJournalBytes {
					break
				}
				accepted = data
			}
			if to == from || len(accepted) == 0 {
				return nil, ErrUndoBudget
			}
			p.Manifest.Bytes += len(accepted)
			if p.Manifest.Bytes > maxUndoPlanBytes {
				return nil, ErrUndoBudget
			}
			p.Manifest.Chunks = append(p.Manifest.Chunks, UndoChunkReference{Kind: kind, SHA: sumBytes(accepted), Bytes: len(accepted), Count: to - from})
			chunks = append(chunks, accepted)
			from = to
		}
	}
	return chunks, nil
}

func sumBytes(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func validateUndoPlan(v JournalView, p undoPlan) error {
	m := p.Manifest
	created, ce := time.Parse(time.RFC3339Nano, m.CreatedAt)
	expires, ee := time.Parse(time.RFC3339Nano, m.ExpiresAt)
	if m.Schema != 1 || !safeID(m.GroupID) || m.OperationID != v.Intent.ID || m.IntentSHA != v.IntentSHA || !goodSHA(m.JournalHead) || !goodSHA(m.PreviewSHA) || !goodSHA(m.SelectionSHA) || !goodSHA(m.MemoryHead) || m.MemoryRevision == 0 || m.MemoryRevision >= 1<<53 || ce != nil || ee != nil || !expires.Equal(created.Add(10*time.Minute)) || m.Count != len(p.Rows) || m.Count == 0 || m.Count > maxItems || m.DirectoryCount != len(p.Directories) || m.DirectoryCount == 0 || m.Bytes <= 0 || m.Bytes > maxUndoPlanBytes {
		return ErrJournal
	}
	ids := []string{}
	for i, d := range p.Directories {
		if !absolute(d.Path) || d.Object == "" || i > 0 && p.Directories[i-1].Path >= d.Path {
			return ErrJournal
		}
	}
	for i, r := range p.Rows {
		idx := itemIndex(v, r.FileID)
		if idx < 0 || r.Action != "restore" || i > 0 && p.Rows[i-1].FileID >= r.FileID || r.Source.Fact == nil || r.Destination.Fact != nil || r.Source.Path != r.Original.To.Path || r.Destination.Path != r.Original.From.Path || *r.Source.Fact != r.Original.To || r.Source.Mode == 0 || r.Source.ChangedAt == "" || len(r.DirectoryIDs) == 0 {
			return ErrJournal
		}
		original, err := undoOriginal(v, v.Items[idx])
		if err != nil || !reflect.DeepEqual(original, r.Original) {
			return ErrJournal
		}
		e := projectmemory.UndoEvidence{Project: v.Intent.Preview.Authority.Project, UndoID: m.GroupID, Claim: "validation", PlanSHA: m.PreviewSHA, PhysicalSHA: m.PreviewSHA, Original: r.Original, Scope: v.Intent.Preview.Authority.Scope, ExpectedFile: r.ExpectedFile, Before: r.Original.To, After: r.Original.From, MemoryRevision: m.MemoryRevision, OccurredAt: m.CreatedAt}
		if e.Validate() != nil {
			return ErrJournal
		}
		for j, index := range r.DirectoryIDs {
			if index < 0 || index >= len(p.Directories) || j > 0 && r.DirectoryIDs[j-1] >= index {
				return ErrJournal
			}
			path := p.Directories[index].Path
			if !within(path, filepath.Dir(r.Source.Path)) && !within(path, filepath.Dir(r.Destination.Path)) {
				return ErrJournal
			}
		}
		ids = append(ids, r.FileID)
	}
	if digestJSON(ids) != m.SelectionSHA {
		return ErrJournal
	}
	return nil
}
