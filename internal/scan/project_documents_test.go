package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBusinessAndDesignProjectsWithRelatedLooseFile(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"品牌改版/首页.fig", "客户提案/需求.md", "客户提案/报价.csv", "客户提案-交付说明.pdf"} {
		p := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("fixture"), 0644)
	}
	r, e := Scan(Options{Roots: []string{root}, Containers: []string{root}})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Projects) != 2 {
		t.Fatalf("projects=%+v", r.Projects)
	}
	found := false
	for _, l := range r.Loose {
		if filepath.Base(l.Path) == "客户提案-交付说明.pdf" {
			found = true
			if l.Group < 0 {
				t.Fatal("related document not grouped")
			}
		}
	}
	if !found {
		t.Fatal("loose deliverable missing")
	}
}
