// Package projectmemory owns a project's file-management event log. Providers
// and agents are evidence fields, never storage partitions or project identity.
package projectmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const schema = 1
const maxRecordBytes = 16 << 20
const maxStateBytes = 32 << 20
const maxObservations = 10000

var ErrConflict = errors.New("项目记忆已更新，请重新读取")
var ErrOperationReused = errors.New("同一操作标识不能用于不同请求")
var ErrCorrupt = errors.New("项目记忆记录损坏或格式不支持；原记录未修改")
var ErrBudget = errors.New("项目记忆超过本次处理限额；原记录保留")

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}
type Scope struct {
	Roots []string `json:"roots"`
	Files []string `json:"files,omitempty"`
}
type Coverage struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
type Fact struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // file, directory, link, other
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
	ObjectKey  string `json:"objectKey,omitempty"` // Adapter-proven object identity, never a content hash.
	SHA256     string `json:"sha256,omitempty"`
	HashState  string `json:"hashState"` // verified, metadata_only, unavailable
}
type File struct {
	ID              string `json:"id"`
	Fact            Fact   `json:"fact"`
	Status          string `json:"status"` // present, missing, untracked
	FirstObservedAt string `json:"firstObservedAt"`
	LastEvent       string `json:"lastEvent"`
}
type Observation struct {
	ScopeSHA  string     `json:"scopeSHA"`
	StartedAt string     `json:"startedAt"`
	EndedAt   string     `json:"endedAt"`
	Complete  bool       `json:"complete"`
	Coverage  []Coverage `json:"coverage"`
	Files     []Fact     `json:"files"`
}
type Note struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	EventID  string `json:"eventID"`
}
type Operation struct {
	ID         string `json:"id"`
	RequestSHA string `json:"requestSHA"`
	Revision   uint64 `json:"revision"`
}
type State struct {
	Sources             []SourceGrant           `json:"sources,omitempty"`
	Origins             []FileOrigin            `json:"origins,omitempty"`
	Schema              int                     `json:"schema"`
	Project             Project                 `json:"project"`
	Revision            uint64                  `json:"revision"`
	Scope               Scope                   `json:"scope"`
	Enabled             bool                    `json:"enabled"`
	Automatic           bool                    `json:"automatic,omitempty"`
	AutomaticSince      string                  `json:"automaticSince,omitempty"`
	Files               []File                  `json:"files"`
	Notes               []Note                  `json:"notes"`
	Operations          []Operation             `json:"operations"`
	LastObservedAt      string                  `json:"lastObservedAt,omitempty"`
	ObservationComplete bool                    `json:"observationComplete"`
	Coverage            []Coverage              `json:"coverage"`
	Placements          []PlacementSummary      `json:"placements,omitempty"`
	Reconciliations     []ReconciliationSummary `json:"reconciliations,omitempty"`
	Undos               []UndoSummary           `json:"undos,omitempty"`
}
type Event struct {
	Auto           *AutoEvidence       `json:"auto,omitempty"`
	SourceGrant    *SourceGrant        `json:"sourceGrant,omitempty"`
	SourceRead     *SourceRead         `json:"sourceRead,omitempty"`
	Origin         *FileOrigin         `json:"origin,omitempty"`
	ID             string              `json:"id"`
	Kind           string              `json:"kind"`
	ObservedAt     string              `json:"observedAt"`
	Source         string              `json:"source"`
	Actor          string              `json:"actor"` // unknown for filesystem observations
	Reason         string              `json:"reason"`
	Before         *File               `json:"before,omitempty"`
	After          *File               `json:"after,omitempty"`
	Scope          *Scope              `json:"scope,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
	Automatic      *bool               `json:"automatic,omitempty"`
	Observation    *Observation        `json:"observation,omitempty"` // completion metadata only, no repeated file list
	Note           *Note               `json:"note,omitempty"`
	Placement      *PlacementFact      `json:"placement,omitempty"`
	Reconciliation *ReconciliationFact `json:"reconciliation,omitempty"`
	Undo           *UndoFact           `json:"undo,omitempty"`
}
type Record struct {
	Schema      int     `json:"schema"`
	Project     Project `json:"project"`
	Revision    uint64  `json:"revision"`
	PreviousSHA string  `json:"previousSHA,omitempty"`
	OperationID string  `json:"operationID"`
	RequestSHA  string  `json:"requestSHA"`
	StateSHA    string  `json:"stateSHA"`
	Events      []Event `json:"events"`
}
type View struct {
	State             State  `json:"state"`
	HeadSHA           string `json:"headSHA"`
	ProjectionPending bool   `json:"projectionPending"`
}
type Result struct {
	Record            Record `json:"record"`
	RecordSHA         string `json:"recordSHA,omitempty"`
	ProjectionPending bool   `json:"projectionPending"`
	ProjectionError   string `json:"projectionError,omitempty"`
}

func digest(b []byte) string  { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func digestJSON(v any) string { b, _ := json.Marshal(v); return digest(b) }
func hashOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func pathOK(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsRune(s, 0)
}
func textOK(s string, n int) bool { return strings.TrimSpace(s) != "" && len(s) <= n }
func idOK(s string) bool {
	if !textOK(s, 128) {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func timeOK(s string) bool { _, e := time.Parse(time.RFC3339Nano, s); return e == nil }
func within(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func (s Scope) Allows(path string) bool {
	if !pathOK(path) {
		return false
	}
	for _, root := range s.Roots {
		if within(root, path) {
			return true
		}
	}
	return contains(s.Files, path)
}
func (s Scope) Digest() string { return digestJSON(s) }
func (s Scope) Validate() error {
	if len(s.Roots)+len(s.Files) == 0 || len(s.Roots)+len(s.Files) > 100 {
		return errors.New("追踪范围为空或过多")
	}
	seen := map[string]bool{}
	for _, list := range [][]string{s.Roots, s.Files} {
		for _, p := range list {
			if !pathOK(p) || seen[p] {
				return errors.New("追踪范围无效或重复")
			}
			seen[p] = true
		}
	}
	return nil
}
func (f Fact) validate(scope Scope) error {
	if !scope.Allows(f.Path) || !contains([]string{"file", "directory", "link", "other"}, f.Kind) || f.Size < 0 || !timeOK(f.ModifiedAt) || len(f.ObjectKey) > 512 {
		return errors.New("文件观察信息无效")
	}
	if !contains([]string{"verified", "metadata_only", "unavailable"}, f.HashState) || f.HashState == "verified" && (!hashOK(f.SHA256) || f.Kind != "file") || f.HashState != "verified" && f.SHA256 != "" {
		return errors.New("内容核验状态无效")
	}
	return nil
}
func (s State) Validate() error {
	if err := validateSources(s); err != nil {
		return err
	}
	undone := map[string]bool{}
	for _, u := range s.Undos {
		key := digestJSON(struct{ Operation, Claim string }{u.OriginalOperation, u.OriginalClaim})
		if u.validate() != nil || undone[key] {
			return ErrCorrupt
		}
		undone[key] = true
	}
	for _, p := range s.Placements {
		if !idOK(p.SourceFileID) || !idOK(p.TargetFileID) || !textOK(p.EventID, 160) || !idOK(p.OperationID) || !idOK(p.Claim) || !pathOK(p.From) || !pathOK(p.To) || !timeOK(p.OccurredAt) || !contains([]string{"moved", "copied"}, p.Outcome) || !contains([]string{"applied", "already_observed", "history_only"}, p.Disposition) || !hashOK(p.PhysicalSHA) {
			return ErrCorrupt
		}
	}
	if s.Automatic && !timeOK(s.AutomaticSince) || !s.Automatic && s.AutomaticSince != "" {
		return ErrCorrupt
	}
	if s.Schema != schema || !idOK(s.Project.ID) || !textOK(s.Project.Name, 1200) || !pathOK(s.Project.Path) || s.Revision == 0 {
		return ErrCorrupt
	}
	if err := s.Scope.Validate(); err != nil {
		return err
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, f := range s.Files {
		if !idOK(f.ID) || ids[f.ID] || !timeOK(f.FirstObservedAt) || !textOK(f.LastEvent, 160) || !contains([]string{"present", "missing", "untracked"}, f.Status) {
			return ErrCorrupt
		}
		ids[f.ID] = true
		// Historical locations may be outside the current scope after a scope change.
		if err := f.Fact.validate(Scope{Files: []string{f.Fact.Path}}); err != nil {
			return err
		}
		if f.Status == "present" {
			if !s.Scope.Allows(f.Fact.Path) || paths[f.Fact.Path] {
				return ErrCorrupt
			}
			paths[f.Fact.Path] = true
		}
	}
	ops := map[string]bool{}
	for _, op := range s.Operations {
		if !textOK(op.ID, 128) || ops[op.ID] || !hashOK(op.RequestSHA) || op.Revision == 0 || op.Revision > s.Revision {
			return ErrCorrupt
		}
		ops[op.ID] = true
	}
	if s.LastObservedAt != "" && !timeOK(s.LastObservedAt) {
		return ErrCorrupt
	}
	for _, c := range s.Coverage {
		if !pathOK(c.Path) || !contains([]string{"complete", "offline", "error", "limited", "excluded"}, c.Status) || len(c.Reason) > 4000 {
			return ErrCorrupt
		}
		if s.ObservationComplete && c.Status != "complete" {
			return ErrCorrupt
		}
	}
	notes := map[string]bool{}
	for _, r := range s.Reconciliations {
		if !textOK(r.EventID, 160) || !idOK(r.OperationID) || !idOK(r.Claim) || !idOK(r.SourceFileID) || !idOK(r.TargetFileID) || !pathOK(r.From) || !pathOK(r.To) || !timeOK(r.ObservedAt) || !timeOK(r.ConfirmedAt) || !hashOK(r.ReviewSHA) || !contains([]string{"at_source", "at_target", "copy_published", "copy_unpublished"}, r.Outcome) || !contains([]string{"history_only", "applied", "already_observed"}, r.Disposition) || r.TemporaryPath != "" && !pathOK(r.TemporaryPath) {
			return ErrCorrupt
		}
	}
	for _, n := range s.Notes {
		if !idOK(n.ID) || notes[n.ID] || !textOK(n.EventID, 160) || !textOK(n.Text, 12000) || len(n.Agent) > 128 || len(n.Model) > 256 || len(n.Evidence) > 4000 || !contains([]string{"user_decision", "agent_proposal"}, n.Kind) || n.Kind == "user_decision" && (n.Agent != "" || n.Model != "") {
			return ErrCorrupt
		}
		notes[n.ID] = true
	}
	return nil
}
func applyRecord(previous State, r Record) (State, error) {
	if r.Schema != schema || r.Project.ID == "" || r.Revision != previous.Revision+1 || !textOK(r.OperationID, 128) || !hashOK(r.RequestSHA) || len(r.Events) == 0 || len(r.Events) > maxObservations*2+2 {
		return State{}, ErrCorrupt
	}
	if previous.Revision > 0 && r.Project != previous.Project {
		return State{}, ErrCorrupt
	}
	for _, event := range r.Events {
		if event.Kind == "placement_reconciled" || event.Reconciliation != nil {
			if err := validateReconciliationRecord(previous, r); err != nil {
				return State{}, err
			}
			break
		}
	}
	state := previous
	state.Schema = schema
	state.Project = r.Project
	state.Revision = r.Revision
	files := map[string]File{}
	for _, f := range previous.Files {
		files[f.ID] = f
	}
	state.Notes = append([]Note{}, previous.Notes...)
	state.Operations = append([]Operation{}, previous.Operations...)
	state.Placements = append([]PlacementSummary{}, previous.Placements...)
	state.Reconciliations = append([]ReconciliationSummary{}, previous.Reconciliations...)
	state.Undos = append([]UndoSummary{}, previous.Undos...)
	state.Sources = append([]SourceGrant{}, previous.Sources...)
	state.Origins = append([]FileOrigin{}, previous.Origins...)
	for i, e := range r.Events {
		if e.Auto != nil && e.Kind != "automatic_project_organized" && e.Kind != "automatic_project_restored" {
			return State{}, ErrCorrupt
		}
		if e.SourceGrant != nil && e.Kind != "source_grant_changed" || e.SourceRead != nil && e.Kind != "source_checked" || e.Origin != nil && e.Kind != "file_source_recorded" {
			return State{}, ErrCorrupt
		}
		if e.Undo != nil && e.Kind != "placement_undone" {
			return State{}, ErrCorrupt
		}
		if e.Placement != nil && e.Kind != "placement_recorded" {
			return State{}, ErrCorrupt
		}
		if e.ID != fmt.Sprintf("%d:%d", r.Revision, i+1) || !timeOK(e.ObservedAt) || !textOK(e.Reason, 4000) {
			return State{}, ErrCorrupt
		}
		switch e.Kind {
		case "automatic_project_organized", "automatic_project_restored":
			if e.Source != "local_execution" || e.Actor != "workshelf" || e.Auto == nil || e.Auto.validate() != nil {
				return State{}, ErrCorrupt
			}
		case "source_grant_changed", "source_checked", "file_source_recorded":
			if err := applySourceEvent(&state, e); err != nil {
				return State{}, err
			}
		case "placement_undone":
			if len(r.Events) != 1 || e.Undo == nil || r.OperationID != UndoOperationID(e.Undo.Evidence.UndoID, e.Undo.Evidence.Claim) {
				return State{}, ErrCorrupt
			}
			if err := applyUndoEvent(&state, files, e); err != nil {
				return State{}, err
			}
		case "placement_reconciled":
			state.Reconciliations = append(state.Reconciliations, reconciliationSummary(e))
		case "placement_recorded":
			if len(r.Events) != 1 || e.Placement == nil || r.OperationID != PlacementOperationID(e.Placement.Evidence.OperationID, e.Placement.Evidence.Claim) {
				return State{}, ErrCorrupt
			}
			if err := applyPlacementEvent(&state, files, e); err != nil {
				return State{}, err
			}
		case "automatic_changed":
			if e.Source != "local_user" || e.Actor != "local_user" || e.Automatic == nil || e.Scope == nil || e.Scope.Digest() != state.Scope.Digest() || *e.Automatic && !state.Enabled {
				return State{}, ErrCorrupt
			}
			state.Automatic = *e.Automatic
			state.AutomaticSince = ""
			if state.Automatic {
				state.AutomaticSince = e.ObservedAt
			}
		case "tracking_started", "scope_changed", "tracking_state":
			if e.Source != "local_user" || e.Actor != "local_user" || e.Scope == nil || e.Enabled == nil {
				return State{}, ErrCorrupt
			}
			state.Scope = *e.Scope
			state.Enabled = *e.Enabled
			if e.Kind == "scope_changed" {
				state.ObservationComplete = false
				state.Coverage = nil
				for _, p := range append(append([]string{}, e.Scope.Roots...), e.Scope.Files...) {
					state.Coverage = append(state.Coverage, Coverage{Path: p, Status: "limited", Reason: "追踪范围已改变，尚未重新观察"})
				}
			}
		case "first_observed", "metadata_changed", "content_changed", "location_changed", "no_longer_seen", "seen_again", "tracking_removed":
			if e.Source != "filesystem_observation" && e.Source != "local_user" || e.Actor != "unknown" && e.Actor != "local_user" || e.After == nil || e.After.LastEvent != e.ID {
				return State{}, ErrCorrupt
			}
			if old, ok := files[e.After.ID]; e.Before != nil {
				if !ok || digestJSON(old) != digestJSON(*e.Before) {
					return State{}, ErrCorrupt
				}
			} else if ok {
				return State{}, ErrCorrupt
			}
			files[e.After.ID] = *e.After
		case "observation_completed":
			if e.Source != "filesystem_observation" || e.Actor != "unknown" || e.Observation == nil || len(e.Observation.Files) != 0 || e.Observation.ScopeSHA != state.Scope.Digest() {
				return State{}, ErrCorrupt
			}
			state.LastObservedAt = e.Observation.EndedAt
			state.ObservationComplete = e.Observation.Complete
			state.Coverage = append([]Coverage{}, e.Observation.Coverage...)
		case "context_note":
			if e.Note == nil || e.Note.EventID != e.ID || !textOK(e.Note.Text, 12000) || !contains([]string{"user_decision", "agent_proposal"}, e.Note.Kind) {
				return State{}, ErrCorrupt
			}
			if e.Note.Kind == "user_decision" && (e.Source != "local_user" || e.Actor != "local_user") || e.Note.Kind == "agent_proposal" && (e.Source != "agent_declaration" || e.Actor != "unverified") {
				return State{}, ErrCorrupt
			}
			state.Notes = append(state.Notes, *e.Note)
		default:
			return State{}, ErrCorrupt
		}
	}
	state.Files = make([]File, 0, len(files))
	for _, f := range files {
		state.Files = append(state.Files, f)
	}
	sort.Slice(state.Files, func(i, j int) bool { return state.Files[i].ID < state.Files[j].ID })
	state.Operations = append(state.Operations, Operation{ID: r.OperationID, RequestSHA: r.RequestSHA, Revision: r.Revision})
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}
