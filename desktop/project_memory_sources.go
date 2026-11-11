package main

import (
	"context"
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

type ProjectMemorySource struct {
	ID           string `json:"id"`
	Adapter      string `json:"adapter"`
	Path         string `json:"path"`
	Enabled      bool   `json:"enabled"`
	ScopeChanged bool   `json:"scopeChanged"`
	LastReadAt   string `json:"lastReadAt"`
	Line         int64  `json:"line"`
	Skipped      int    `json:"skipped"`
	More         bool   `json:"more"`
	Problem      string `json:"problem"`
	Origins      int    `json:"origins"`
	Directory    bool   `json:"directory,omitempty"`
	Records      int    `json:"records,omitempty"`
	ReadRecords  int    `json:"readRecords,omitempty"`
	Unreadable   int    `json:"unreadable,omitempty"`
}

func (s *ShelfService) setProjectMemorySource(in MemoryAction) (projectmemory.Result, error) {
	finish := s.holdProjectMemory(in.ProjectKey)
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, in.ProjectKey)
	if err != nil {
		return projectmemory.Result{}, err
	}
	if err = memoryRequestOK(in.OperationID, in.ScopeSHA, e.tracking); err != nil {
		return projectmemory.Result{}, err
	}
	if e.store == nil {
		return projectmemory.Result{}, errors.New("请先开启此项目的文件记录")
	}
	return e.store.SetSourceLocation(ctx, in.Revision, in.OperationID, in.ScopeSHA, in.SourceAdapter, in.SourcePath, in.SourceDirectory, in.Kind == "source_on", time.Now())
}

func projectMemorySources(state projectmemory.State, currentScope string) []ProjectMemorySource {
	out := []ProjectMemorySource{}
	counts := map[string]int{}
	for _, o := range state.Origins {
		counts[o.SourceID]++
	}
	for _, g := range state.Sources {
		item := ProjectMemorySource{ID: g.ID, Adapter: g.Adapter, Path: g.Path, Enabled: g.Enabled, ScopeChanged: g.ScopeSHA != currentScope, LastReadAt: g.LastReadAt, Line: g.Cursor.Line, Skipped: g.Skipped, More: g.More, Problem: g.Problem, Origins: counts[g.ID], Directory: g.Directory, Records: len(g.Documents)}
		for _, d := range g.Documents {
			if d.Cursor.ObjectKey != "" {
				item.ReadRecords++
			}
			if d.Problem != "" {
				item.Unreadable++
			}
			item.Line += d.Cursor.Line
		}
		out = append(out, item)
	}
	return out
}
