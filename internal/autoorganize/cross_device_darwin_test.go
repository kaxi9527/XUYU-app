//go:build darwin && cgo

package autoorganize

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestAutomaticCrossDeviceMoveAndObjection(t *testing.T) {
	volume := os.Getenv("WORKSHELF_TEST_CROSS_VOLUME")
	if volume == "" {
		t.Skip("requires an isolated second volume")
	}
	base := t.TempDir()
	cfg := config.Default(base)
	cfg.Root = filepath.Join(volume, t.Name())
	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(base, "Desktop", "需求.md"), filepath.Join(base, "Downloads", "首页.psd")}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test artifact"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	candidate := Candidate{Project: "跨盘项目", WorkType: "设计·UI原型", Files: files}
	op, err := Execute(context.Background(), cfg, base, Policy{Enabled: true, Roots: []string{base}}, candidate)
	if err != nil || op.Status != "organized" || op.Summary == nil || op.Summary.Moved != len(files) || op.Summary.Copied != 0 {
		t.Fatalf("automatic cross-device move: %+v %v", op, err)
	}
	for _, file := range files {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("source remains after automatic move", err)
		}
	}
	restored, err := Restore(cfg, op.ID)
	if err != nil || restored.Status != "restored" || restored.Restore.Restored != len(files) {
		t.Fatalf("automatic cross-device restore: %+v %v", restored, err)
	}
	for _, file := range files {
		if data, err := os.ReadFile(file); err != nil || string(data) != "test artifact" {
			t.Fatal("automatic source not restored", err)
		}
	}
}
