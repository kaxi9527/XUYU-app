package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestAutomaticReportForOrganizationAndObjection(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "design.psd")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{file}, Agent: "codex", Project: "品牌", WorkType: "设计·UI原型"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	type report struct {
		operation autoorganize.Operation
		restored  bool
	}
	var reports []report
	s.autoOrganize.report = func(operation autoorganize.Operation, restored bool) {
		reports = append(reports, report{operation, restored})
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].restored || reports[0].operation.Project != "品牌" {
		t.Fatal("organization report missing", reports)
	}
	if title, body := autoReportText(reports[0].operation, false); title != "项目整理完成" || !strings.Contains(body, "已收纳 1 项") {
		t.Fatal(title, body)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil || len(reports) != 1 {
		t.Fatal("duplicate organization report", reports, err)
	}
	if _, err := autoorganize.RequestObjection(cfg, reports[0].operation.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || !reports[1].restored || reports[1].operation.Status != "restored" {
		t.Fatal("restore report missing", reports)
	}
	if title, body := autoReportText(reports[1].operation, true); title != "文件已恢复" || !strings.Contains(body, "已恢复 1 项") {
		t.Fatal(title, body)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil || len(reports) != 2 {
		t.Fatal("duplicate restore report", reports, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "design" {
		t.Fatal("file was not restored", err)
	}
}

func TestAutomaticReportWhenOriginalLocationIsOccupied(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "design.psd")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{file}, Agent: "codex", Project: "品牌", WorkType: "设计·UI原型"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	var reports []autoorganize.Operation
	s.autoOrganize.report = func(operation autoorganize.Operation, restored bool) {
		if restored {
			reports = append(reports, operation)
		}
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	operations, err := autoorganize.List(cfg)
	if err != nil || len(operations) != 1 || operations[0].Status != "organized" {
		t.Fatal("organization failed", operations, err)
	}
	if err := os.WriteFile(file, []byte("new file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := autoorganize.RequestObjection(cfg, operations[0].ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Status != "restore_partial" || reports[0].Restore.Restored != 0 {
		t.Fatal("conflict was not reported", reports)
	}
	if title, body := autoReportText(reports[0], true); title != "文件恢复未完成" || !strings.Contains(body, "1 项未恢复") {
		t.Fatal(title, body)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "new file" {
		t.Fatal("new file changed", err)
	}
	if data, err := os.ReadFile(operations[0].Summary.Items[0].Target); err != nil || string(data) != "original" {
		t.Fatal("organized file changed", err)
	}
}

func TestPartialRestoreMemoryRecordsOnlyRestoredFiles(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	desktop := filepath.Join(base, "Desktop")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(desktop, "a.psd"), filepath.Join(desktop, "b.psd"), filepath.Join(desktop, "c.psd")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte(filepath.Base(file)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: files, Agent: "codex", Project: "品牌", WorkType: "设计·UI原型"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	operations, err := autoorganize.List(cfg)
	if err != nil || len(operations) != 1 || operations[0].Status != "organized" {
		t.Fatal("organization failed", operations, err)
	}
	for _, file := range files[1:] {
		if err := os.WriteFile(file, []byte("occupied"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(expected ...string) {
		t.Helper()
		memory, err := s.ProjectMemory("root:品牌")
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.ProjectMemoryEvents("root:品牌", memory.Revision, "")
		if err != nil {
			t.Fatal(err)
		}
		var got []projectmemory.AutoEvidence
		for _, event := range page.Items {
			if event.Kind == "automatic_project_restored" && event.Auto != nil {
				got = append(got, *event.Auto)
			}
		}
		if len(got) != len(expected) {
			t.Fatalf("expected %d restoration events, got %d", len(expected), len(got))
		}
		for i, want := range expected {
			var paths []string
			for _, item := range got[i].Items {
				paths = append(paths, filepath.Base(item.After))
			}
			sort.Strings(paths)
			if strings.Join(paths, ",") != want {
				t.Fatalf("event %d restored %v, want %s", i, paths, want)
			}
		}
	}
	if _, err := s.RejectAutoOrganize(operations[0].ID); err != nil {
		t.Fatal(err)
	}
	check("a.psd")
	if err := os.Remove(files[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectAutoOrganize(operations[0].ID); err != nil {
		t.Fatal(err)
	}
	check("a.psd,b.psd", "a.psd")
	if err := os.Remove(files[2]); err != nil {
		t.Fatal(err)
	}
	result, err := s.RejectAutoOrganize(operations[0].ID)
	if err != nil || result.Status != "restored" {
		t.Fatal("final restore failed", result, err)
	}
	check("a.psd,b.psd,c.psd", "a.psd,b.psd", "a.psd")
}
