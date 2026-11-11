package main

import (
	"bytes"
	"encoding/json"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/trash"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDeletePreviewReportsHiddenDescendants(t *testing.T) {
	h := t.TempDir()
	root := filepath.Join(h, "Work")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "notes.txt"), []byte("private notes"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(h, "config.json")
	b, _ := json.Marshal(map[string]string{"root": root})
	if err := os.WriteFile(configPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSHELF_CONFIG", configPath)
	pre, err := (&ShelfService{}).PreviewDelete([]string{project})
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(pre)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	count, ok := wire["hiddenCount"].(float64)
	if !ok || count < 1 {
		t.Fatalf("ordinary parent must disclose hidden descendants before deletion: %s", b)
	}
	if _, err = os.Stat(filepath.Join(project, ".codex", "notes.txt")); err != nil {
		t.Fatal("preview changed source", err)
	}
}

func deleteFixture(t *testing.T) (trash.Options, string) {
	t.Helper()
	h := t.TempDir()
	cfg := config.Default(h)
	source := filepath.Join(cfg.Root, "project")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, ".Trash"), 0700); err != nil {
		t.Fatal(err)
	}
	return trash.Options{Config: cfg, Home: h, Paths: []string{source}}, source
}

func writeDeleteFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDeletePlanHiddenConsentReplayAndUndo(t *testing.T) {
	opt, source := deleteFixture(t)
	file := filepath.Join(source, ".codex", "notes.txt")
	writeDeleteFixture(t, file, []byte("retain this content"))
	store := deletePlans{}
	pre, err := store.preview(opt)
	if err != nil || pre.Token == "" || pre.HiddenCount != 2 {
		t.Fatalf("preview=%+v err=%v", pre, err)
	}
	if _, err = store.apply(pre.Token, false, opt.Config, opt.Home); err == nil {
		t.Fatal("hidden descendants deleted without confirmation")
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("denial changed file", err)
	}
	done, err := store.apply(pre.Token, true, opt.Config, opt.Home)
	if err != nil || done.Trashed+done.Staged != 1 {
		t.Fatalf("done=%+v err=%v", done, err)
	}
	replay, err := store.apply(pre.Token, true, opt.Config, opt.Home)
	if err != nil || replay.UndoPath != done.UndoPath {
		t.Fatal("duplicate call did not return original receipt", err)
	}
	restored, err := trash.Undo(opt.Config, done.UndoPath)
	if err != nil || restored.Restored != 1 {
		t.Fatalf("undo=%+v err=%v", restored, err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "retain this content" {
		t.Fatal("undo content differs", err)
	}
	// A delayed retry after undo must not delete the restored path.
	if _, err = store.apply(pre.Token, true, opt.Config, opt.Home); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("late retry deleted restored data", err)
	}
}

func TestDeletePlanRejectsDriftAndUnreviewedHiddenContent(t *testing.T) {
	for _, kind := range []string{"hidden-added", "large-content", "replaced"} {
		t.Run(kind, func(t *testing.T) {
			opt, source := deleteFixture(t)
			file := filepath.Join(source, "data.bin")
			original := bytes.Repeat([]byte("a"), 256*1024)
			writeDeleteFixture(t, file, original)
			if kind != "hidden-added" {
				opt.Paths = []string{file}
			}
			store := deletePlans{}
			pre, err := store.preview(opt)
			if err != nil || pre.Token == "" {
				t.Fatalf("%+v %v", pre, err)
			}
			fi, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "hidden-added":
				writeDeleteFixture(t, filepath.Join(source, ".codex", "new.txt"), []byte("new"))
			case "large-content":
				writeDeleteFixture(t, file, bytes.Repeat([]byte("b"), len(original)))
				if err = os.Chtimes(file, fi.ModTime(), fi.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err = os.Rename(file, file+".original"); err != nil {
					t.Fatal(err)
				}
				writeDeleteFixture(t, file, original)
				if err = os.Chtimes(file, fi.ModTime(), fi.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.apply(pre.Token, true, opt.Config, opt.Home); err == nil {
				t.Fatal("changed object accepted")
			}
			if _, err = os.Stat(file); err != nil {
				t.Fatal("stale rejection changed source", err)
			}
		})
	}
}

func TestDeletePlanRejectsUnknownExpiredAndChangedScope(t *testing.T) {
	opt, source := deleteFixture(t)
	writeDeleteFixture(t, filepath.Join(source, "brief.txt"), []byte("brief"))
	store := deletePlans{}
	if _, err := store.apply("invented", true, opt.Config, opt.Home); err == nil {
		t.Fatal("unreviewed deletion accepted")
	}
	pre, err := store.preview(opt)
	if err != nil {
		t.Fatal(err)
	}
	changed := opt.Config
	changed.Root = filepath.Join(opt.Home, "Other")
	if _, err = store.apply(pre.Token, true, changed, opt.Home); err == nil {
		t.Fatal("config change accepted")
	}
	store.plans[pre.Token].created = time.Now().Add(-deletePreviewTTL - time.Second)
	if _, err = store.apply(pre.Token, true, opt.Config, opt.Home); err == nil {
		t.Fatal("expired preview accepted")
	}
	if _, err := (&ShelfService{}).ApplyDeleteConfirmed(opt.Paths, true); err == nil {
		t.Fatal("legacy raw-path call bypassed preview")
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("rejected request changed source", err)
	}
}

func TestDeletePlanConcurrentCallsAndProtectedPaths(t *testing.T) {
	opt, source := deleteFixture(t)
	file := filepath.Join(source, "brief.txt")
	writeDeleteFixture(t, file, []byte("brief"))
	protected := filepath.Join(opt.Config.Root, opt.Config.Names.Control)
	writeDeleteFixture(t, filepath.Join(protected, "record.txt"), []byte("control data"))
	opt.Paths = append(opt.Paths, protected)
	store := deletePlans{}
	pre, err := store.preview(opt)
	if err != nil || pre.Skipped != 1 || pre.Token == "" {
		t.Fatalf("%+v %v", pre, err)
	}
	var wg sync.WaitGroup
	results := make(chan trash.Summary, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := store.apply(pre.Token, true, opt.Config, opt.Home)
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	log := ""
	for r := range results {
		if r.Trashed+r.Staged != 1 || r.Skipped != 1 {
			t.Fatalf("receipt=%+v", r)
		}
		if log != "" && log != r.UndoPath {
			t.Fatal("executed twice")
		}
		log = r.UndoPath
	}
	if _, err = os.Stat(protected); err != nil {
		t.Fatal("protected tree changed", err)
	}
}

func TestDeletePlanKeepsWholeProjectDeletionAndNeverFollowsNestedLinks(t *testing.T) {
	opt, source := deleteFixture(t)
	writeDeleteFixture(t, filepath.Join(source, ".git", "config"), []byte("git metadata"))
	writeDeleteFixture(t, filepath.Join(source, "brief.txt"), []byte("project"))
	outside := filepath.Join(opt.Home, "outside.txt")
	writeDeleteFixture(t, outside, []byte("outside must remain"))
	if err := os.Symlink(outside, filepath.Join(source, "reference")); err != nil {
		t.Fatal(err)
	}
	store := deletePlans{}
	pre, err := store.preview(opt)
	if err != nil || pre.Token == "" || pre.Skipped != 0 || pre.HiddenCount == 0 {
		t.Fatalf("whole project preview=%+v err=%v", pre, err)
	}
	if _, err = store.apply(pre.Token, false, opt.Config, opt.Home); err == nil {
		t.Fatal("Git metadata did not require hidden-content confirmation")
	}
	done, err := store.apply(pre.Token, true, opt.Config, opt.Home)
	if err != nil || done.Trashed+done.Staged != 1 {
		t.Fatalf("whole project delete=%+v err=%v", done, err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "outside must remain" {
		t.Fatal("nested link target changed", err)
	}
	undo, err := trash.Undo(opt.Config, done.UndoPath)
	if err != nil || undo.Restored != 1 {
		t.Fatalf("undo=%+v %v", undo, err)
	}
	if target, err := os.Readlink(filepath.Join(source, "reference")); err != nil || target != outside {
		t.Fatal("link not restored", err)
	}
}
