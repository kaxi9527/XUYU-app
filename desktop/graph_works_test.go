package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkGraphReadsRealLedgerWithoutMutation(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	g, err := s.WorkGraph("root:品牌项目", "", 0)
	if err != nil || g.Total != 0 || g.ProjectKey != "root:品牌项目" {
		t.Fatal(g, err)
	}
	if _, err = os.Stat(filepath.Dir(versionCatalogPath(cfg))); !os.IsNotExist(err) {
		t.Fatal("read allocated catalog", err)
	}
	file := versionFixtureFile(t, project, "首页.txt", "version one")
	p := versionPreview(t, s, file, "")
	r, err := s.RegisterVersion(p.Token, "graph-register")
	if err != nil {
		t.Fatal(err)
	}
	workID, versionID := r.EntityIDs[1], r.EntityIDs[2]
	r, err = s.SetWorkUsable("root:品牌项目", workID, versionID, r.Revision, "graph-usable")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(versionCatalogPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.WorkGraph("root:品牌项目", workID, 0)
	if err != nil || g.Revision != r.Revision || len(g.Nodes) != 2 || !g.Nodes[1].Usable {
		t.Fatal(g, err)
	}
	if err = os.WriteFile(file, []byte("new draft"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VersionFile("root:品牌项目", versionID); err == nil {
		t.Fatal("stale graph permitted opening rewritten content")
	}
	g, err = s.WorkGraph("root:品牌项目", workID, 0)
	if err != nil || g.Nodes[1].State != "changed" || g.Nodes[0].Usable {
		t.Fatal(g, err)
	}
	after, err := os.ReadFile(versionCatalogPath(cfg))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("graph modified ledger", err)
	}
	if _, err = s.WorkGraph("root:品牌项目", "foreign", 0); err == nil {
		t.Fatal("foreign work accepted")
	}
}
