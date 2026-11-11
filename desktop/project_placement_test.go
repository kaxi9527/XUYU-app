//go:build darwin && cgo

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func placementServiceFixture(t *testing.T) (*ShelfService, string, projectmemory.AnalysisRequest) {
	t.Helper()
	s, project, _ := analysisServiceFixture(t, nil)
	t.Cleanup(s.closePlacements)
	p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "placement-analysis", true, false); err != nil {
		t.Fatal(err)
	}
	r := waitAnalysis(t, s, "placement-analysis", true)
	if r.Status != "succeeded" {
		t.Fatal(r.Status)
	}
	return s, project, r
}

func TestProjectPlacementServicePreviewConfirmExecuteRestartUndo(t *testing.T) {
	s, project, r := placementServiceFixture(t)
	key := "root:品牌项目"
	p, err := s.PreviewProjectPlacement(key, "single", r.ID)
	if err != nil {
		t.Fatal("preview", err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Action != "move" {
		t.Fatal("missing grouping", p.Rows)
	}
	row := p.Rows[0]
	if data, err := os.ReadFile(row.Source); err != nil || string(data) != "DO_NOT_SEND_FILE_CONTENT" {
		t.Fatal("preview moved file", err)
	}
	if _, err = s.ConfirmProjectPlacement("root:另一个项目", p.Token, "place-one", []string{row.ID}); err == nil {
		t.Fatal("foreign project token accepted")
	}
	op, err := s.ConfirmProjectPlacement(key, p.Token, "place-one", []string{row.ID})
	if err != nil {
		t.Fatal("confirm", err)
	}
	if op.Status != "confirmed" {
		t.Fatal(op.Status)
	}
	if _, err = os.Lstat(row.Source); err != nil {
		t.Fatal("confirmation moved file", err)
	}
	op, err = s.ExecuteProjectPlacement(key, op.ID, op.IntentSHA)
	if err != nil || op.Status != "completed" || op.Problem != "" {
		t.Fatal("execute", op, err)
	}
	if _, err = os.Lstat(row.Source); !os.IsNotExist(err) {
		t.Fatal("source not moved")
	}
	if _, err = os.Lstat(filepath.Join(project, "参考资料", "说明.txt")); err != nil {
		t.Fatal(err)
	}
	restarted := &ShelfService{}
	t.Cleanup(restarted.closePlacements)
	again, err := restarted.ProjectPlacementOperation(key, op.ID)
	if err != nil || again.Revision != op.Revision {
		t.Fatal("restart read", err)
	}
	again, err = restarted.ExecuteProjectPlacement(key, op.ID, op.IntentSHA)
	if err != nil || again.Revision != op.Revision {
		t.Fatal("replay moved file", err)
	}
	u, err := restarted.PreviewProjectPlacementUndo(key, op.ID, op.IntentSHA)
	if err != nil {
		t.Fatal("undo preview", err)
	}
	if len(u.Rows) != 1 || u.Rows[0].Action != "move" {
		t.Fatal(u.Rows)
	}
	op, err = restarted.ConfirmProjectPlacementUndo(key, u.Token, "undo-one", []string{row.ID})
	if err != nil {
		t.Fatal("undo confirm", err)
	}
	g := op.UndoGroups[0]
	op, err = restarted.ExecuteProjectPlacementUndo(key, op.ID, op.IntentSHA, g.ID, g.SHA)
	if err != nil || op.Problem != "" || op.UndoGroups[0].Restored != 1 {
		t.Fatal("undo", op, err)
	}
	if data, err := os.ReadFile(row.Source); err != nil || string(data) != "DO_NOT_SEND_FILE_CONTENT" {
		t.Fatal("not restored", err)
	}
	status, err := restarted.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := restarted.ProjectMemoryGraph(key, status.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	linked := 0
	for _, edge := range graph.Edges {
		if edge.Type == "memory_reference" {
			linked++
		}
	}
	if linked != 1 {
		t.Fatal("real undo not linked to original record", linked)
	}
}

func TestProjectPlacementServiceRejectsForeignIDsAndStoppedExecution(t *testing.T) {
	s, _, r := placementServiceFixture(t)
	key := "root:品牌项目"
	p, err := s.PreviewProjectPlacement(key, "single", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmProjectPlacement(key, p.Token, "foreign-files", []string{"invented-file"}); err == nil {
		t.Fatal("invented membership accepted")
	}
	p, err = s.PreviewProjectPlacement(key, "single", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.ConfirmProjectPlacement(key, p.Token, "stop-one", []string{p.Rows[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StopProjectPlacement(key, op.ID, op.IntentSHA, "", ""); err != nil {
		t.Fatal(err)
	}
	op, err = s.ExecuteProjectPlacement(key, op.ID, op.IntentSHA)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "stopped" || op.Rows[0].Stage != "pending" {
		t.Fatal("stopped operation executed", op)
	}
	if _, err = os.Lstat(p.Rows[0].Source); err != nil {
		t.Fatal(err)
	}
}
