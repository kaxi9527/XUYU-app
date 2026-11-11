//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallAppBundlePreservesSignature(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source", "WorkShelf.app")
	macos := filepath.Join(src, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0755); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, "WorkShelf"), bin, 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>test.xuyu.install</string><key>CFBundleExecutable</key><string>WorkShelf</string><key>CFBundleName</key><string>WorkShelf</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(src, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--deep", "--sign", "-", src).CombinedOutput(); err != nil {
		t.Fatalf("sign fixture: %v: %s", err, out)
	}
	dest := filepath.Join(dir, "Applications", "WorkShelf.app")
	if err := installAppBundle(src, dest); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", dest).CombinedOutput(); err != nil {
		t.Fatalf("verify installed fixture: %v: %s", err, out)
	}
}
