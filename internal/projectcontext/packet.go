// Package projectcontext derives model-independent, bounded context from one
// project's authoritative memory. It does not read files or authorize network
// requests, modify memory, infer authorship or execute model advice.
package projectcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

var ErrContext = errors.New("项目上下文无法核实，请重新读取文件记录与目录规则")
var ErrBudget = errors.New("项目上下文超过本次限额，请按目录缩小分析范围；原记忆没有截断")
var ErrAdvice = errors.New("模型建议与当前项目上下文不一致，未执行任何文件操作")

const maxPacketBytes = 48 << 10

type Limits struct {
	MaxBytes int
	MaxFiles int
}

func (l Limits) normalized() (Limits, error) {
	if l.MaxBytes == 0 {
		l.MaxBytes = maxPacketBytes
	}
	if l.MaxFiles == 0 {
		l.MaxFiles = 2000
	}
	if l.MaxBytes < 512 || l.MaxBytes > maxPacketBytes || l.MaxFiles < 1 || l.MaxFiles > 2000 {
		return l, ErrBudget
	}
	return l, nil
}

type Position struct {
	SourceID string `json:"sourceID"`
	Relative string `json:"relative"`
}
type File struct {
	ID           string   `json:"id"`
	Position     Position `json:"position"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	Size         int64    `json:"size"`
	ModifiedAt   string   `json:"modifiedAt"`
	SHA256       string   `json:"sha256,omitempty"`
	HashState    string   `json:"hashState"`
	EventID      string   `json:"eventID"`
	RuleFolderID string   `json:"ruleFolderID,omitempty"`
}
type Folder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
type Note struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Agent   string `json:"agent,omitempty"`
	Model   string `json:"model,omitempty"`
	EventID string `json:"eventID"`
}
type Body struct {
	Schema              int              `json:"schema"`
	ProjectID           string           `json:"projectID"`
	ProjectName         string           `json:"projectName"`
	MemoryRevision      uint64           `json:"memoryRevision"`
	MemoryHead          string           `json:"memoryHead"`
	ScopeSHA            string           `json:"scopeSHA"`
	ObservedAt          string           `json:"observedAt"`
	ObservationComplete bool             `json:"observationComplete"`
	Coverage            map[string]int   `json:"coverage"`
	Profile             worktype.Profile `json:"profile"`
	Folders             []Folder         `json:"folders"`
	Files               []File           `json:"files"`
	Notes               []Note           `json:"notes"`
	Excluded            map[string]int   `json:"excluded"`
	SelectedIDs         []string         `json:"selectedIDs,omitempty"`
}
type Packet struct {
	SHA256 string `json:"sha256"`
	Body   Body   `json:"body"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func goodHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func text(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func id(s string) bool {
	return s != "" && len(s) <= 128 && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-:", r))
	})
}
func relative(root, path string) (string, bool) {
	r, e := filepath.Rel(root, path)
	return filepath.ToSlash(r), e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func excluded(path string) string {
	for _, p := range strings.Split(filepath.ToSlash(path), "/") {
		if scan.IsSensitiveName(p) {
			return "sensitive"
		}
		if strings.HasPrefix(p, ".") {
			return "hidden"
		}
		switch strings.ToLower(p) {
		case "node_modules", "venv", "vendor", "__pycache__", "library", "_总控":
			return "environment"
		}
	}
	return ""
}
func position(scope projectmemory.Scope, path string) (Position, bool) {
	roots := append([]string(nil), scope.Roots...)
	sort.Strings(roots)
	var out Position
	found := false
	best := -1
	for i, r := range roots {
		if rel, ok := relative(r, path); ok && len(r) > best {
			out = Position{fmt.Sprintf("root-%d", i+1), rel}
			found = true
			best = len(r)
		}
	}
	if found {
		return out, true
	}
	files := append([]string(nil), scope.Files...)
	sort.Strings(files)
	for i, p := range files {
		if p == path {
			return Position{fmt.Sprintf("item-%d", i+1), filepath.Base(path)}, true
		}
	}
	return Position{}, false
}

// selected is an explicit set chosen by the trusted controller, not model text.
// Nil means all eligible observed files. Exclusions and omitted selections are
// counted. User decisions, prior proposals and template rules are never trimmed.
// Free-form notes remain untrusted data and require outbound scope approval.
func Build(view projectmemory.View, profile worktype.Profile, selected []string, limits Limits) (Packet, error) {
	limits, err := limits.normalized()
	if err != nil {
		return Packet{}, err
	}
	body, err := buildBody(view, profile, selected)
	if err != nil {
		return Packet{}, err
	}
	return finishPacket(body, limits)
}

// Shared source for bounded outbound packets and the local range browser.
func buildBody(view projectmemory.View, profile worktype.Profile, selected []string) (Body, error) {
	s := view.State
	if !id(s.Project.ID) || s.Revision == 0 || !s.Enabled || s.LastObservedAt == "" || !goodHash(view.HeadSHA) || s.Scope.Validate() != nil || profile.Validate() != nil || !text(s.Project.Name, 256) {
		return Body{}, ErrContext
	}
	body := Body{Schema: 1, ProjectID: s.Project.ID, ProjectName: s.Project.Name, MemoryRevision: s.Revision, MemoryHead: view.HeadSHA, ScopeSHA: s.Scope.Digest(), ObservedAt: s.LastObservedAt, ObservationComplete: s.ObservationComplete, Profile: profile, Folders: []Folder{}, Files: []File{}, Notes: []Note{}, Coverage: map[string]int{}, Excluded: map[string]int{}}
	wanted := map[string]bool{}
	for _, key := range selected {
		if !id(key) || wanted[key] {
			return Body{}, ErrContext
		}
		wanted[key] = true
		body.SelectedIDs = append(body.SelectedIDs, key)
	}
	sort.Strings(body.SelectedIDs)
	for i, f := range profile.Folders {
		body.Folders = append(body.Folders, Folder{fmt.Sprintf("folder-%d", i+1), f})
	}
	for _, c := range s.Coverage {
		if !text(c.Status, 80) || c.Status == "" {
			return Body{}, ErrContext
		}
		body.Coverage[c.Status]++
	}
	seen := map[string]bool{}
	present := map[string]bool{}
	for _, f := range s.Files {
		if !id(f.ID) || seen[f.ID] {
			return Body{}, ErrContext
		}
		seen[f.ID] = true
		path := f.Fact.Path
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || !text(path, 4096) {
			return Body{}, ErrContext
		}
		pos, ok := position(s.Scope, path)
		reason := ""
		if !ok {
			reason = "out_of_scope"
		} else if f.Fact.Kind != "file" && f.Fact.Kind != "directory" {
			reason = "non_regular"
		} else {
			reason = excluded(path)
		}
		if reason != "" {
			if wanted[f.ID] {
				return Body{}, ErrContext
			}
			body.Excluded[reason]++
			continue
		}
		if len(wanted) > 0 && !wanted[f.ID] {
			body.Excluded["not_selected"]++
			continue
		}
		present[f.ID] = true
		if !id(f.LastEvent) || !text(f.Fact.ModifiedAt, 64) || f.Fact.Size < 0 {
			return Body{}, ErrContext
		}
		if f.Status != "present" && f.Status != "missing" && f.Status != "untracked" {
			return Body{}, ErrContext
		}
		if f.Fact.HashState != "verified" && f.Fact.HashState != "metadata_only" && f.Fact.HashState != "unavailable" {
			return Body{}, ErrContext
		}
		if f.Fact.SHA256 != "" && !goodHash(f.Fact.SHA256) || f.Fact.HashState == "verified" && !goodHash(f.Fact.SHA256) {
			return Body{}, ErrContext
		}
		required := ""
		if folder, _ := profile.Match(path); folder != "" {
			for _, target := range body.Folders {
				if target.Path == folder {
					required = target.ID
				}
			}
		}
		body.Files = append(body.Files, File{f.ID, pos, f.Fact.Kind, f.Status, f.Fact.Size, f.Fact.ModifiedAt, f.Fact.SHA256, f.Fact.HashState, f.LastEvent, required})
	}
	for key := range wanted {
		if !present[key] {
			return Body{}, ErrContext
		}
	}
	sort.Slice(body.Files, func(i, j int) bool { return body.Files[i].ID < body.Files[j].ID })
	notesSeen := map[string]bool{}
	for _, n := range s.Notes {
		if !id(n.ID) || !id(n.EventID) || !text(n.Text, 12000) || !text(n.Agent, 128) || !text(n.Model, 256) || (n.Kind != "user_decision" && n.Kind != "agent_proposal") || (n.Kind == "user_decision" && (n.Agent != "" || n.Model != "")) {
			return Body{}, ErrContext
		}
		if notesSeen[n.ID] {
			return Body{}, ErrContext
		}
		notesSeen[n.ID] = true
		body.Notes = append(body.Notes, Note{n.ID, n.Kind, n.Text, n.Agent, n.Model, n.EventID})
	}
	return body, nil
}

func finishPacket(body Body, limits Limits) (Packet, error) {
	if len(body.Files) > limits.MaxFiles {
		return Packet{}, ErrBudget
	}
	// JSON roundtrip detaches maps/slices from the caller's mutable view/profile.
	data, err := json.Marshal(body)
	if err != nil {
		return Packet{}, ErrContext
	}
	if len(data) > limits.MaxBytes {
		return Packet{}, ErrBudget
	}
	var detached Body
	if json.Unmarshal(data, &detached) != nil {
		return Packet{}, ErrContext
	}
	return Packet{hash(data), detached}, nil
}

func (p Packet) Bytes() ([]byte, error) {
	if p.Body.Schema != 1 || !id(p.Body.ProjectID) || p.Body.MemoryRevision == 0 || !goodHash(p.Body.MemoryHead) || !goodHash(p.Body.ScopeSHA) || p.Body.Profile.Validate() != nil {
		return nil, ErrContext
	}
	body, err := json.Marshal(p.Body)
	if err != nil || len(body) > maxPacketBytes || !goodHash(p.SHA256) || hash(body) != p.SHA256 {
		return nil, ErrContext
	}
	return json.Marshal(p)
}

// Match re-derives from authoritative state. A valid checksum alone is never
// permission, proof of fresh filesystem state or authority to move files.
func (p Packet) Match(view projectmemory.View, profile worktype.Profile) error {
	if _, err := p.Bytes(); err != nil {
		return err
	}
	current, err := Build(view, profile, p.Body.SelectedIDs, Limits{})
	if err != nil {
		return err
	}
	if current.SHA256 != p.SHA256 {
		return ErrContext
	}
	return nil
}
func Restore(data []byte) (Packet, error) {
	var p Packet
	if len(data) > maxPacketBytes+256 || strictJSON(data, &p) != nil {
		return p, ErrContext
	}
	if _, err := p.Bytes(); err != nil {
		return Packet{}, err
	}
	return p, nil
}
