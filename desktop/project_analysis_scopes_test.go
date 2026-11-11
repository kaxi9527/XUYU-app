package main

import (
	"context"
	"fmt"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAnalysisScopesRealRecordsAndSelectedHTTP(t *testing.T) {
	s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		if len(p.Body.Files) != 40 {
			t.Errorf("unexpected selected group %d", len(p.Body.Files))
		}
		for _, f := range p.Body.Files {
			if !strings.HasPrefix(f.Position.Relative, "docs/") {
				t.Errorf("unselected file sent: %s", f.Position.Relative)
			}
		}
		writeAnalysisFixture(w, p)
	})
	for i := 0; i < 120; i++ {
		versionFixtureFile(t, project, fmt.Sprintf("docs/file-%03d.txt", i), "PRIVATE_FILE_CONTENT")
	}
	for i := 0; i < 80; i++ {
		versionFixtureFile(t, project, fmt.Sprintf("assets/image-%03d.png", i), "PRIVATE_ASSET")
	}
	state, err := s.ProjectMemory("root:品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveProjectMemory("root:品牌项目", state.Revision, "scope-scan", state.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	page, err := s.ProjectAnalysisScopes("root:品牌项目", "", "", 0)
	if err != nil || page.TotalFiles != 201 || page.CanPreview {
		t.Fatal("scope list", page.TotalFiles, err)
	}
	var docs string
	for _, row := range page.Rows {
		if row.Name == "docs" {
			docs = row.ID
		}
	}
	child, err := s.ProjectAnalysisScopes("root:品牌项目", docs, page.SHA, 0)
	if err != nil || len(child.Rows) != 3 || child.Files != 120 {
		t.Fatal("directory groups", err)
	}
	preview, err := s.PreviewProjectAnalysisGroup("root:品牌项目", child.SHA, child.Rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(preview.Packet.Body.Files) != 40 || strings.Contains(preview.Input, "PRIVATE_") {
		t.Fatal("scope browse sent or read file body")
	}
	h, err := s.ProjectAnalysisHistory("root:品牌项目", 0)
	if err != nil || h.Total != 0 {
		t.Fatal("preview reserved quota")
	}
	if _, err = s.PreviewProjectAnalysisGroup("root:品牌项目", "", child.Rows[0].ID); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	if _, err = s.PreviewProjectAnalysisGroup("root:品牌项目", child.SHA, "not-a-scope"); err == nil {
		t.Fatal("forged scope expanded to all")
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", preview.Token, "selected-group", true, false); err != nil {
		t.Fatal(err)
	}
	result := waitAnalysis(t, s, "selected-group", true)
	if result.Status != "succeeded" || calls.Load() != 1 {
		t.Fatal("selected request did not succeed once")
	}
	if _, err = s.PreviewProjectAnalysisGroup("root:品牌项目", child.SHA, child.Rows[0].ID); err == nil {
		t.Fatal("scope survived new project note revision")
	}
	fresh, err := s.ProjectAnalysisScopes("root:品牌项目", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(project, worktype.ProfileFile)
	old, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(profilePath, []byte(`{"name":"changed","folders":["different"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectAnalysisScopes("root:品牌项目", fresh.ID, fresh.SHA, 0); err == nil {
		t.Fatal("template change did not invalidate directory browser")
	}
	if err = os.WriteFile(profilePath, old, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.connections.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.connections.store.SetMode(context.Background(), catalog.Revision, "tool-only-scope", "tool", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectAnalysisScopes("root:品牌项目", "", "", 0); err != nil {
		t.Fatal("local directory browsing requires model mode", err)
	}
	if calls.Load() != 1 {
		t.Fatal("browsing made extra calls")
	}
}
