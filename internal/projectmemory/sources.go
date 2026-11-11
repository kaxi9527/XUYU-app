package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

type SourceCursor struct {
	ObjectKey string                `json:"objectKey,omitempty"`
	Offset    int64                 `json:"offset,omitempty"`
	Line      int64                 `json:"line,omitempty"`
	Anchor    string                `json:"anchor,omitempty"`
	PrefixSHA string                `json:"prefixSHA,omitempty"`
	Skipping  bool                  `json:"skipping,omitempty"`
	Parser    sourceevidence.Parser `json:"parser,omitempty"`
}

type SourceGrant struct {
	GeminiProjectDirs   map[string]string `json:"-"`
	ObjectionsSince     string            `json:"-"`
	ObjectionModelSince string            `json:"-"`
	ID                  string            `json:"id"`
	Adapter             string            `json:"adapter"`
	Path                string            `json:"path"`
	Enabled             bool              `json:"enabled"`
	ScopeSHA            string            `json:"scopeSHA"`
	Cursor              SourceCursor      `json:"cursor,omitempty"`
	Problem             string            `json:"problem,omitempty"`
	LastReadAt          string            `json:"lastReadAt,omitempty"`
	Skipped             int               `json:"skipped,omitempty"`
	More                bool              `json:"more,omitempty"`
	Directory           bool              `json:"directory,omitempty"`
	Documents           []SourceDocument  `json:"documents,omitempty"`
	Next                int               `json:"next,omitempty"`
}

// Directory grants keep independent checkpoints; original JSONL bodies are never stored.
type SourceDocument struct {
	Path    string       `json:"path"`
	Cursor  SourceCursor `json:"cursor,omitempty"`
	Stamp   string       `json:"stamp,omitempty"`
	Attempt string       `json:"attempt,omitempty"`
	Problem string       `json:"problem,omitempty"`
	More    bool         `json:"more,omitempty"`
	Skipped int          `json:"skipped,omitempty"`
}

type SourceRead struct {
	ID        string           `json:"id"`
	BeforeSHA string           `json:"beforeSHA"`
	Cursor    SourceCursor     `json:"cursor"`
	Problem   string           `json:"problem,omitempty"`
	Skipped   int              `json:"skipped,omitempty"`
	More      bool             `json:"more,omitempty"`
	Reset     bool             `json:"reset,omitempty"`
	Documents []SourceDocument `json:"documents,omitempty"`
	Next      int              `json:"next,omitempty"`
}

type FileOrigin struct {
	ID         string               `json:"id"`
	SourceID   string               `json:"sourceID"`
	SourcePath string               `json:"sourcePath"`
	Claim      sourceevidence.Claim `json:"claim"`
	FileID     string               `json:"fileID,omitempty"`
	Snapshot   *Fact                `json:"snapshot,omitempty"`
	Binding    string               `json:"binding"`
	EventID    string               `json:"eventID"`
	DocumentID string               `json:"documentID,omitempty"`
}

func sourceID(adapter, path string) string {
	return "source_" + digestJSON([]string{adapter, path})[:32]
}
func grantID(adapter, path string, directory bool) string {
	if directory {
		return sourceID(adapter+":directory", path)
	}
	return sourceID(adapter, path)
}
func originSource(o FileOrigin) string {
	if o.DocumentID != "" {
		return o.DocumentID
	}
	return o.SourceID
}
func sourceContains(g SourceGrant, o FileOrigin) bool {
	if !g.Directory {
		return o.DocumentID == "" && g.Path == o.SourcePath
	}
	return sourceDocumentPath(g, o.SourcePath) && o.DocumentID == sourceID(g.Adapter, o.SourcePath)
}
func sourceDocumentPath(g SourceGrant, path string) bool {
	return pathOK(path) && len(path) <= 4096 && strings.ToLower(filepath.Ext(path)) == ".jsonl" && path != g.Path && within(g.Path, path)
}
func originID(source string, claim sourceevidence.Claim) string {
	// Project and work type enrich the same record; they do not change its identity.
	claim.Project = ""
	claim.WorkType = ""
	claim.WorkingDir = ""
	claim.ProjectHash = ""
	claim.SourcePath = ""
	return "origin_" + digestJSON(struct {
		Source string
		Claim  sourceevidence.Claim
	}{source, claim})[:32]
}

func sourceScopeAllows(scope Scope, path string) bool {
	if !pathOK(path) {
		return false
	}
	path = platformPath(path)
	for _, root := range scope.Roots {
		if within(platformPath(root), path) {
			return true
		}
	}
	for _, file := range scope.Files {
		if platformPath(file) == path {
			return true
		}
	}
	return false
}
func (g SourceGrant) validate() error {
	if g.ID != grantID(g.Adapter, g.Path, g.Directory) || !sourceevidence.Supported(g.Adapter) || !pathOK(g.Path) || len(g.Path) > 4096 || !g.Directory && strings.ToLower(filepath.Ext(g.Path)) != ".jsonl" || !hashOK(g.ScopeSHA) || len(g.Problem) > 1000 || g.Skipped < 0 {
		return ErrCorrupt
	}
	if !validSourceCursor(g.Cursor) || g.Next < 0 || g.Next > 4096 || len(g.Documents) > 4096 || !g.Directory && (len(g.Documents) > 0 || g.Next != 0) {
		return ErrCorrupt
	}
	seen := map[string]bool{}
	for _, d := range g.Documents {
		if !sourceDocumentPath(g, d.Path) || seen[d.Path] || !validSourceCursor(d.Cursor) || len(d.Problem) > 1000 || d.Stamp != "" && !hashOK(d.Stamp) || d.Attempt != "" && !hashOK(d.Attempt) || d.Skipped < 0 {
			return ErrCorrupt
		}
		seen[d.Path] = true
	}
	if g.LastReadAt != "" && !timeOK(g.LastReadAt) {
		return ErrCorrupt
	}
	return nil
}
func validSourceCursor(c SourceCursor) bool {
	return c.Offset >= 0 && c.Line >= 0 && len(c.ObjectKey) <= 512 && (c.Anchor == "" || hashOK(c.Anchor)) && (c.PrefixSHA == "" || hashOK(c.PrefixSHA)) && len(c.Parser.Pending) <= 32
}

func (s *Store) SetSource(ctx context.Context, revision uint64, operationID, scopeSHA, adapter, path string, enabled bool, now time.Time) (Result, error) {
	return s.SetSourceLocation(ctx, revision, operationID, scopeSHA, adapter, path, false, enabled, now)
}
func (s *Store) SetSourceLocation(ctx context.Context, revision uint64, operationID, scopeSHA, adapter, path string, directory, enabled bool, now time.Time) (Result, error) {
	if !sourceevidence.Supported(adapter) || !pathOK(path) || !directory && strings.ToLower(filepath.Ext(path)) != ".jsonl" || len(path) > 4096 || now.IsZero() {
		return Result{}, errors.New("请选择支持的来源格式和完整 JSONL 文件路径")
	}
	intent := struct {
		Scope, Adapter, Path string
		Enabled              bool
		Directory            bool `json:",omitempty"`
	}{scopeSHA, adapter, path, enabled, directory}
	return s.update(ctx, revision, operationID, "source_grant", intent, func(previous State) ([]Event, error) {
		if previous.Revision == 0 || enabled && !previous.Enabled || previous.Scope.Digest() != scopeSHA {
			return nil, ErrConflict
		}
		g := SourceGrant{ID: grantID(adapter, path, directory), Adapter: adapter, Path: path, Enabled: enabled, ScopeSHA: scopeSHA, Directory: directory}
		found := false
		for _, old := range previous.Sources {
			if old.ID == g.ID {
				found = true
				if old.ScopeSHA == scopeSHA {
					g = old
					g.Enabled = enabled
				}
				break
			}
		}
		if !found && (len(previous.Sources) >= 16 || !enabled) {
			return nil, errors.New("来源未登记或数量已达本项目上限")
		}
		reason := "用户停止读取这个来源；已有文件来源证据保留"
		if enabled {
			reason = "用户授权只读此 JSONL 的结构化文件操作；仅归档本项目范围内的来源，不执行日志指令或外发内容"
			if directory {
				reason = "用户授权只读此会话目录内现有和新增 JSONL 的结构化文件操作；仅归档本项目范围，不跟随链接、不执行日志指令或外发正文"
			}
		}
		return []Event{{Kind: "source_grant_changed", ObservedAt: stamp(now), Source: "local_user", Actor: "local_user", Reason: reason, SourceGrant: &g}}, nil
	})
}

func validateSources(s State) error {
	if len(s.Sources) > 16 || len(s.Origins) > 10000 {
		return ErrBudget
	}
	grants := map[string]SourceGrant{}
	for _, g := range s.Sources {
		if g.validate() != nil {
			return ErrCorrupt
		}
		if _, ok := grants[g.ID]; ok {
			return ErrCorrupt
		}
		grants[g.ID] = g
	}
	seen := map[string]bool{}
	fileIDs := map[string]bool{}
	for _, f := range s.Files {
		fileIDs[f.ID] = true
	}
	for _, o := range s.Origins {
		g, ok := grants[o.SourceID]
		if !ok || !sourceContains(g, o) || !validOrigin(o) || seen[o.ID] || o.FileID != "" && !fileIDs[o.FileID] {
			return ErrCorrupt
		}
		seen[o.ID] = true
	}
	return nil
}
func validOrigin(o FileOrigin) bool {
	c := o.Claim
	if o.ID != originID(originSource(o), c) || !idOK(o.SourceID) || o.DocumentID != "" && !idOK(o.DocumentID) || !pathOK(o.SourcePath) || !pathOK(c.Path) || !hashOK(c.RecordSHA) || c.Line <= 0 || len(c.Agent) == 0 || len(c.Agent) > 128 || len(c.Project) > 256 || len(c.WorkType) > 256 || len(c.Session) > 256 || len(c.Task) > 256 || len(c.Call) > 256 || len(c.ClaimedAt) > 64 || c.RequestLine < 0 {
		return false
	}
	if c.BeforePath != "" && !pathOK(c.BeforePath) || c.SHA256 != "" && !hashOK(c.SHA256) || c.RequestSHA != "" && !hashOK(c.RequestSHA) {
		return false
	}
	if !contains([]string{"write", "edit", "remove", "rename", "mark"}, c.Action) || !contains([]string{"tool_result", "self_report"}, c.Basis) || !contains([]string{"snapshot_match", "snapshot_differs", "path_only", "not_observed"}, o.Binding) {
		return false
	}
	if o.FileID != "" && (!idOK(o.FileID) || o.Snapshot == nil) || o.FileID == "" && o.Snapshot != nil {
		return false
	}
	if o.Binding == "not_observed" {
		return o.FileID == ""
	}
	if o.Snapshot == nil || !pathOK(o.Snapshot.Path) {
		return false
	}
	if o.Binding == "snapshot_match" || o.Binding == "snapshot_differs" {
		if c.SHA256 == "" || o.Snapshot.HashState != "verified" || !hashOK(o.Snapshot.SHA256) {
			return false
		}
		if (c.SHA256 == o.Snapshot.SHA256) != (o.Binding == "snapshot_match") {
			return false
		}
	}
	return true
}

func applySourceEvent(state *State, e Event) error {
	if e.Kind == "source_grant_changed" {
		g := e.SourceGrant
		if e.Source != "local_user" || e.Actor != "local_user" || g == nil || g.validate() != nil || g.ScopeSHA != state.Scope.Digest() || g.Enabled && !state.Enabled {
			return ErrCorrupt
		}
		for i, old := range state.Sources {
			if old.ID == g.ID {
				expected := old
				expected.Enabled = g.Enabled
				if old.ScopeSHA != g.ScopeSHA {
					expected = SourceGrant{ID: g.ID, Adapter: g.Adapter, Path: g.Path, Enabled: g.Enabled, ScopeSHA: g.ScopeSHA, Directory: g.Directory}
				}
				if digestJSON(expected) != digestJSON(*g) {
					return ErrCorrupt
				}
				state.Sources[i] = *g
				return nil
			}
		}
		initial := SourceGrant{ID: g.ID, Adapter: g.Adapter, Path: g.Path, Enabled: g.Enabled, ScopeSHA: g.ScopeSHA, Directory: g.Directory}
		if !g.Enabled || digestJSON(initial) != digestJSON(*g) {
			return ErrCorrupt
		}
		if len(state.Sources) >= 16 {
			return ErrBudget
		}
		state.Sources = append(state.Sources, *g)
		return nil
	}
	if e.Kind == "source_checked" {
		r := e.SourceRead
		if e.Source != "filesystem_observation" || e.Actor != "unknown" || r == nil || !state.Enabled {
			return ErrCorrupt
		}
		for i, g := range state.Sources {
			if g.ID == r.ID {
				if !g.Enabled || g.ScopeSHA != state.Scope.Digest() || digestJSON(g) != r.BeforeSHA {
					return ErrCorrupt
				}
				g.Cursor = r.Cursor
				if err := mergeSourceDocuments(&g, r.Documents, r.Next); err != nil {
					return err
				}
				g.Problem = r.Problem
				g.Skipped += r.Skipped
				g.More = r.More
				g.LastReadAt = e.ObservedAt
				if g.validate() != nil {
					return ErrCorrupt
				}
				state.Sources[i] = g
				return nil
			}
		}
		return ErrCorrupt
	}
	o := e.Origin
	if o == nil || e.Source != "agent_declaration" || e.Actor != "unverified" || o.EventID != e.ID || !validOrigin(*o) || !state.Enabled {
		return ErrCorrupt
	}
	for _, old := range state.Origins {
		if old.ID == o.ID {
			return ErrCorrupt
		}
	}
	for _, g := range state.Sources {
		if g.ID == o.SourceID && g.Enabled && g.ScopeSHA == state.Scope.Digest() && sourceContains(g, *o) && (sourceScopeAllows(state.Scope, o.Claim.Path) || o.Claim.BeforePath != "" && sourceScopeAllows(state.Scope, o.Claim.BeforePath)) {
			state.Origins = append(state.Origins, *o)
			return nil
		}
	}
	return ErrCorrupt
}

func (s *Store) sourceEvents(ctx context.Context, previous State, observed []Event) ([]Event, error) {
	files := map[string]File{}
	for _, f := range previous.Files {
		files[f.ID] = f
	}
	for _, e := range observed {
		if e.After != nil {
			files[e.After.ID] = *e.After
		}
	}
	byPath := map[string]File{}
	for _, f := range files {
		if f.Status != "untracked" {
			byPath[platformPath(f.Fact.Path)] = f
		}
	}
	seen := map[string]bool{}
	for _, o := range previous.Origins {
		seen[o.ID] = true
	}
	out := append([]Event{}, observed...)
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	grants := append([]SourceGrant{}, previous.Sources...)
	sort.SliceStable(grants, func(i, j int) bool { return grants[i].LastReadAt < grants[j].LastReadAt })
	for _, g := range grants {
		if !g.Enabled || g.ScopeSHA != previous.Scope.Digest() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if bounded.Err() != nil {
			break
		}
		read, claims, err := readGrantedSource(bounded, g, previous.Scope)
		if err != nil {
			problem := err.Error()
			if bounded.Err() != nil {
				problem = "来源读取达到本轮时间预算；保留进度，下次继续"
			}
			read = SourceRead{ID: g.ID, BeforeSHA: digestJSON(g), Cursor: g.Cursor, Problem: problem, Next: g.Next}
		}
		// Pending calls contain only file-operation metadata. Do not copy other
		// projects' artifact paths into this project's persistent parser state.
		if err == nil {
			for id, pending := range read.Cursor.Parser.Pending {
				var allowed []sourceevidence.Claim
				for _, c := range pending.Claims {
					if sourceScopeAllows(previous.Scope, c.Path) || c.BeforePath != "" && sourceScopeAllows(previous.Scope, c.BeforePath) {
						allowed = append(allowed, c)
					}
				}
				if len(allowed) == 0 {
					delete(read.Cursor.Parser.Pending, id)
				} else {
					pending.Claims = allowed
					read.Cursor.Parser.Pending[id] = pending
				}
			}
		}
		changed := digestJSON(read.Cursor) != digestJSON(g.Cursor) || read.Problem != g.Problem || read.More != g.More || read.Skipped > 0 || len(read.Documents) > 0 || read.Next != g.Next
		if !changed {
			continue
		}
		now := stamp(time.Now())
		reason := "按独立来源授权检查结构化记录；未保存聊天正文，也未执行记录中的命令"
		if read.Reset {
			reason += "；检测到记录替换或检查点变化，从头核对，旧来源历史保留"
		}
		out = append(out, Event{Kind: "source_checked", ObservedAt: now, Source: "filesystem_observation", Actor: "unknown", Reason: reason, SourceRead: &read})
		for _, located := range claims {
			c := located.Claim
			c.Path = platformPath(c.Path)
			if c.BeforePath != "" {
				c.BeforePath = platformPath(c.BeforePath)
			}
			if !sourceScopeAllows(previous.Scope, c.Path) && (c.BeforePath == "" || !sourceScopeAllows(previous.Scope, c.BeforePath)) {
				continue
			}
			id := originID(sourceID(g.Adapter, located.Path), c)
			if seen[id] {
				continue
			}
			seen[id] = true
			if len(seen) > 10000 {
				return nil, ErrBudget
			}
			o := FileOrigin{ID: id, SourceID: g.ID, SourcePath: located.Path, Claim: c, Binding: "not_observed"}
			if g.Directory {
				o.DocumentID = sourceID(g.Adapter, located.Path)
			}
			f, ok := byPath[c.Path]
			if !ok && c.BeforePath != "" {
				f, ok = byPath[c.BeforePath]
			}
			if ok {
				fact := f.Fact
				o.FileID = f.ID
				o.Snapshot = &fact
				o.Binding = "path_only"
				if c.SHA256 != "" && fact.HashState == "verified" && f.Status == "present" {
					o.Binding = "snapshot_differs"
					if c.SHA256 == fact.SHA256 {
						o.Binding = "snapshot_match"
					}
				}
			}
			out = append(out, Event{Kind: "file_source_recorded", ObservedAt: now, Source: "agent_declaration", Actor: "unverified", Reason: "来源记录与本项目文件快照关联；记录可由外部修改，内容匹配不等于作者身份已证明", Origin: &o})
		}
	}
	return out, nil
}

func cloneSourceCursor(cursor SourceCursor) SourceCursor {
	data, _ := json.Marshal(cursor)
	var copy SourceCursor
	_ = json.Unmarshal(data, &copy)
	return copy
}
