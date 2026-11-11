package projectplacement

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

var ErrJournal = errors.New("整理记录损坏或不支持；原记录已保留")
var ErrOperation = errors.New("操作标识已用于另一份整理计划")
var ErrConsent = errors.New("请重新查看整理预览并确认")
var ErrReconcile = errors.New("上一步尚未核对完成；不会重新搬移文件")
var ErrNotFound = errors.New("未找到这次整理记录")
var ErrStopped = errors.New("本次整理已停止，尚未处理的文件保留原处")

const maxIntentBytes = 16 << 20
const maxJournalBytes = 128 << 10
const maxJournalRecords = maxItems*8 + 128

// Intent is immutable after preparation. Its presence alone is not consent.
// SelectedIDs are resolved by the host from the user's selected preview groups.
type Intent struct {
	Schema      int             `json:"schema"`
	ID          string          `json:"id"`
	RequestSHA  string          `json:"requestSHA"`
	PreviewSHA  string          `json:"previewSHA"`
	Preview     PreviewSnapshot `json:"preview"`
	SelectedIDs []string        `json:"selectedIDs"`
}

type PhysicalReceipt struct {
	Outcome        string              `json:"outcome"` // moved, copied, unchanged, failed, uncertain
	Before         projectmemory.Fact  `json:"before"`
	After          *projectmemory.Fact `json:"after,omitempty"`
	SourceObject   string              `json:"sourceObject,omitempty"`
	TargetObject   string              `json:"targetObject,omitempty"`
	Reason         string              `json:"reason"`
	SourceParent   string              `json:"sourceParent,omitempty"`
	TargetParent   string              `json:"targetParent,omitempty"`
	Directories    []Directory         `json:"directories,omitempty"`
	MemoryRevision uint64              `json:"memoryRevision,omitempty"`
	OccurredAt     string              `json:"occurredAt,omitempty"`
}

type CopyStage struct {
	Path   string `json:"path"`
	Object string `json:"object,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type MemoryReceipt struct {
	ProjectID string `json:"projectID"`
	Operation string `json:"operation"`
	Revision  uint64 `json:"revision"`
	RecordSHA string `json:"recordSHA"`
	EventID   string `json:"eventID"`
}

type ItemState struct {
	FileID      string            `json:"fileID"`
	Stage       string            `json:"stage"` // pending, claimed, memory_pending, moved, copied, unchanged, failed, uncertain
	Claim       string            `json:"claim,omitempty"`
	Receipt     *PhysicalReceipt  `json:"receipt,omitempty"`
	Memory      *MemoryReceipt    `json:"memory,omitempty"`
	Copy        *CopyStage        `json:"copy,omitempty"`
	ClaimedAt   string            `json:"claimedAt,omitempty"`
	PhysicalAt  string            `json:"physicalAt,omitempty"`
	PhysicalSHA string            `json:"physicalSHA,omitempty"`
	Recovery    *RecoveryDecision `json:"recovery,omitempty"`
}

type JournalView struct {
	Intent              Intent      `json:"intent"`
	IntentSHA           string      `json:"intentSHA"`
	Revision            uint64      `json:"revision"`
	HeadSHA             string      `json:"headSHA"`
	Status              string      `json:"status"`
	ConfirmedAt         string      `json:"confirmedAt,omitempty"`
	Items               []ItemState `json:"items"`
	StopRequested       bool        `json:"stopRequested"`
	NeedsReconciliation bool        `json:"needsReconciliation"`
	UndoGroups          []UndoGroup `json:"undoGroups,omitempty"`
	undoPlans           map[string]undoPlan
}

// Records are immutable and sequential. A physical receipt is first persisted
// as memory_pending; a separate event acknowledges the project's real ledger.
type journalRecord struct {
	Schema      int               `json:"schema"`
	Revision    uint64            `json:"revision"`
	PreviousSHA string            `json:"previousSHA,omitempty"`
	IntentSHA   string            `json:"intentSHA"`
	Kind        string            `json:"kind"`
	At          string            `json:"at"`
	FileID      string            `json:"fileID,omitempty"`
	Claim       string            `json:"claim,omitempty"`
	Receipt     *PhysicalReceipt  `json:"receipt,omitempty"`
	Memory      *MemoryReceipt    `json:"memory,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Copy        *CopyStage        `json:"copy,omitempty"`
	Recovery    *RecoveryDecision `json:"recovery,omitempty"`
	Undo        *undoTransition   `json:"undo,omitempty"`
}

type Journal struct {
	control string
	project projectmemory.Project
	// Tests inject failures at write/sync/publication boundaries. These hooks
	// are never exposed to RPC callers or model-generated input.
	fault func(string) error
}

func NewJournal(control string, project projectmemory.Project) (*Journal, error) {
	if !absolute(control) || !absolute(project.Path) || !safeID(project.ID) || project.Name == "" || within(pathKey(control), pathKey(project.Path)) {
		return nil, ErrScope
	}
	return &Journal{control: control, project: project}, nil
}

func safeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func digestJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func goodSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}

func validTime(value string) bool { _, err := time.Parse(time.RFC3339Nano, value); return err == nil }

func memoryOperationID(operation, claim string) string {
	return "placement-" + digestJSON(struct{ Operation, Claim string }{operation, claim})
}

// Written journals use canonical JSON. Comparing the decoded representation
// rejects duplicate keys, unknown fields, trailing data and noncanonical edits.
func decodeCanonical(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrJournal
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrJournal
	}
	canonical, err := json.Marshal(out)
	if err != nil || !bytes.Equal(data, canonical) {
		return ErrJournal
	}
	return nil
}

func (j *Journal) directory() string {
	return filepath.Join(j.control, "项目整理", j.project.ID)
}

func (j *Journal) fail(stage string) error {
	if j.fault != nil {
		return j.fault(stage)
	}
	return nil
}

func makeIntent(id string, preview Preview, selected []string) (Intent, error) {
	if !safeID(id) || preview.sha == "" || digestJSON(preview.snapshot) != preview.sha || len(selected) == 0 || len(selected) > maxItems {
		return Intent{}, ErrConsent
	}
	out := Intent{Schema: 1, ID: id, PreviewSHA: preview.sha, Preview: preview.Snapshot(), SelectedIDs: append([]string{}, selected...)}
	sort.Strings(out.SelectedIDs)
	out.RequestSHA = digestJSON(struct {
		PreviewSHA  string
		SelectedIDs []string
	}{out.PreviewSHA, out.SelectedIDs})
	if err := out.validate(); err != nil {
		return Intent{}, err
	}
	return out, nil
}

func (i Intent) validate() error {
	if i.Schema != 1 || !safeID(i.ID) || !goodSHA(i.PreviewSHA) || digestJSON(i.Preview) != i.PreviewSHA || i.RequestSHA != digestJSON(struct {
		PreviewSHA  string
		SelectedIDs []string
	}{i.PreviewSHA, i.SelectedIDs}) || validate(i.Preview.Authority, i.Preview.Proposal) != nil || i.Preview.Schema != 1 || len(i.SelectedIDs) == 0 || len(i.SelectedIDs) > maxItems {
		return ErrJournal
	}
	created, e1 := time.Parse(time.RFC3339Nano, i.Preview.CreatedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, i.Preview.ExpiresAt)
	if e1 != nil || e2 != nil || expires.Sub(created) != 30*time.Minute {
		return ErrJournal
	}
	rows := map[string]Row{}
	suggestions := i.Preview.Proposal.Suggestions
	if len(i.Preview.Rows) != len(suggestions) {
		return ErrJournal
	}
	byID := make(map[string]int, len(suggestions))
	for index, s := range suggestions {
		byID[s.File.ID] = index
	}
	for _, r := range i.Preview.Rows {
		if _, exists := rows[r.FileID]; exists {
			return ErrJournal
		}
		index, exists := byID[r.FileID]
		if !exists {
			return ErrJournal
		}
		s := suggestions[index]
		if r.Source != s.File.Fact.Path || r.Folder != s.Folder || r.Reference != s.Reference {
			return ErrJournal
		}
		if r.Action != "keep" && (filepath.Dir(r.Target) != filepath.Join(i.Preview.Authority.Project.Path, r.Folder) || r.Before.Size != s.File.Fact.Size || r.Before.ModifiedAt != s.File.Fact.ModifiedAt || s.File.Fact.HashState == "verified" && r.Before.SHA256 != s.File.Fact.SHA256) {
			return ErrJournal
		}
		rows[r.FileID] = r
	}
	previous := ""
	for _, id := range i.SelectedIDs {
		r, exists := rows[id]
		if !exists || id <= previous || (r.Action != "move" && r.Action != "copy") || r.Code != "proposed" || !i.Preview.Authority.allowed(r.Source) || !i.Preview.Authority.allowed(r.Target) || !within(i.Preview.Authority.Project.Path, r.Target) || r.Source == r.Target || !goodSHA(r.Before.SHA256) || r.Before.HashState != "verified" || r.Before.Path != r.Source || r.Object == "" {
			return ErrJournal
		}
		previous = id
	}
	return nil
}

func initialView(intent Intent) JournalView {
	out := JournalView{Intent: intent, IntentSHA: digestJSON(intent), Status: "prepared", Items: []ItemState{}}
	for _, id := range intent.SelectedIDs {
		out.Items = append(out.Items, ItemState{FileID: id, Stage: "pending"})
	}
	return out
}

func selectedRow(v JournalView, id string) (Row, bool) {
	for _, r := range v.Intent.Preview.Rows {
		if r.FileID == id {
			return r, true
		}
	}
	return Row{}, false
}

func itemIndex(v JournalView, id string) int {
	for index, item := range v.Items {
		if item.FileID == id {
			return index
		}
	}
	return -1
}

func unsettled(v JournalView) bool {
	if undoUnsettled(v) {
		return true
	}
	for _, item := range v.Items {
		if item.Stage == "claimed" || item.Stage == "memory_pending" || item.Stage == "uncertain" || item.Stage == "reconciliation_pending" {
			return true
		}
	}
	return false
}

func validatePhysical(row Row, receipt PhysicalReceipt) error {
	if receipt.Reason == "" || len(receipt.Reason) > 2000 || receipt.Before != row.Before {
		return ErrJournal
	}
	switch receipt.Outcome {
	case "moved", "copied":
		if row.Action == "move" && receipt.Outcome != "moved" || row.Action == "copy" && receipt.Outcome != "copied" || receipt.After == nil || receipt.After.Path != row.Target || receipt.After.Kind != "file" || receipt.After.Size != row.Before.Size || receipt.After.HashState != "verified" || receipt.After.SHA256 != row.Before.SHA256 || receipt.SourceObject != row.Object || receipt.TargetObject == "" {
			return ErrJournal
		}
		if _, err := time.Parse(time.RFC3339Nano, receipt.After.ModifiedAt); err != nil {
			return ErrJournal
		}
		if receipt.Outcome == "moved" && receipt.TargetObject != row.Object || receipt.Outcome == "copied" && receipt.TargetObject == row.Object {
			return ErrJournal
		}
	case "unchanged", "failed", "uncertain":
		if receipt.After != nil {
			return ErrJournal
		}
	default:
		return ErrJournal
	}
	return nil
}

func applyJournal(v JournalView, r journalRecord) (JournalView, error) {
	if r.Schema != 1 || r.Revision != v.Revision+1 || r.PreviousSHA != v.HeadSHA || r.IntentSHA != v.IntentSHA {
		return JournalView{}, ErrJournal
	}
	if r.Copy != nil && r.Kind != "copy_reserved" && r.Kind != "copy_ready" {
		return JournalView{}, ErrJournal
	}
	if r.Recovery != nil && r.Kind != "recovery_confirmed" {
		return JournalView{}, ErrJournal
	}
	if (r.Undo != nil) != undoRecordKind(r.Kind) {
		return JournalView{}, ErrJournal
	}
	at, err := time.Parse(time.RFC3339Nano, r.At)
	if err != nil {
		return JournalView{}, ErrJournal
	}
	out := v
	out.Items = append([]ItemState{}, v.Items...)
	index := itemIndex(v, r.FileID)
	switch r.Kind {
	case "recovery_confirmed":
		if index < 0 || r.Recovery == nil || r.Claim != v.Items[index].Claim || r.Receipt != nil || r.Memory != nil || r.Recovery.ConfirmedAt != r.At || r.Reason != "" || validateRecovery(v, *r.Recovery) != nil {
			return JournalView{}, ErrJournal
		}
		out.Items[index].Recovery = r.Recovery
		out.StopRequested = true
		out.Items[index].Stage, out.Status = "reconciliation_pending", "needs_reconciliation"
	case "recovery_memory_saved":
		if index < 0 || v.Items[index].Stage != "reconciliation_pending" || v.Items[index].Recovery == nil || r.Claim != v.Items[index].Claim || r.Memory == nil || r.Receipt != nil || r.Memory.ProjectID != v.Intent.Preview.Authority.Project.ID || r.Memory.Operation != projectmemory.ReconciliationOperationID(v.Intent.ID, r.Claim, v.Items[index].Recovery.ReviewSHA) || r.Memory.Revision == 0 || r.Memory.Revision >= 1<<53 || !goodSHA(r.Memory.RecordSHA) || r.Memory.EventID == "" {
			return JournalView{}, ErrJournal
		}
		out.Items[index].Memory, out.Items[index].Stage, out.Status = r.Memory, "reconciled", "stopped"
	case "confirmed":
		created, _ := time.Parse(time.RFC3339Nano, v.Intent.Preview.CreatedAt)
		expires, _ := time.Parse(time.RFC3339Nano, v.Intent.Preview.ExpiresAt)
		if v.Status != "prepared" || at.Before(created) || !at.Before(expires) || r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil {
			return JournalView{}, ErrJournal
		}
		out.Status, out.ConfirmedAt = "confirmed", r.At
	case "claimed":
		if len(v.UndoGroups) > 0 || (v.Status != "confirmed" && v.Status != "running") || unsettled(v) || index < 0 || v.Items[index].Stage != "pending" || !safeID(r.Claim) || r.Receipt != nil || r.Memory != nil || r.Reason != "" {
			return JournalView{}, ErrJournal
		}
		confirmed, _ := time.Parse(time.RFC3339Nano, v.ConfirmedAt)
		if at.Before(confirmed) || !at.Before(confirmed.Add(2*time.Hour)) {
			return JournalView{}, ErrJournal
		}
		for _, item := range v.Items {
			if item.Claim == r.Claim {
				return JournalView{}, ErrJournal
			}
		}
		out.Items[index].Stage, out.Items[index].Claim, out.Status = "claimed", r.Claim, "running"
		out.Items[index].ClaimedAt = r.At
	case "copy_reserved", "copy_ready":
		if index < 0 || v.Items[index].Stage != "claimed" || r.Claim != v.Items[index].Claim || r.Receipt != nil || r.Memory != nil || r.Copy == nil || r.Reason != "" {
			return JournalView{}, ErrJournal
		}
		row, ok := selectedRow(v, r.FileID)
		if !ok || row.Action != "copy" || r.Copy.Path != copyTemporary(row, r.Claim) {
			return JournalView{}, ErrJournal
		}
		if r.Kind == "copy_reserved" {
			if v.Items[index].Copy != nil || r.Copy.Object != "" || r.Copy.SHA256 != "" {
				return JournalView{}, ErrJournal
			}
		} else if v.Items[index].Copy == nil || v.Items[index].Copy.Object != "" || r.Copy.Object == "" || r.Copy.Object == row.Object || r.Copy.SHA256 != row.Before.SHA256 {
			return JournalView{}, ErrJournal
		}
		out.Items[index].Copy = r.Copy
	case "physical":
		if index < 0 || v.Items[index].Stage != "claimed" || r.Claim != v.Items[index].Claim || r.Receipt == nil || r.Memory != nil {
			return JournalView{}, ErrJournal
		}
		row, ok := selectedRow(v, r.FileID)
		if !ok || validatePhysical(row, *r.Receipt) != nil {
			return JournalView{}, ErrJournal
		}
		if r.Receipt.Outcome == "copied" && (v.Items[index].Copy == nil || v.Items[index].Copy.Object != r.Receipt.TargetObject || v.Items[index].Copy.SHA256 != row.Before.SHA256) {
			return JournalView{}, ErrJournal
		}
		out.Items[index].Receipt = r.Receipt
		out.Items[index].PhysicalAt, out.Items[index].PhysicalSHA = r.At, digestJSON(r)
		out.Items[index].Stage = r.Receipt.Outcome
		if r.Receipt.Outcome == "moved" || r.Receipt.Outcome == "copied" {
			out.Items[index].Stage = "memory_pending"
		} else if r.Receipt.Outcome == "uncertain" {
			out.Status = "needs_reconciliation"
		} else {
			out.Status = "stopped"
		}
	case "memory_saved":
		if index < 0 || v.Items[index].Stage != "memory_pending" || r.Claim != v.Items[index].Claim || r.Receipt != nil || r.Memory == nil || r.Memory.ProjectID != v.Intent.Preview.Authority.Project.ID || r.Memory.Operation != memoryOperationID(v.Intent.ID, r.Claim) || r.Memory.Revision == 0 || r.Memory.Revision >= 1<<53 || r.Memory.EventID == "" || !goodSHA(r.Memory.RecordSHA) {
			return JournalView{}, ErrJournal
		}
		out.Items[index].Memory, out.Items[index].Stage = r.Memory, v.Items[index].Receipt.Outcome
	case "completed":
		if v.Status != "running" || unsettled(v) || r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil {
			return JournalView{}, ErrJournal
		}
		for _, item := range v.Items {
			if item.Stage != "moved" && item.Stage != "copied" {
				return JournalView{}, ErrJournal
			}
		}
		out.Status = "completed"
	case "stopped":
		if v.Status == "completed" || v.Status == "stopped" || unsettled(v) || r.Reason == "" || len(r.Reason) > 2000 || r.FileID != "" || r.Claim != "" || r.Receipt != nil || r.Memory != nil {
			return JournalView{}, ErrJournal
		}
		out.Status = "stopped"
	default:
		if !undoRecordKind(r.Kind) {
			return JournalView{}, ErrJournal
		}
		out, err = applyUndoRecord(v, r)
		if err != nil {
			return JournalView{}, err
		}
	}
	out.Revision, out.HeadSHA = r.Revision, digestJSON(r)
	if out.Revision+undoReserved(out) > maxJournalRecords {
		return JournalView{}, ErrUndoBudget
	}
	out.NeedsReconciliation = unsettled(out)
	return out, nil
}

func copyTemporary(row Row, claim string) string {
	return filepath.Join(filepath.Dir(row.Target), ".workshelf-copy-"+claim)
}

// The public methods only persist approval and expose records. A runner lives
// in this package and uses a locked journalSession; no raw append/claim RPC is
// provided, and loading a recorded claim never executes it.
func (j *Journal) Prepare(ctx context.Context, id string, preview Preview, selected []string) (JournalView, error) {
	intent, err := makeIntent(id, preview, selected)
	if err != nil {
		return JournalView{}, err
	}
	if intent.Preview.Authority.Project != j.project {
		return JournalView{}, ErrScope
	}
	for _, row := range intent.Preview.Rows {
		if within(pathKey(j.control), pathKey(row.Source)) || row.Target != "" && within(pathKey(j.control), pathKey(row.Target)) {
			return JournalView{}, ErrScope
		}
	}
	return j.prepare(ctx, intent)
}

func (j *Journal) Confirm(ctx context.Context, id, intentSHA string, current Authority, now time.Time) (JournalView, error) {
	return j.change(ctx, id, func(s *journalSession) error {
		if s.view.IntentSHA != intentSHA || current.Project != j.project || !reflect.DeepEqual(current, s.view.Intent.Preview.Authority) {
			return ErrConsent
		}
		if s.view.ConfirmedAt != "" {
			return nil
		} // Return the original receipt; never re-confirm or resume.
		if s.view.Status != "prepared" || s.view.StopRequested {
			return ErrStopped
		}
		created, _ := time.Parse(time.RFC3339Nano, s.view.Intent.Preview.CreatedAt)
		expires, _ := time.Parse(time.RFC3339Nano, s.view.Intent.Preview.ExpiresAt)
		if now.Before(created) || !now.Before(expires) {
			return ErrConsent
		}
		return s.append(journalRecord{Kind: "confirmed", At: now.UTC().Format(time.RFC3339Nano)})
	})
}
