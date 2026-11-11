//go:build darwin

package main

import (
	"bytes"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func backgroundFixture(t *testing.T) backgroundFiles {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := backgroundFiles{home: home, config: filepath.Join(home, "settings", "config.json"), app: filepath.Join(home, "Applications", "WorkShelf.app")}
	return f
}

func TestBackgroundDefaultGrantAndRestart(t *testing.T) {
	f := backgroundFixture(t)
	s, err := f.read()
	if err != nil || s.Enabled || s.StartAtLogin {
		t.Fatal(s, err)
	}
	if _, err = os.Stat(filepath.Dir(f.config)); !os.IsNotExist(err) {
		t.Fatal("read created settings")
	}
	s, err = f.set(true, false)
	if err != nil || !s.Enabled || s.StartAtLogin {
		t.Fatal(s, err)
	}
	s, err = f.read()
	if err != nil || !s.Enabled {
		t.Fatal("restart lost preference", s, err)
	}
	if _, err = os.Stat(filepath.Join(f.home, "Library")); !os.IsNotExist(err) {
		t.Fatal("background grant created login item")
	}
	if _, err = os.Stat(f.config); !os.IsNotExist(err) {
		t.Fatal("background grant changed project configuration")
	}
	s, err = f.set(false, false)
	if err != nil || s.Enabled {
		t.Fatal(s, err)
	}
}

func TestBackgroundLoginRoundTripAndEscaping(t *testing.T) {
	f := backgroundFixture(t)
	f.config = filepath.Join(f.home, "settings & context", "config.json")
	s, err := f.set(true, true)
	if err != nil || !s.Enabled || !s.StartAtLogin {
		t.Fatal(s, err)
	}
	b, err := os.ReadFile(f.loginPath())
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/plutil", "-lint", f.loginPath()).CombinedOutput(); err != nil {
		t.Fatalf("invalid macOS launch plist: %s", output)
	}
	decoder := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, err = decoder.Token()
		if err != nil {
			if err.Error() != "EOF" {
				t.Fatal(err)
			}
			break
		}
	}
	if !bytes.Contains(b, []byte("settings &amp; context")) || !bytes.Contains(b, []byte("--background")) {
		t.Fatal("incorrect launch arguments")
	}
	fi, _ := os.Stat(f.loginPath())
	if fi.Mode().Perm() != 0600 {
		t.Fatal("login file permissions")
	}
	if _, err = f.set(true, true); err != nil {
		t.Fatal("duplicate enable", err)
	}
	s, err = f.set(false, false)
	if err != nil || s.Enabled || s.StartAtLogin {
		t.Fatal(s, err)
	}
	if _, err = os.Stat(f.loginPath()); !os.IsNotExist(err) {
		t.Fatal("login item retained")
	}
}

func TestBackgroundRefusesOtherStartupItemsAndLinks(t *testing.T) {
	t.Run("foreign", func(t *testing.T) {
		f := backgroundFixture(t)
		if err := os.MkdirAll(filepath.Dir(f.loginPath()), 0700); err != nil {
			t.Fatal(err)
		}
		original := []byte("foreign startup configuration")
		if err := os.WriteFile(f.loginPath(), original, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.set(true, true); err == nil {
			t.Fatal("overwrote foreign login item")
		}
		if _, err := f.set(false, false); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(f.loginPath())
		if !bytes.Equal(b, original) {
			t.Fatal("changed foreign item")
		}
	})
	t.Run("preferences symlink", func(t *testing.T) {
		f := backgroundFixture(t)
		os.MkdirAll(filepath.Dir(f.config), 0700)
		target := filepath.Join(f.home, "keep.txt")
		os.WriteFile(target, []byte("keep"), 0600)
		os.Symlink(target, f.preferencesPath())
		if _, err := f.set(true, false); err == nil {
			t.Fatal("followed preference link")
		}
		b, _ := os.ReadFile(target)
		if string(b) != "keep" {
			t.Fatal("modified linked target")
		}
	})
	t.Run("startup directory symlink", func(t *testing.T) {
		f := backgroundFixture(t)
		target := filepath.Join(f.home, "elsewhere")
		os.MkdirAll(target, 0700)
		os.Symlink(target, filepath.Join(f.home, "Library"))
		if _, err := f.set(true, true); err == nil {
			t.Fatal("followed startup directory link")
		}
		entries, _ := os.ReadDir(target)
		if len(entries) != 0 {
			t.Fatal("wrote through directory link")
		}
	})
}

func TestBackgroundRequiresInstalledApplicationAndExplicitGrant(t *testing.T) {
	f := backgroundFixture(t)
	if _, err := f.set(false, true); err == nil {
		t.Fatal("login implicitly granted background")
	}
	f.app = filepath.Join(f.home, "Downloads", "WorkShelf.app")
	if _, err := f.set(true, true); err == nil {
		t.Fatal("temporary bundle accepted for login")
	}
	if s, err := f.set(true, false); err != nil || !s.Enabled {
		t.Fatal("temporary build cannot test background", s, err)
	}
}

func TestBackgroundProfilesCannotReplaceEachOthersLoginItem(t *testing.T) {
	f := backgroundFixture(t)
	other := f
	other.config = filepath.Join(f.home, "other", "config.json")
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, candidate := range []backgroundFiles{f, other} {
		wg.Add(1)
		go func(c backgroundFiles) { defer wg.Done(); _, err := c.set(true, true); errors <- err }(candidate)
	}
	wg.Wait()
	close(errors)
	success := 0
	for err := range errors {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected one login owner, got %d", success)
	}
	b, _ := os.ReadFile(f.loginPath())
	if !bytes.Equal(b, f.loginDocument()) && !bytes.Equal(b, other.loginDocument()) {
		t.Fatal("partial startup item")
	}
}

func TestInfoUsesActualHomeForCustomProjectRoot(t *testing.T) {
	f := backgroundFixture(t)
	os.MkdirAll(filepath.Dir(f.config), 0700)
	os.WriteFile(f.config, []byte(`{"root":"`+filepath.Join(f.home, "custom", "Projects")+`"}`), 0600)
	t.Setenv("WORKSHELF_CONFIG", f.config)
	s := &ShelfService{}
	info, err := s.Info()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if info.Home != home || info.Home == filepath.Dir(info.Root) {
		t.Fatal("custom root treated as home", info.Home, info.Root)
	}
}
