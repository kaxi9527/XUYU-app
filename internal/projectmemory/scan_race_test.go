//go:build go1.24 && unix

package projectmemory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedHandlesRejectReplacedFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "draft.txt")
	writeScanFile(t, path, "original")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeScanFile(t, outside, "private outside content")
	r, err := openDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before, err := r.Lstat("draft.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	s := scanner{ctx: context.Background(), options: ScanOptions{MaxHashBytes: 1024}}
	if hash, used, err := s.hash(r, "draft.txt", before); err == nil || hash != "" || used != 0 {
		t.Fatal("replacement link read", hash, used, err)
	}
	dir := filepath.Join(root, "folder")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	before, err = r.Lstat("folder")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Dir(outside), dir); err != nil {
		t.Fatal(err)
	}
	if next, err := childDirectory(r, "folder", before); err == nil {
		next.Close()
		t.Fatal("replacement directory link followed")
	}
	// Even a single observed hardlink cannot prove unique object ownership.
	if err = os.Link(path+".old", filepath.Join(root, "hardlink.txt")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path + ".old")
	if err != nil || objectKey(info) != "" {
		t.Fatal("hardlink got unique identity", err)
	}
}

func TestMemoryBaseLinkIsRejectedBeforeAllocation(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "memory")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(filepath.Join(dir, "memory"), Project{ID: "project_a", Name: "A", Path: dir}); err == nil {
		t.Fatal("linked base accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
