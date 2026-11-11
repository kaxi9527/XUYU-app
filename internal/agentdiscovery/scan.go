// Package agentdiscovery observes local installation clues. It never launches
// clients, reads conversations or credentials, or installs rules as a side effect.
package agentdiscovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agents"
)

const maxRuleBytes = 256 << 10

type Evidence struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`   // directory, application, rule
	Status string `json:"status"` // observed, missing, rule_present, needs_rule, review_needed, inaccessible
	SHA256 string `json:"sha256,omitempty"`
}

type Candidate struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"` // tool_candidate or rule_candidate, never confirmed execution identity
	Status    string     `json:"status"`
	Evidence  []Evidence `json:"evidence"`
	FirstSeen time.Time  `json:"firstSeen"`
	LastSeen  time.Time  `json:"lastSeen"`
}

type Gap struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

type Scan struct {
	Home       string      `json:"home"`
	StartedAt  time.Time   `json:"startedAt"`
	Candidates []Candidate `json:"candidates"`
	Gaps       []Gap       `json:"gaps"`
	Roots      []string    `json:"roots"`
	Coverage   string      `json:"coverage"`
}

type Scanner struct {
	Home             string
	ApplicationRoots []string
	MaxHomeEntries   int
}

type signature struct {
	id, name          string
	dirs, rules, apps []string
}

// These are discovery hints, not a claim that each client loads these files.
// Rule delivery must separately verify its supported entry point (WS-21b).
var signatures = []signature{
	{"codex", "Codex", []string{".codex"}, []string{".codex/AGENTS.md"}, []string{"Codex.app"}},
	{"claude", "Claude", []string{".claude"}, []string{".claude/CLAUDE.md", "CLAUDE.md"}, []string{"Claude.app"}},
	{"gemini", "Gemini", []string{".gemini"}, []string{".gemini/GEMINI.md", "GEMINI.md"}, nil},
	{"cursor", "Cursor", []string{".cursor"}, []string{".cursorrules", ".cursor/rules/workshelf-identity.mdc"}, []string{"Cursor.app"}},
	{"windsurf", "Windsurf", []string{".windsurf", ".codeium/windsurf"}, []string{".windsurfrules", ".windsurf/AGENTS.md"}, []string{"Windsurf.app"}},
	{"copilot", "Copilot", []string{".copilot"}, []string{".copilot/copilot-instructions.md"}, nil},
	{"workbuddy", "WorkBuddy", []string{".workbuddy"}, []string{".workbuddy/AGENTS.md"}, []string{"WorkBuddy.app"}},
	{"openclaw", "OpenClaw", []string{".openclaw", "clawd"}, []string{".openclaw/workspace/SOUL.md", "clawd/SOUL.md"}, nil},
	{"grok", "Grok Bot", []string{".grokbot"}, []string{".grokbot/AGENTS.md"}, nil},
	{"hermes", "Hermes", []string{".hermes"}, []string{".hermes/AGENTS.md"}, nil},
	{"qwen", "Qwen", []string{".qwen"}, []string{".qwen/AGENTS.md", ".qwen/QWEN.md"}, nil},
	{"kimi", "Kimi", []string{".kimi"}, []string{".kimi/AGENTS.md"}, nil},
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (s Scanner) Run(ctx context.Context, now time.Time) (Scan, error) {
	if !filepath.IsAbs(s.Home) || now.IsZero() {
		return Scan{}, errors.New("扫描需要完整主目录和检查时间")
	}
	home, err := filepath.EvalSymlinks(s.Home)
	if err != nil {
		return Scan{}, err
	}
	s.Home = home
	out := Scan{Home: home, StartedAt: now.UTC(), Candidates: []Candidate{}, Gaps: []Gap{}, Roots: []string{home}, Coverage: "known_signatures_and_top_level_rule_candidates"}
	for _, root := range s.ApplicationRoots {
		if !filepath.IsAbs(root) {
			return Scan{}, errors.New("应用扫描范围需要完整路径")
		}
		out.Roots = append(out.Roots, filepath.Clean(root))
	}
	if len(out.Roots) > 9 {
		return Scan{}, errors.New("应用扫描范围过多")
	}
	bytesRead, rulesRead := 0, 0
	probe := func(path, kind, agent string) Evidence {
		e := Evidence{Path: path, Kind: kind, Status: "missing"}
		fi, err := noLinkStat(path)
		if errors.Is(err, os.ErrNotExist) {
			return e
		}
		if err != nil {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "unreadable_or_link"})
			return e
		}
		if kind != "rule" {
			if fi.IsDir() {
				e.Status = "observed"
			} else {
				e.Status = "review_needed"
			}
			return e
		}
		if !fi.Mode().IsRegular() || fi.Size() > maxRuleBytes {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "rule_type_or_size"})
			return e
		}
		if rulesRead >= 256 || bytesRead+int(fi.Size()) > 8<<20 {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "rule_read_budget"})
			return e
		}
		rulesRead++
		f, err := os.Open(path)
		if err != nil {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "rule_unreadable"})
			return e
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(fi, opened) {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "rule_changed"})
			return e
		}
		b, err := io.ReadAll(io.LimitReader(f, maxRuleBytes+1))
		bytesRead += len(b)
		after, statErr := f.Stat()
		current, pathErr := noLinkStat(path)
		if err != nil || statErr != nil || pathErr != nil || len(b) > maxRuleBytes || !os.SameFile(fi, current) || after.Size() != fi.Size() || !after.ModTime().Equal(fi.ModTime()) {
			e.Status = "inaccessible"
			out.Gaps = append(out.Gaps, Gap{path, "rule_changed_or_unreadable"})
			return e
		}
		e.SHA256 = hash(b)
		e.Status = "needs_rule"
		if agent != "" && bytes.Contains(b, []byte(agents.Block(agent))) && bytes.Count(b, []byte(agents.Begin)) == 1 && bytes.Count(b, []byte(agents.End)) == 1 {
			e.Status = "rule_present"
		} else if bytes.Contains(b, []byte(agents.Begin)) || bytes.Contains(b, []byte(agents.End)) {
			e.Status = "review_needed"
		}
		return e
	}
	add := func(c Candidate) {
		present, good, uncertain := false, false, false
		for _, e := range c.Evidence {
			present = present || e.Status != "missing"
			good = good || e.Status == "rule_present"
			uncertain = uncertain || e.Status == "inaccessible" || e.Status == "review_needed"
		}
		if !present {
			return
		}
		c.Status = "needs_rule"
		if good {
			c.Status = "rule_present"
		}
		if uncertain || c.Kind == "rule_candidate" {
			c.Status = "review_needed"
		}
		c.FirstSeen, c.LastSeen = now.UTC(), now.UTC()
		out.Candidates = append(out.Candidates, c)
	}
	known := map[string]bool{".agents": true, ".git": true, ".Trash": true, ".cache": true, ".npm": true, ".local": true}
	for _, sig := range signatures {
		if err := ctx.Err(); err != nil {
			return Scan{}, err
		}
		c := Candidate{ID: sig.id, Name: sig.name, Kind: "tool_candidate", Evidence: []Evidence{}}
		for _, path := range sig.dirs {
			known[strings.Split(path, "/")[0]] = true
			c.Evidence = append(c.Evidence, probe(filepath.Join(home, path), "directory", sig.id))
		}
		for _, path := range sig.rules {
			c.Evidence = append(c.Evidence, probe(filepath.Join(home, path), "rule", sig.id))
		}
		for _, root := range s.ApplicationRoots {
			for _, name := range sig.apps {
				c.Evidence = append(c.Evidence, probe(filepath.Join(root, name), "application", sig.id))
			}
		}
		add(c)
	}
	limit := s.MaxHomeEntries
	if limit <= 0 {
		limit = 2048
	}
	if limit > 4096 {
		return Scan{}, errors.New("扫描条目预算过大")
	}
	f, err := os.Open(home)
	if err != nil {
		return Scan{}, err
	}
	entries, err := f.ReadDir(limit + 1)
	f.Close()
	if err != nil && err != io.EOF {
		out.Gaps = append(out.Gaps, Gap{home, "home_list_incomplete"})
	}
	if len(entries) > limit {
		entries = entries[:limit]
		out.Gaps = append(out.Gaps, Gap{home, "home_entry_budget"})
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Scan{}, err
		}
		if e.Type()&os.ModeSymlink != 0 && strings.HasPrefix(e.Name(), ".") && !known[e.Name()] {
			out.Gaps = append(out.Gaps, Gap{filepath.Join(home, e.Name()), "top_level_link_not_followed"})
			continue
		}
		if len(out.Candidates) >= 256 {
			out.Gaps = append(out.Gaps, Gap{home, "candidate_budget"})
			break
		}
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".") || known[e.Name()] {
			continue
		}
		root := filepath.Join(home, e.Name())
		c := Candidate{ID: "candidate_" + hash([]byte(root))[:24], Name: e.Name(), Kind: "rule_candidate", Evidence: []Evidence{}}
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", "SOUL.md", "IDENTITY.md"} {
			ev := probe(filepath.Join(root, name), "rule", "")
			if ev.Status != "missing" {
				c.Evidence = append(c.Evidence, ev)
			}
		}
		add(c)
	}
	// A shared rules file is not evidence of a specific installed agent.
	ev := probe(filepath.Join(home, "AGENTS.md"), "rule", "")
	if ev.Status != "missing" {
		add(Candidate{ID: "shared_rules", Name: "通用规则入口", Kind: "rule_candidate", Evidence: []Evidence{ev}})
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].ID < out.Candidates[j].ID })
	sort.Slice(out.Gaps, func(i, j int) bool { return out.Gaps[i].Path < out.Gaps[j].Path })
	return out, nil
}

// Refuse symlinks in every component; discovering one is a coverage gap.
func noLinkStat(path string) (os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("需要完整路径")
	}
	parts := []string{}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		parts = append(parts, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	var fi os.FileInfo
	for i := len(parts) - 1; i >= 0; i-- {
		var err error
		fi, err = os.Lstat(parts[i])
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("路径含符号链接")
		}
	}
	return fi, nil
}
