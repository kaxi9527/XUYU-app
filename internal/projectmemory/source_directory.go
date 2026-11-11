package projectmemory

import (
	"context"
	"sort"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

const maxSourceDocuments = 4096
const sourceDocumentsPerRound = 32

type sourceCandidate struct {
	Path, Stamp string
	Modified    int64
}
type sourceClaim struct {
	Path  string
	Claim sourceevidence.Claim
}

func mergeSourceDocuments(g *SourceGrant, updates []SourceDocument, next int) error {
	if !g.Directory {
		if len(updates) > 0 || next != 0 {
			return ErrCorrupt
		}
		return nil
	}
	if len(updates) > maxSourceDocuments || next < 0 || next > maxSourceDocuments {
		return ErrBudget
	}
	documents := append([]SourceDocument{}, g.Documents...)
	indexes := map[string]int{}
	seen := map[string]bool{}
	for i, d := range documents {
		indexes[d.Path] = i
	}
	for _, d := range updates {
		if !sourceDocumentPath(*g, d.Path) || seen[d.Path] {
			return ErrCorrupt
		}
		seen[d.Path] = true
		if i, ok := indexes[d.Path]; ok {
			documents[i] = d
		} else {
			indexes[d.Path] = len(documents)
			documents = append(documents, d)
		}
	}
	if len(documents) > maxSourceDocuments {
		return ErrBudget
	}
	g.Documents = documents
	g.Next = next
	return nil
}

func sourcePendingScope(cursor *SourceCursor, scope Scope) {
	for id, pending := range cursor.Parser.Pending {
		var allowed []sourceevidence.Claim
		for _, c := range pending.Claims {
			if sourceScopeAllows(scope, c.Path) || c.BeforePath != "" && sourceScopeAllows(scope, c.BeforePath) {
				allowed = append(allowed, c)
			}
		}
		if len(allowed) == 0 {
			delete(cursor.Parser.Pending, id)
		} else {
			pending.Claims = allowed
			cursor.Parser.Pending[id] = pending
		}
	}
}

func readGrantedSource(ctx context.Context, g SourceGrant, scope Scope) (SourceRead, []sourceClaim, error) {
	if g.Adapter == "gemini" {
		g.GeminiProjectDirs = sourceevidence.GeminiProjectDirectories(scope.Roots)
	}
	if !g.Directory {
		r, claims, err := readSource(ctx, g)
		if err != nil {
			return r, nil, err
		}
		sourcePendingScope(&r.Cursor, scope)
		var out []sourceClaim
		for _, c := range claims {
			out = append(out, sourceClaim{g.Path, c})
		}
		return r, out, nil
	}
	r := SourceRead{ID: g.ID, BeforeSHA: digestJSON(g), Next: g.Next}
	candidates, gaps, err := discoverSourceDirectory(ctx, g.Path)
	if err != nil {
		return r, nil, err
	}
	documents := map[string]SourceDocument{}
	updates := map[string]SourceDocument{}
	for _, d := range g.Documents {
		documents[d.Path] = d
	}
	// Newest changed sessions first; unchanged records are also rechecked in a bounded rotation.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Modified != candidates[j].Modified {
			return candidates[i].Modified > candidates[j].Modified
		}
		return candidates[i].Path < candidates[j].Path
	})
	known := map[string]bool{}
	var priority, stable []sourceCandidate
	for _, c := range candidates {
		known[c.Path] = true
		d, ok := documents[c.Path]
		if !ok {
			if len(documents) >= maxSourceDocuments {
				gaps = append(gaps, "来源记录数量达到上限，部分新增记录尚未纳入")
				break
			}
			d = SourceDocument{Path: c.Path}
			documents[c.Path] = d
			updates[c.Path] = d
		}
		if !ok || d.Stamp != c.Stamp && d.Attempt != c.Stamp || d.More {
			priority = append(priority, c)
		} else {
			stable = append(stable, c)
		}
	}
	// A missing path retains its checkpoint and history. It is never interpreted as a deletion of the artifact.
	for path, d := range documents {
		if !known[path] && d.Problem == "" && len(gaps) == 0 {
			d.Problem = "来源记录暂未找到，历史与读取进度保留"
			updates[path] = d
		}
	}
	sort.Slice(stable, func(i, j int) bool { return stable[i].Path < stable[j].Path })
	if len(stable) > 0 {
		start := g.Next % len(stable)
		stable = append(append([]sourceCandidate{}, stable[start:]...), stable[:start]...)
	}
	priorityCount := len(priority)
	maxPriority := sourceDocumentsPerRound
	if len(stable) > 0 {
		maxPriority -= 8
	}
	if priorityCount > maxPriority {
		priorityCount = maxPriority
	}
	selected := append(append([]sourceCandidate{}, priority[:priorityCount]...), stable...)
	if len(selected) > sourceDocumentsPerRound {
		selected = selected[:sourceDocumentsPerRound]
	}
	r.More = len(priority) > priorityCount
	var claims []sourceClaim
	stableRead := 0
	for _, c := range selected {
		if ctx.Err() != nil {
			r.More = true
			gaps = append(gaps, "来源读取达到本轮时间预算，将继续检查剩余记录")
			break
		}
		d := documents[c.Path]
		d.Attempt = c.Stamp
		read, found, e := readSource(ctx, SourceGrant{ID: sourceID(g.Adapter, c.Path), Adapter: g.Adapter, Path: c.Path, Cursor: d.Cursor, ObjectionsSince: g.ObjectionsSince, ObjectionModelSince: g.ObjectionModelSince, GeminiProjectDirs: g.GeminiProjectDirs})
		if e != nil {
			d.Problem = "记录暂不可读或正在变化，保留进度"
			d.More = false
		} else {
			sourcePendingScope(&read.Cursor, scope)
			d.Cursor = read.Cursor
			d.Stamp = c.Stamp
			d.Problem = ""
			d.More = read.More
			d.Skipped += read.Skipped
			r.Skipped += read.Skipped
			r.Reset = r.Reset || read.Reset
			for _, claim := range found {
				claims = append(claims, sourceClaim{c.Path, claim})
			}
		}
		r.More = r.More || d.More
		if digestJSON(d) != digestJSON(documents[c.Path]) {
			updates[c.Path] = d
		}
		if documents[c.Path].Stamp == c.Stamp || documents[c.Path].Attempt == c.Stamp {
			if !documents[c.Path].More {
				stableRead++
			}
		}
	}
	if len(stable) > 0 {
		r.Next = (g.Next + stableRead) % len(stable)
	} else {
		r.Next = 0
	}
	paths := make([]string, 0, len(updates))
	for path := range updates {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		r.Documents = append(r.Documents, updates[path])
	}
	if len(gaps) > 0 {
		r.Problem = strings.Join(uniqueSourceGaps(gaps), "；")
	}
	return r, claims, nil
}

func uniqueSourceGaps(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
		if len(out) == 4 {
			break
		}
	}
	return out
}
