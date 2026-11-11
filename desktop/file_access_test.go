package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFileAccessReportsAvailableAndMissingFolders(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "Desktop"), 0700); err != nil {
		t.Fatal(err)
	}
	got := checkFileAccess(home)
	if len(got) != 5 || got[0].Name != "桌面" || got[0].Status != "可访问" || got[1].Status != "文件夹不存在" || got[2].Status != "文件夹不存在" || got[3].Status != "文件夹不存在" || got[4].Status != "可访问" {
		t.Fatalf("unexpected access status: %+v", got)
	}
}
