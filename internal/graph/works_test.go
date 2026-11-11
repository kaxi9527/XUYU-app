package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/versions"
)

func graphCatalog(t *testing.T, count int) (versions.Catalog, string) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("recorded bytes")
	hash := sha256.Sum256(data)
	c := versions.Catalog{Revision: 7, Projects: []versions.Project{{ID: "p", Name: "品牌项目", Path: dir, LegacyKey: "root:品牌项目"}}, Works: []versions.Work{{ID: "w", ProjectID: "p", Title: "首页设计", CurrentUsableVersionID: "v00"}}}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("v%02d", i)
		path := filepath.Join(dir, id+".txt")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		c.Versions = append(c.Versions, versions.Version{ID: id, WorkID: "w", Role: "draft", RegisteredAt: "2026-11-09T00:00:00Z", Content: versions.Content{SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data))}})
		c.Locations = append(c.Locations, versions.Location{ID: "loc" + id, VersionID: id, Path: path, AuthorizedRoot: dir, RegisteredBy: "user"})
		c.Works[0].VersionIDs = append(c.Works[0].VersionIDs, id)
	}
	return c, dir
}
func allowGraph(versions.Location) error { return nil }
func graphNode(t *testing.T, g WorkGraphResult, id string) Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("missing %s", id)
	return Node{}
}

func TestWorksGraphAvailabilityAndNoInferredHistory(t *testing.T) {
	c, dir := graphCatalog(t, 3)
	g, err := Works(context.Background(), c, "p", "", 0, allowGraph, 10000)
	if err != nil || len(g.Nodes) != 1 || len(g.Edges) != 0 || !g.Nodes[0].Usable || g.Nodes[0].VersionCount != 3 {
		t.Fatal(g, err)
	}
	g, err = Works(context.Background(), c, "p", "w", 0, allowGraph, 10000)
	if err != nil || len(g.Edges) != 3 {
		t.Fatal(g, err)
	}
	for _, e := range g.Edges {
		if e.Type != EdgeWorkVersion || e.Directed {
			t.Fatal("same bytes or time inferred derivation", e)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "v00.txt"), []byte("rewritten"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err = Works(context.Background(), c, "p", "w", 0, allowGraph, 10000)
	for _, id := range []string{"work:w", "version:v00"} {
		n := graphNode(t, g, id)
		if err != nil || n.Usable || !n.RecordedUsable || n.State != "changed" {
			t.Fatal(n, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "v00.txt")); err != nil {
		t.Fatal(err)
	}
	g, err = Works(context.Background(), c, "p", "", 0, allowGraph, 10000)
	if err != nil || g.Nodes[0].State != "missing" || g.Nodes[0].Usable {
		t.Fatal(g, err)
	}
}

func TestWorksGraphScopedReferencesAndBudget(t *testing.T) {
	c, _ := graphCatalog(t, 65)
	for i := 20; i < 65; i++ {
		c.Relations = append(c.Relations, versions.Relation{ID: fmt.Sprintf("r%d", i), VersionID: "v00", ParentVersionID: fmt.Sprintf("v%02d", i), Kind: "exported_from"})
	}
	c.Works = append(c.Works, versions.Work{ID: "foreign", ProjectID: "other", Title: "must not appear"})
	c.Versions = append(c.Versions, versions.Version{ID: "foreign-v", WorkID: "foreign"})
	c.Relations = append(c.Relations, versions.Relation{ID: "invalid-foreign", VersionID: "v00", ParentVersionID: "foreign-v", Kind: "exported_from"})
	g, err := Works(context.Background(), c, "p", "w", 0, allowGraph, 0)
	if err != nil || len(g.Nodes) != 41 || len(g.Edges) != 40 || g.Total != 65 || g.Offset != 0 || g.Limit != 20 || !strings.Contains(g.Skipped, "25 条") || !strings.Contains(g.Skipped, "未完成内容核对") {
		t.Fatal(g, err)
	}
	ids := map[string]bool{}
	refs := 0
	for _, n := range g.Nodes {
		ids[n.ID] = true
		if n.WorkID == "foreign" || n.Usable {
			t.Fatal(n)
		}
		if n.Reference {
			refs++
			if n.State != "unchecked" {
				t.Fatal(n)
			}
		}
	}
	if refs != 20 {
		t.Fatal(refs)
	}
	for _, e := range g.Edges {
		if !ids[e.From] || !ids[e.To] {
			t.Fatal("dangling endpoint", e)
		}
		if e.Type == EdgeExport && (!e.Directed || e.To != "version:v00") {
			t.Fatal("export direction", e)
		}
	}
	if _, err = Works(context.Background(), c, "p", "foreign", 0, allowGraph, 100); err == nil {
		t.Fatal("foreign work accepted")
	}
	g, err = Works(context.Background(), c, "p", "w", 10000, allowGraph, 10000)
	if err != nil || g.Offset != 60 || graphNode(t, g, "version:v60").Reference {
		t.Fatal(g, err)
	}
}

func TestWorksGraphPaginationRestrictionAndCancellation(t *testing.T) {
	c, _ := graphCatalog(t, 1)
	for i := 0; i < 35; i++ {
		c.Works = append(c.Works, versions.Work{ID: fmt.Sprintf("work%02d", i), ProjectID: "p", Title: "相同名称"})
	}
	g, err := Works(context.Background(), c, "p", "", 30, allowGraph, 10000)
	if err != nil || g.Total != 36 || len(g.Nodes) != 6 || g.Offset != 30 {
		t.Fatal(g, err)
	}
	g, err = Works(context.Background(), c, "p", "w", 0, func(versions.Location) error { return errors.New("scope revoked") }, 10000)
	if err != nil || graphNode(t, g, "version:v00").State != "restricted" || g.Nodes[0].Usable {
		t.Fatal(g, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g, err = Works(ctx, c, "p", "w", 0, allowGraph, 10000)
	if err != nil || graphNode(t, g, "version:v00").Usable || !strings.Contains(g.Skipped, "未完成内容核对") {
		t.Fatal(g, err)
	}
}
