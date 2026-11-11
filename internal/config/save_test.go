package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckNewRoot(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "Work")
	os.MkdirAll(old, 0o755)
	ok := filepath.Join(home, "Shelf")
	os.MkdirAll(ok, 0o755)
	inside := filepath.Join(old, "inside")
	os.MkdirAll(inside, 0o755)

	abs, err := CheckNewRoot("~/Shelf", old, home)
	if err != nil || abs != ok {
		t.Fatalf("ok: abs=%s err=%v", abs, err)
	}
	if _, err := CheckNewRoot(inside, old, home); err == nil {
		t.Fatal("want nest error")
	}
	if _, err := CheckNewRoot(old, inside, home); err == nil {
		t.Fatal("want reverse nest error")
	}
	if _, err := CheckNewRoot(filepath.Join(home, "nope"), old, home); err == nil {
		t.Fatal("want missing error")
	}
	same, err := CheckNewRoot(old, old, home)
	if err != nil || same != old {
		t.Fatalf("same root should be ok: %s %v", same, err)
	}
}

func TestPatchJSONFileKeepsOtherKeys(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".config", "workshelf", "config.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"root":"~/Work","timezone":"America/Los_Angeles","scan_paths":["~/Desktop"],"extra":"keep-me"}`), 0o644)
	bak, err := PatchJSONFile(p, map[string]any{"root": RelHome(filepath.Join(home, "Shelf"), home)})
	if err != nil {
		t.Fatal(err)
	}
	if bak == "" {
		t.Fatal("want bak")
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["extra"] != "keep-me" || m["timezone"] != "America/Los_Angeles" {
		t.Fatalf("lost fields: %v", m)
	}
	if m["root"] != "~/Shelf" {
		t.Fatalf("root=%v", m["root"])
	}
}
