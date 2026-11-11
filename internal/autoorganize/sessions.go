package autoorganize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/allocate"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

type sessionState struct {
	PolicyID              string                      `json:"policyID"`
	CodexParserVersion    int                         `json:"codexParserVersion,omitempty"`
	Grants                []projectmemory.SourceGrant `json:"grants"`
	Records               map[string]activity.Record  `json:"records"`
	UnresolvedObjections  map[string][]string         `json:"unresolvedObjections,omitempty"`
	ObjectionModelProblem string                      `json:"objectionModelProblem,omitempty"`
}
type ObjectionJudgment struct {
	Accepted        bool
	Provider, Model string
}
type ObjectionJudge func(context.Context, sourceevidence.Claim, Operation) (ObjectionJudgment, error)

var errObjectionJudgment = errors.New("会话反对判断未完成")

type UnassignedSession struct {
	ID             string   `json:"id"`
	Agent          string   `json:"agent"`
	Session        string   `json:"session,omitempty"`
	Count          int      `json:"count"`
	SuggestedTrade string   `json:"suggestedTrade,omitempty"`
	Examples       []string `json:"examples,omitempty"`
}

func SessionKey(r activity.Record) string {
	var meta struct {
		Session    string `json:"session"`
		SourcePath string `json:"sourcePath"`
	}
	if r.Source == "会话文件操作" {
		_ = json.Unmarshal([]byte(r.Note), &meta)
	}
	if meta.SourcePath == "" {
		meta.SourcePath = filepath.Dir(r.Path)
	}
	h := sha256.Sum256([]byte(r.Agent + "\x00" + meta.Session + "\x00" + meta.SourcePath))
	return hex.EncodeToString(h[:16])
}

type sessionGrouping struct {
	policy    Policy
	registry  *registry.Registry
	scopes    map[string]map[string]bool
	first     map[string]string
	firstLine map[string]int64
}

func newSessionGrouping(p Policy, reg *registry.Registry, records map[string]activity.Record) *sessionGrouping {
	g := &sessionGrouping{policy: p, registry: reg, scopes: map[string]map[string]bool{}, first: map[string]string{}, firstLine: map[string]int64{}}
	for _, r := range records {
		if r.Source != "会话文件操作" || !filepath.IsAbs(r.Path) || !allowed(p, filepath.Clean(r.Path)) {
			continue
		}
		id, scope := SessionKey(r), g.scope(r)
		if g.scopes[id] == nil {
			g.scopes[id] = map[string]bool{}
		}
		g.scopes[id][scope] = true
		var metadata struct {
			Line int64 `json:"line"`
		}
		_ = json.Unmarshal([]byte(r.Note), &metadata)
		previous, seen := g.first[id]
		line := g.firstLine[id]
		if !seen || metadata.Line > 0 && (line <= 0 || metadata.Line < line) || metadata.Line == line && scope < previous {
			g.first[id], g.firstLine[id] = scope, metadata.Line
		}
	}
	return g
}

func (g *sessionGrouping) scope(r activity.Record) string {
	if r.Source != "会话文件操作" {
		return ""
	}
	var metadata struct {
		WorkingDir string `json:"workingDir"`
	}
	_ = json.Unmarshal([]byte(r.Note), &metadata)
	if !filepath.IsAbs(metadata.WorkingDir) {
		return ""
	}
	path := filepath.Clean(metadata.WorkingDir)
	if project := g.registry.Owner(path); project != nil {
		return "project:" + project.ID
	}
	return "dir:" + path
}

func (g *sessionGrouping) key(r activity.Record) string {
	legacy, scope := SessionKey(r), g.scope(r)
	scoped := scopedSessionKey(legacy, scope)
	if g.policy.SessionProjects[scoped] != "" {
		return scoped
	}
	if strings.HasPrefix(scope, "project:") {
		var metadata struct {
			WorkingDir string `json:"workingDir"`
		}
		_ = json.Unmarshal([]byte(r.Note), &metadata)
		project := g.registry.Owner(metadata.WorkingDir)
		for _, path := range append([]string{metadata.WorkingDir}, project.Dirs()...) {
			previous := scopedSessionKey(legacy, "dir:"+filepath.Clean(path))
			if g.policy.SessionProjects[previous] != "" {
				return previous
			}
		}
	}
	// Legacy choices follow the earliest retained context; later contexts need their own decision.
	if g.policy.SessionProjects[legacy] != "" && g.first[legacy] == scope {
		return legacy
	}
	if len(g.scopes[legacy]) > 1 {
		return scoped
	}
	return legacy
}

func scopedSessionKey(legacy, scope string) string {
	sum := sha256.Sum256([]byte("work-context\x00" + legacy + "\x00" + scope))
	return hex.EncodeToString(sum[:16])
}

func UnassignedSessions(cfg config.Config, p Policy, records []activity.Record) []UnassignedSession {
	groups := map[string]*UnassignedSession{}
	latest := latestFileRecords(records)
	reg, _ := registry.Load(cfg)
	grouping := newSessionGrouping(p, reg, latest)
	for _, r := range latest {
		if r.Project != "" || !filepath.IsAbs(r.Path) || !allowed(p, r.Path) || within(filepath.Join(cfg.Root, cfg.Names.Control), r.Path) {
			continue
		}
		info, err := os.Lstat(r.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		id := grouping.key(r)
		if p.SessionProjects[id] != "" {
			continue
		}
		g := groups[id]
		if g == nil {
			var meta struct {
				Session string `json:"session"`
			}
			_ = json.Unmarshal([]byte(r.Note), &meta)
			g = &UnassignedSession{ID: id, Agent: r.Agent, Session: meta.Session}
			groups[id] = g
		}
		g.Count++
		g.Examples = append(g.Examples, r.Path)
		if g.Count == 1 {
			g.SuggestedTrade = r.WorkType
		} else if g.SuggestedTrade != r.WorkType {
			g.SuggestedTrade = ""
		}
	}
	var out []UnassignedSession
	for _, g := range groups {
		sort.Strings(g.Examples)
		if len(g.Examples) > 3 {
			g.Examples = g.Examples[:3]
		}
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func ReadSessionEvidence(cfg config.Config, p Policy) ([]activity.Record, []string, error) {
	data, err := os.ReadFile(filepath.Join(dir(cfg), "会话进度.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var state sessionState
	if json.Unmarshal(data, &state) != nil {
		return nil, nil, errors.New("会话读取进度无法解析")
	}
	if state.PolicyID != sessionPolicyID(p) {
		return nil, nil, nil
	}
	var records []activity.Record
	var issues []string
	for _, r := range state.Records {
		records = append(records, r)
	}
	for _, g := range state.Grants {
		if g.Problem != "" {
			issues = append(issues, g.Path+"："+g.Problem)
		} else if g.More {
			issues = append(issues, g.Path+"：正在分批读取")
		}
		if g.Skipped > 0 {
			issues = append(issues, fmt.Sprintf("%s：%d 条会话记录未映射为文件操作，可能包含尚未识别的产出。", g.Path, g.Skipped))
		}
	}
	if state.ObjectionModelProblem != "" {
		issues = append(issues, state.ObjectionModelProblem)
	}
	if len(state.UnresolvedObjections) > 0 {
		operations, err := List(cfg)
		if err != nil {
			return nil, nil, err
		}
		active := map[string]bool{}
		for _, o := range operations {
			active[o.ID] = o.Status == "organized" || o.Status == "partial"
		}
		for _, ids := range state.UnresolvedObjections {
			count := 0
			for _, id := range ids {
				if active[id] {
					count++
				}
			}
			if count > 1 {
				issues = append(issues, "一条会话反对对应多次整理，请在最近自动整理中选择要恢复的批次。")
				break
			}
		}
	}
	return records, issues, nil
}

func SessionsPending(cfg config.Config, p Policy) bool {
	data, err := os.ReadFile(filepath.Join(dir(cfg), "会话进度.json"))
	if err != nil {
		return false
	}
	var state sessionState
	if json.Unmarshal(data, &state) != nil || state.PolicyID != sessionPolicyID(p) {
		return false
	}
	for _, g := range state.Grants {
		if g.More {
			return true
		}
	}
	return false
}

func sessionPolicyID(p Policy) string {
	b, _ := json.Marshal(struct {
		Version int
		Roots   []string
		Sources []SessionSource
	}{3, p.Roots, p.Sources})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func inferProject(c sourceevidence.Claim, roots []string) string {
	if c.Project != "" {
		return c.Project
	}
	cwd := filepath.Clean(c.WorkingDir)
	if !filepath.IsAbs(cwd) {
		return projectFromFile(c.Path, roots)
	}
	best := ""
	for _, root := range roots {
		if within(root, cwd) && len(root) > len(best) {
			best = root
		}
	}
	if best != "" {
		rel, err := filepath.Rel(best, cwd)
		if err == nil && rel == "." {
			return projectFromFile(c.Path, roots)
		}
		if err == nil && rel != "." {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 1 {
				switch strings.ToLower(parts[0]) {
				case "projects", "desktop", "downloads", "documents", "work", "workspace":
					return parts[1]
				}
			}
			return parts[0]
		}
	}
	for _, root := range roots {
		if within(cwd, root) {
			return projectFromFile(c.Path, roots)
		}
	}
	return projectFromFile(c.Path, roots)
}
func projectFromFile(path string, roots []string) string {
	best := ""
	for _, root := range roots {
		if within(root, path) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return ""
	}
	rel, err := filepath.Rel(best, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return ""
	}
	switch strings.ToLower(parts[0]) {
	case "projects", "desktop", "downloads", "documents", "work", "workspace":
		if len(parts) > 2 {
			return parts[1]
		}
		return ""
	}
	return parts[0]
}
func inferWorkType(c sourceevidence.Claim, p Policy, cache map[string]string) string {
	if c.WorkType != "" {
		return c.WorkType
	}
	switch strings.ToLower(filepath.Ext(c.Path)) {
	case ".psd", ".fig", ".sketch", ".ai", ".xd":
		return "设计·UI原型"
	case ".mp4", ".mov", ".prproj", ".aep":
		return "视频剪辑·剪辑成片"
	case ".go", ".js", ".ts", ".tsx", ".py", ".rs", ".java":
		return "开发·功能开发"
	default:
		if !filepath.IsAbs(c.WorkingDir) || !allowed(p, c.WorkingDir) {
			return ""
		}
		for _, root := range p.Roots {
			if filepath.Clean(root) == filepath.Clean(c.WorkingDir) {
				return ""
			}
		}
		if result, ok := cache[c.WorkingDir]; ok {
			return result
		}
		chosen := ""
		for _, item := range allocate.InferProject(allocate.InferOptions{ProjectDir: c.WorkingDir, MaxEntries: 100}) {
			if item.Confidence != allocate.ConfHigh || item.Sensitive {
				continue
			}
			if chosen != "" && chosen != item.Label {
				chosen = ""
				break
			}
			chosen = item.Label
		}
		cache[c.WorkingDir] = chosen
		return chosen
	}
}

func discoverSessionProject(cfg config.Config, p Policy, workingDir string, reg *registry.Registry) (*registry.Project, error) {
	if !filepath.IsAbs(workingDir) || !allowed(p, workingDir) || within(cfg.Root, workingDir) {
		return nil, nil
	}
	if reg != nil {
		if existing := reg.Owner(workingDir); existing != nil {
			return existing, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var candidate *scan.Project
	for dir := filepath.Clean(workingDir); allowed(p, dir) && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if dir == home || dir == cfg.Root {
			break
		}
		switch strings.ToLower(filepath.Base(dir)) {
		case "projects", "desktop", "downloads", "documents", "work", "workspace":
			return ensureSessionProject(cfg, home, candidate)
		}
		found, e := scan.DetectProjectAt(dir)
		if e == nil && found != nil {
			candidate = found
		}
	}
	return ensureSessionProject(cfg, home, candidate)
}

func ensureSessionProject(cfg config.Config, home string, candidate *scan.Project) (*registry.Project, error) {
	if candidate == nil {
		return nil, nil
	}
	return registry.EnsureSessionProject(cfg, home, candidate)
}

// ScanSessions keeps only file-operation metadata and one checkpoint per log.
// Session contents themselves are not retained or executed.
func ScanSessions(ctx context.Context, cfg config.Config, p Policy) ([]activity.Record, error) {
	return scanSessions(ctx, cfg, p, time.Time{}, nil)
}

func ScanSessionsWithObjectionModel(ctx context.Context, cfg config.Config, p Policy, since time.Time, judge ObjectionJudge) ([]activity.Record, error) {
	return scanSessions(ctx, cfg, p, since, judge)
}

func scanSessions(ctx context.Context, cfg config.Config, p Policy, modelSince time.Time, judge ObjectionJudge) ([]activity.Record, error) {
	if len(p.Sources) == 0 {
		return nil, nil
	}
	path := filepath.Join(dir(cfg), "会话进度.json")
	state := sessionState{PolicyID: sessionPolicyID(p), Records: map[string]activity.Record{}}
	if data, err := os.ReadFile(path); err == nil {
		var old sessionState
		if json.Unmarshal(data, &old) != nil {
			return nil, errors.New("会话读取进度无法解析")
		}
		if old.PolicyID == state.PolicyID {
			state = old
			if state.Records == nil {
				state.Records = map[string]activity.Record{}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if state.CodexParserVersion < 2 {
		for i := range state.Grants {
			g := &state.Grants[i]
			if g.Adapter != "codex" {
				continue
			}
			for j := range g.Documents {
				d := &g.Documents[j]
				d.Cursor = projectmemory.SourceCursor{}
				d.Stamp, d.Attempt = "", ""
				d.More = true
			}
			g.Next, g.Skipped, g.More = 0, 0, true
		}
		state.CodexParserVersion = 2
	}
	scope := projectmemory.Scope{Roots: append([]string{}, p.Roots...)}
	tradeCache := map[string]string{}
	reg, _ := registry.Load(cfg)
	projectCache := map[string]*registry.Project{}
	var geminiDirectories map[string]string
	if len(state.Grants) == 0 {
		for _, source := range p.Sources {
			g, err := projectmemory.NewDirectorySource(source.Adapter, source.Path, scope)
			if err != nil {
				return nil, err
			}
			state.Grants = append(state.Grants, g)
		}
	}
	for i, g := range state.Grants {
		g.ObjectionsSince = p.ObjectionEnabledAt
		if judge != nil && !modelSince.IsZero() {
			g.ObjectionModelSince = modelSince.UTC().Format(time.RFC3339Nano)
		}
		updated, claims, err := projectmemory.ScanDirectorySource(ctx, g, scope)
		if err != nil {
			g.Problem = err.Error()
			state.Grants[i] = g
			continue
		}
		state.Grants[i] = updated
		for _, claim := range claims {
			if claim.Action == "objection" || claim.Action == "objection_candidate" {
				ambiguous, judged, err := queueSessionObjection(ctx, cfg, p, claim, judge)
				if errors.Is(err, errObjectionJudgment) {
					state.ObjectionModelProblem = "会话消息的模型判断未完成，请在最近自动整理中检查是否需要恢复。"
					continue
				}
				if err != nil {
					return nil, err
				}
				if judged {
					state.ObjectionModelProblem = ""
				}
				if len(ambiguous) > 1 {
					if state.UnresolvedObjections == nil {
						state.UnresolvedObjections = map[string][]string{}
					}
					state.UnresolvedObjections[claim.RecordSHA] = ambiguous
				}
				continue
			}
			if claim.Action == "delete" || !allowed(p, claim.Path) {
				continue
			}
			if previous, ok := state.Records[claim.Path]; ok && !replacesRecord(previous, activity.Record{Time: claim.ClaimedAt}) {
				continue
			}
			if claim.Agent == "gemini" && claim.WorkingDir == "" && claim.ProjectHash != "" {
				if geminiDirectories == nil {
					geminiDirectories = sourceevidence.GeminiProjectDirectories(p.Roots)
				}
				claim.WorkingDir = geminiDirectories[claim.ProjectHash]
			}
			project := inferProject(claim, p.Roots)
			if claim.Project == "" && filepath.IsAbs(claim.WorkingDir) && allowed(p, claim.WorkingDir) {
				if within(cfg.Root, claim.WorkingDir) {
					rel, _ := filepath.Rel(cfg.Root, claim.WorkingDir)
					name := strings.Split(rel, string(filepath.Separator))[0]
					if info, e := os.Lstat(filepath.Join(cfg.Root, name)); e == nil && info.IsDir() && name != cfg.Names.Control && put.CheckName("项目", name) == nil {
						project = name
					}
				}
				existing, seen := projectCache[claim.WorkingDir]
				if !seen {
					var e error
					existing, e = discoverSessionProject(cfg, p, claim.WorkingDir, reg)
					if e != nil {
						return nil, e
					}
					projectCache[claim.WorkingDir] = existing
					if existing != nil {
						reg, _ = registry.Load(cfg)
					}
				}
				if existing != nil {
					project = existing.Name
				}
			}
			metadata, _ := json.Marshal(struct {
				Session    string `json:"session"`
				RecordSHA  string `json:"recordSHA"`
				SourcePath string `json:"sourcePath"`
				Line       int64  `json:"line"`
				WorkingDir string `json:"workingDir,omitempty"`
			}{claim.Session, claim.RecordSHA, claim.SourcePath, claim.Line, claim.WorkingDir})
			state.Records[claim.Path] = activity.Record{Path: claim.Path, Agent: claim.Agent, Project: project, WorkType: inferWorkType(claim, p, tradeCache), Task: claim.Task, Time: claim.ClaimedAt, SHA256: claim.SHA256, Source: "会话文件操作", Note: string(metadata)}
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir(cfg), 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir(cfg), ".sessions-")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return nil, err
	}
	if err = os.Rename(tmp, path); err != nil {
		return nil, err
	}
	var out []activity.Record
	for _, record := range state.Records {
		out = append(out, record)
	}
	return out, nil
}

func queueSessionObjection(ctx context.Context, cfg config.Config, p Policy, claim sourceevidence.Claim, judge ObjectionJudge) ([]string, bool, error) {
	from, err := time.Parse(time.RFC3339Nano, p.ObjectionEnabledAt)
	if err != nil {
		return nil, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, claim.ClaimedAt)
	if err != nil || !at.After(from) {
		return nil, false, nil
	}
	operations, err := List(cfg)
	if err != nil {
		return nil, false, err
	}
	var matches []Operation
	for _, o := range operations {
		if (o.Status != "organized" && o.Status != "partial") || !at.After(o.CreatedAt) {
			continue
		}
		for _, signal := range o.Signals {
			if signal.Agent == claim.Agent && signal.Session == claim.Session && signal.SourcePath == claim.SourcePath && signal.Line < claim.Line {
				matches = append(matches, o)
				break
			}
		}
	}
	if claim.Message != "" && claim.Action == "objection" {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(claim.Message, "撤销"), "的整理"))
		name = strings.Trim(name, "「」『』“”\"'")
		var named []Operation
		for _, operation := range matches {
			if operation.Project == name {
				named = append(named, operation)
			}
		}
		matches = named
	} else if claim.Message != "" && len(matches) > 1 {
		var named []Operation
		for _, operation := range matches {
			if strings.Contains(claim.Message, operation.Project) {
				named = append(named, operation)
			}
		}
		if len(named) == 1 {
			matches = named
		}
	}
	judged := false
	var judgment ObjectionJudgment
	if claim.Action == "objection_candidate" && len(matches) > 0 {
		if judge == nil {
			return nil, false, nil
		}
		judged = true
		judgment, err = judge(ctx, claim, matches[0])
		if err != nil {
			return nil, true, errObjectionJudgment
		}
		if !judgment.Accepted {
			return nil, true, nil
		}
	}
	if len(matches) != 1 {
		var ids []string
		for _, o := range matches {
			ids = append(ids, o.ID)
		}
		return ids, judged, nil
	}
	_, err = RequestSessionObjection(cfg, matches[0].ID, claim, judgment.Provider, judgment.Model)
	return nil, judged, err
}
