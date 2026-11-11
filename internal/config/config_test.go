package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsWhenMissing(t *testing.T) {
	home := t.TempDir()
	cfg, found, err := Load(filepath.Join(home, "nope.json"), home)
	if err != nil || found {
		t.Fatalf("want defaults, got found=%v err=%v", found, err)
	}
	if cfg.Root != filepath.Join(home, "Work") {
		t.Fatalf("root = %s", cfg.Root)
	}
	if cfg.Names.Outputs != "成品" || cfg.Timezone != "America/Los_Angeles" {
		t.Fatalf("bad defaults: %+v", cfg)
	}
}

func TestLoadOverrideAndEnPreset(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.json")
	os.WriteFile(p, []byte(`{"root":"~/Shelf","names_preset":"en","names":{"outputs":"deliverables"},"scan_paths":["~/A"]}`), 0o644)
	cfg, found, err := Load(p, home)
	if err != nil || !found {
		t.Fatal(err)
	}
	if cfg.Root != filepath.Join(home, "Shelf") {
		t.Fatalf("root = %s", cfg.Root)
	}
	if cfg.Names.Outputs != "deliverables" || cfg.Names.Tasks != "tasks" {
		t.Fatalf("names = %+v", cfg.Names)
	}
	if len(cfg.ScanPaths) != 1 || cfg.ScanPaths[0] != filepath.Join(home, "A") {
		t.Fatalf("scan paths = %v", cfg.ScanPaths)
	}
}

func TestBadTimezone(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.json")
	os.WriteFile(p, []byte(`{"timezone":"Mars/Base"}`), 0o644)
	if _, _, err := Load(p, home); err == nil {
		t.Fatal("want error")
	}
}
