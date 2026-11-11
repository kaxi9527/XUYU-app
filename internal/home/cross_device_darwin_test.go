//go:build darwin && cgo

package home

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestCrossDeviceMoveAndUndo(t *testing.T) {
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
	for _, directory := range []bool{false, true} {
		name := "document.txt"
		if directory {
			name = "folder"
		}
		source := filepath.Join(base, name)
		file := source
		if directory {
			if err := os.Mkdir(source, 0750); err != nil {
				t.Fatal(err)
			}
			file = filepath.Join(source, "document.txt")
		}
		if err := os.WriteFile(file, []byte("test content"), 0640); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("xattr", "-w", "org.workshelf.test", "origin", file).CombinedOutput(); err != nil {
			t.Fatalf("set test attribute: %s %v", output, err)
		}
		snapshot, err := Snapshot(source)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(cfg.Root, name)
		if !IsCrossDevice(source, target) {
			t.Fatal("test paths are not on separate devices")
		}
		sum, err := ApplyExact(cfg, []ExactMove{{Source: source, Target: target, SourceRoot: base, Snapshot: snapshot, CrossDevice: true}})
		if err != nil || sum.Moved != 1 || sum.Copied != 0 || sum.Failed != 0 {
			t.Fatalf("cross-device move: %+v %v", sum, err)
		}
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Fatal("source remains after move", err)
		}
		targetFile := target
		if directory {
			targetFile = filepath.Join(target, "document.txt")
		}
		if data, err := os.ReadFile(targetFile); err != nil || string(data) != "test content" {
			t.Fatal("target content", err)
		}
		if data, err := exec.Command("xattr", "-p", "org.workshelf.test", targetFile).Output(); err != nil || string(data) != "origin\n" {
			t.Fatal("target attribute", string(data), err)
		}
		undo, err := Undo(cfg, sum.UndoPath)
		if err != nil || undo.Restored != 1 || undo.Failed != 0 || undo.Skipped != 0 {
			t.Fatalf("cross-device undo: %+v %v", undo, err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("target remains after undo", err)
		}
		if data, err := os.ReadFile(file); err != nil || string(data) != "test content" {
			t.Fatal("restored content", err)
		}
		if data, err := exec.Command("xattr", "-p", "org.workshelf.test", file).Output(); err != nil || string(data) != "origin\n" {
			t.Fatal("restored attribute", string(data), err)
		}
		again, err := Undo(cfg, sum.UndoPath)
		if err != nil || again.Unchanged != 1 || again.Failed != 0 {
			t.Fatalf("repeat undo: %+v %v", again, err)
		}
	}
}

func TestCrossDeviceLegacyMoveAndUndo(t *testing.T) {
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
	source := filepath.Join(base, "legacy.txt")
	if err := os.WriteFile(source, []byte("legacy content"), 0644); err != nil {
		t.Fatal(err)
	}
	sum, err := Apply(Options{Config: cfg, Jobs: []Job{{Source: source, Project: "项目", Area: AreaLoose}}})
	if err != nil || sum.Moved != 1 || sum.Copied != 0 || sum.Failed != 0 || !sum.Items[0].CrossDevice {
		t.Fatalf("legacy move: %+v %v", sum, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source remains after legacy move", err)
	}
	undo, err := Undo(cfg, sum.UndoPath)
	if err != nil || undo.Restored != 1 || undo.Failed != 0 {
		t.Fatalf("legacy undo: %+v %v", undo, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "legacy content" {
		t.Fatal("legacy content not restored", err)
	}
	if _, err := os.Stat(sum.Items[0].Target); !os.IsNotExist(err) {
		t.Fatal("target remains after legacy undo", err)
	}
}

func TestCrossDeviceMoveDoesNotReplaceExistingFile(t *testing.T) {
	volume := os.Getenv("WORKSHELF_TEST_CROSS_VOLUME")
	if volume == "" {
		t.Skip("requires an isolated second volume")
	}
	source := filepath.Join(t.TempDir(), "source.txt")
	target := filepath.Join(volume, t.Name()+".txt")
	if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := moveCrossDevice(source, target); err == nil {
		t.Fatal("existing target was accepted")
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "source" {
		t.Fatal("source changed after failed move", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "existing" {
		t.Fatal("existing target changed", err)
	}
}

func TestCrossDeviceUndoPreservesLaterEdits(t *testing.T) {
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
	source, target := filepath.Join(base, "draft.txt"), filepath.Join(cfg.Root, "draft.txt")
	if err := os.WriteFile(source, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Snapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := ApplyExact(cfg, []ExactMove{{Source: source, Target: target, SourceRoot: base, Snapshot: snapshot, CrossDevice: true}})
	if err != nil || sum.Moved != 1 {
		t.Fatal("initial move", sum, err)
	}
	if err := os.WriteFile(target, []byte("later edit"), 0644); err != nil {
		t.Fatal(err)
	}
	undo, err := Undo(cfg, sum.UndoPath)
	if err != nil || undo.Failed != 1 || undo.Restored != 0 {
		t.Fatal("changed target was not reported", undo, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "later edit" {
		t.Fatal("later edit changed", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("undo recreated source despite later edit", err)
	}
}

func TestCrossDeviceMoveKeepsSourceWhenWritingContinues(t *testing.T) {
	volume := os.Getenv("WORKSHELF_TEST_CROSS_VOLUME")
	if volume == "" {
		t.Skip("requires an isolated second volume")
	}
	source := filepath.Join(t.TempDir(), "active.bin")
	target := filepath.Join(volume, t.Name()+".bin")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	stop, started, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		var value uint64
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			value++
			var data [8]byte
			binary.LittleEndian.PutUint64(data[:], value)
			if _, err := f.WriteAt(data[:], 0); err != nil {
				done <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	<-started
	err = moveCrossDevice(source, target)
	close(stop)
	if writeErr := <-done; writeErr != nil {
		t.Fatal("test writer failed", writeErr)
	}
	if err == nil {
		t.Fatal("actively changing source was accepted")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("actively changing source was removed", err)
	}
}
