package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
)

// Opt-in only, sharing the existing isolated acceptance transport. It never
// changes production configuration or launches native file-manager actions.
type graphAcceptanceFixture struct {
	WorkID     string `json:"workID"`
	SourceID   string `json:"sourceID"`
	ExportID   string `json:"exportID"`
	ledgerHash [32]byte
}

func seedGraphAcceptance(t *testing.T, s *ShelfService, first, second, catalogPath string) *graphAcceptanceFixture {
	t.Helper()
	p := versionPreview(t, s, first, "")
	r, err := s.RegisterVersion(p.Token, "graph-browser-source")
	if err != nil {
		t.Fatal(err)
	}
	f := &graphAcceptanceFixture{WorkID: r.EntityIDs[1], SourceID: r.EntityIDs[2]}
	p, err = s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{second}, WorkID: f.WorkID, Role: "export"})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.RegisterVersion(p.Token, "graph-browser-export")
	if err != nil {
		t.Fatal(err)
	}
	f.ExportID = r.EntityIDs[2]
	r, err = s.SetVersionExportSource("root:品牌项目", f.ExportID, f.SourceID, r.Revision, "graph-browser-relation")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetWorkUsable("root:品牌项目", f.WorkID, f.SourceID, r.Revision, "graph-browser-usable")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	f.ledgerHash = sha256.Sum256(data)
	return f
}

func (f *graphAcceptanceFixture) finish(t *testing.T, s *ShelfService, second, catalogPath string) {
	t.Helper()
	g, err := s.WorkGraph("root:品牌项目", f.WorkID, 0)
	if err != nil || len(g.Nodes) != 3 || len(g.Edges) != 3 || g.Nodes[0].Usable || g.Nodes[0].State != "changed" {
		t.Fatal(g, err)
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil || sha256.Sum256(data) != f.ledgerHash {
		t.Fatal("graph read modified ledger", err)
	}
	data, err = os.ReadFile(second)
	if err != nil || string(data) != "second content" {
		t.Fatal("unrelated bytes changed", err)
	}
	report := map[string]any{"ledgerUnchanged": true, "nodes": len(g.Nodes), "edges": len(g.Edges), "usableState": g.Nodes[0].State, "nativeReveal": "not executed; acknowledged only after real VersionFile"}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("WORKSHELF_ACCEPTANCE_REPORT"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
