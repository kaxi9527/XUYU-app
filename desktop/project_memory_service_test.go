package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestProjectMemoryServiceLifecycleIdentityAndOffline(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	key := "root:品牌项目"
	status, err := s.ProjectMemory(key)
	if err != nil || status.Revision != 0 || status.ProjectID != "" {
		t.Fatal(status, err)
	}
	control := filepath.Join(cfg.Root, cfg.Names.Control)
	if _, err = os.Stat(control); !os.IsNotExist(err) {
		t.Fatal("read allocated control records", err)
	}
	file := versionFixtureFile(t, project, "作品.txt", "first result")
	start, err := s.SetProjectMemoryTracking(key, true, 0, "enable", status.CurrentScopeSHA)
	if err != nil || start.Record.Revision != 1 {
		t.Fatal(start, err)
	}
	status, err = s.ProjectMemory(key)
	if err != nil || status.ProjectID == "" || status.Revision != 1 || status.Tracked != 0 {
		t.Fatal(status, err)
	}
	retry, err := s.SetProjectMemoryTracking(key, true, 0, "enable", status.CurrentScopeSHA)
	if err != nil || retry.Record.Revision != 1 {
		t.Fatal(retry, err)
	}
	r, err := s.ObserveProjectMemory(key, 1, "scan", status.CurrentScopeSHA)
	if err != nil || r.Record.Revision != 2 {
		t.Fatal(r, err)
	}
	items, err := s.ProjectMemoryFiles(key, 2, 0)
	if err != nil || items.Total != 2 {
		t.Fatal(items, err)
	}
	if _, err = s.ProjectMemoryFiles(key, 1, 0); !errors.Is(err, projectmemory.ErrConflict) {
		t.Fatal("stale page accepted", err)
	}
	// Registering a work later uses exactly the same project, not a new ID.
	preview := versionPreview(t, s, file, "")
	if _, err = s.RegisterVersion(preview.Token, "register-after-memory"); err != nil {
		t.Fatal(err)
	}
	_, _, catalog, p, err := s.versionProject(context.Background(), key)
	if err != nil || p.ID != status.ProjectID || len(catalog.Projects) != 1 {
		t.Fatal(catalog, err)
	}
	if err = os.Rename(project, project+"-offline"); err != nil {
		t.Fatal(err)
	}
	r, err = s.ObserveProjectMemory(key, 2, "offline-scan", status.CurrentScopeSHA)
	if err != nil || r.Record.Revision != 3 {
		t.Fatal(r, err)
	}
	status, err = s.ProjectMemory(key)
	if err != nil || status.ObservationComplete || status.Missing != 0 || status.Tracked != 2 {
		t.Fatal(status, err)
	}
	if _, err = s.SetProjectMemoryTracking(key, false, 3, "pause-offline", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveProjectMemory(key, 4, "paused", status.CurrentScopeSHA); err == nil {
		t.Fatal("paused scan accepted")
	}
	history, err := s.ProjectMemoryHistory(key, 0)
	if err != nil || len(history) != 4 {
		t.Fatal(history, err)
	}
	for _, record := range history {
		if record.Project.ID != status.ProjectID {
			t.Fatal("project identity forked", record)
		}
	}
}

func TestProjectMemoryServiceScopeAndStorageLinks(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	key := "root:品牌项目"
	status, err := s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectMemoryTracking(key, true, 0, "invalid-scope", "wrong"); !errors.Is(err, projectmemory.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = os.Stat(versionCatalogPath(cfg)); !os.IsNotExist(err) {
		t.Fatal("invalid request created project", err)
	}
	if _, err = s.SetProjectMemoryTracking(key, true, 0, "enable", status.CurrentScopeSHA); err != nil {
		t.Fatal(err)
	}
	// Reading a different project does not expose this project's history.
	if err = os.Mkdir(filepath.Join(filepath.Dir(project), "另一个项目"), 0700); err != nil {
		t.Fatal(err)
	}
	other, err := s.ProjectMemory("root:另一个项目")
	if err != nil || other.Revision != 0 || other.ProjectID != "" {
		t.Fatal(other, err)
	}
	if _, err = s.SetProjectMemoryTracking("root:另一个项目", true, 0, "enable", other.CurrentScopeSHA); err != nil {
		t.Fatal("operation IDs must be project scoped", err)
	}
	first, err := s.ProjectMemory(key)
	other, err = s.ProjectMemory("root:另一个项目")
	if err != nil || first.ProjectID == other.ProjectID {
		t.Fatal(first, other, err)
	}
	base := filepath.Join(cfg.Root, cfg.Names.Control, "项目记忆")
	if err = os.Rename(base, base+"-preserved"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, base); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectMemory(key); err == nil {
		t.Fatal("memory link followed")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
