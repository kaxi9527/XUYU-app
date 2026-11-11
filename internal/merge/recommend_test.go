package merge

import (
	"os"
	"path/filepath"
	"testing"
)

func evidenceFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecommendationSeparatesNamesContentAndIdentity(t *testing.T) {
	r := t.TempDir()
	a, b := filepath.Join(r, "项目"), filepath.Join(r, "项目 副本")
	hs := []hit{{path: a}, {path: b}}
	evidenceFiles(t, a, map[string]string{"README.md": "same", "package.json": "same", "logo.png": "same"})
	evidenceFiles(t, b, map[string]string{"README.md": "same", "package.json": "same", "logo.png": "same"})
	if level, _ := recommend(hs, newEvidenceBudget(r)); level != "possible" {
		t.Fatal("generic templates must remain uncertain", level)
	}
	evidenceFiles(t, a, map[string]string{"客户简报.md": "client brief", "品牌草图.png": "sketch"})
	evidenceFiles(t, b, map[string]string{"客户简报.md": "client brief", "品牌草图.png": "sketch"})
	if level, _ := recommend(hs, newEvidenceBudget(r)); level != "recommended" {
		t.Fatal("missing content evidence", level)
	}
	hs[0].origin = "git@github.com:one/project.git"
	hs[1].origin = "https://github.com/two/project.git"
	if level, _ := recommend(hs, newEvidenceBudget(r)); level != "conflict" {
		t.Fatal("content must not override repository identity", level)
	}
	hs[1].origin = "https://github.com/one/project.git"
	if level, _ := recommend(hs, newEvidenceBudget(r)); level != "recommended" {
		t.Fatal("same normalized repository", level)
	}
}

func TestRecommendationDoesNotBridgePartialEvidence(t *testing.T) {
	r := t.TempDir()
	a, b, c := filepath.Join(r, "a"), filepath.Join(r, "b"), filepath.Join(r, "c")
	evidenceFiles(t, a, map[string]string{"客户简报.md": "brief", "品牌草图.png": "sketch"})
	evidenceFiles(t, b, map[string]string{"客户简报.md": "brief", "品牌草图.png": "sketch", "营销计划.md": "plan", "活动海报.png": "poster"})
	evidenceFiles(t, c, map[string]string{"营销计划.md": "plan", "活动海报.png": "poster"})
	if level, _ := recommend([]hit{{path: a}, {path: b}, {path: c}}, newEvidenceBudget(r)); level != "possible" {
		t.Fatal("A/B and B/C do not prove A/C", level)
	}
}

func TestRecommendationUsesCurrentRepositoryIdentity(t *testing.T) {
	r := t.TempDir()
	a, b := filepath.Join(r, "a"), filepath.Join(r, "b")
	evidenceFiles(t, filepath.Join(a, ".git"), map[string]string{"config": "[remote \"origin\"]\nurl = https://github.com/one/project.git\n"})
	evidenceFiles(t, filepath.Join(b, ".git"), map[string]string{"config": "[remote \"origin\"]\nurl = https://github.com/two/project.git\n"})
	hs := []hit{{path: a, origin: "https://github.com/stale/same.git"}, {path: b, origin: "https://github.com/stale/same.git"}}
	if level, _ := recommend(hs, newEvidenceBudget(r)); level != "conflict" {
		t.Fatal("cached registration overrode live identity", level)
	}
}

func TestRecommendationEvidenceHonorsBoundsAndLinks(t *testing.T) {
	r := t.TempDir()
	a, b := filepath.Join(r, "a"), filepath.Join(r, "b")
	evidenceFiles(t, a, map[string]string{"客户简报.md": "brief", "品牌草图.png": "sketch", ".env": "secret"})
	evidenceFiles(t, b, map[string]string{"客户简报.md": "brief", "品牌草图.png": "sketch", ".env": "secret"})
	hs := []hit{{path: a}, {path: b}}
	budget := newEvidenceBudget(r)
	budget.remaining = 1
	if level, _ := recommend(hs, budget); level != "possible" {
		t.Fatal("incomplete check upgraded", level)
	}
	if level, _ := recommend(hs, newEvidenceBudget(filepath.Join(r, "unrelated"))); level != "possible" {
		t.Fatal("read outside configured roots", level)
	}
	link := filepath.Join(r, "linked")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	if level, _ := recommend([]hit{{path: link}, {path: b}}, newEvidenceBudget(r)); level != "possible" {
		t.Fatal("followed source symlink", level)
	}
	files := newEvidenceBudget(r).files(a)
	if len(files) != 2 || files[".env"] != "" {
		t.Fatal("hidden configuration inspected", files)
	}
}
