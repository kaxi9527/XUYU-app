package autoorganize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

// Opt-in acceptance of project-boundary handling for explicitly selected real files.
func TestRealProjectFilesStayInTheirProject(t *testing.T) {
	input := os.Getenv("WORKSHELF_REAL_PROJECT_INPUT")
	if input == "" {
		t.Skip("real project acceptance only")
	}
	var manifest struct {
		Project string `json:"project"`
		Files   []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Backup string `json:"backup"`
		} `json:"files"`
	}
	data, err := os.ReadFile(input)
	if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Files) != 2 || !filepath.IsAbs(manifest.Project) {
		t.Fatal("invalid real project acceptance inputs")
	}
	var paths []string
	for _, f := range manifest.Files {
		data, e := os.ReadFile(f.Path)
		backup, backupErr := os.ReadFile(f.Backup)
		if e != nil || backupErr != nil || sourceevidence.Hash(data) != f.SHA256 || sourceevidence.Hash(backup) != f.SHA256 {
			t.Fatal("selected file or backup changed before acceptance")
		}
		paths = append(paths, f.Path)
	}
	h := t.TempDir()
	cfg := config.Default(h)
	if err = os.MkdirAll(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		operations, e := List(cfg)
		if e != nil {
			t.Errorf("cannot inspect receipts: %v", e)
		}
		for _, operation := range operations {
			if operation.Status != "restored" {
				if _, e = Restore(cfg, operation.ID); e != nil {
					t.Errorf("cannot restore unexpected move: %v", e)
				}
			}
		}
		for _, f := range manifest.Files {
			data, e := os.ReadFile(f.Path)
			if e != nil || sourceevidence.Hash(data) != f.SHA256 {
				t.Errorf("project file changed; verified backup: %s", f.Backup)
			}
		}
	}()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	operation, err := Execute(context.Background(), cfg, home, Policy{Enabled: true, Roots: []string{manifest.Project}}, Candidate{Project: "工程保护验收", WorkType: "设计·UI原型", Files: paths})
	if err == nil || !strings.Contains(err.Error(), "保持完整工程的内部结构") {
		t.Fatalf("project internals were not excluded: status=%s error=%v", operation.Status, err)
	}
	if _, err = os.Stat(filepath.Join(cfg.Root, "工程保护验收")); !os.IsNotExist(err) {
		t.Fatalf("unexpected project created: %v", err)
	}
}
