package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepeatedSameNamesNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	os.Mkdir(dest, 0755)
	for i, content := range []string{"one", "two", "three"} {
		src := filepath.Join(dir, content, "same.txt")
		os.MkdirAll(filepath.Dir(src), 0755)
		os.WriteFile(src, []byte(content), 0644)
		target, err := moveInto(src, dest)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(target)
		if string(data) != content {
			t.Fatalf("item %d overwritten", i)
		}
	}
	ents, _ := os.ReadDir(dest)
	if len(ents) != 3 {
		t.Fatal(ents)
	}
}
func TestDeleteJournalFailurePreservesHiddenSource(t *testing.T) {
	cfg, h := setup(t)
	src := filepath.Join(h, ".codex", "test.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("session"), 0644)
	// A regular file blocks the journal directory before any rename is attempted.
	os.WriteFile(filepath.Join(cfg.Root, cfg.Names.Control, "整理撤销"), []byte("blocked"), 0644)
	sum, err := Apply(Options{Config: cfg, Home: h, Paths: []string{src}, AllowOutsideRoot: true})
	if err == nil && sum.Failed == 0 {
		t.Fatal("unexpected success")
	}
	if b, e := os.ReadFile(src); e != nil || string(b) != "session" {
		t.Fatal("source changed", e)
	}
}
