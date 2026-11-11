//go:build (darwin && cgo) || linux

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestPreviewReservesAllTemplateDirectoriesBeforeFiles(t *testing.T) {
	a, p := inputs(t, "one/nested", "two/report.txt")
	p.Profile.Folders = []string{"资料", "资料/nested/deeper"}
	p.Suggestions[1].Folder = p.Profile.Folders[1]
	p.Suggestions[1].SuggestedTarget = filepath.Join(a.Project.Path, p.Profile.Folders[1], "report.txt")
	before := disk(t, a.Boundaries[0])
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	rows := preview.Snapshot().Rows
	if rows[0].Action != "move" || filepath.Base(rows[0].Target) != "nested-02" || !rows[0].Renamed || rows[1].Action != "move" {
		t.Fatal("file occupies required directory", rows)
	}
	if within(rows[0].Target, rows[1].Target) {
		t.Fatal("file ancestor conflict")
	}
	if !reflect.DeepEqual(before, disk(t, a.Boundaries[0])) {
		t.Fatal("reservation created directories")
	}
}

func TestPreviewRenamesRecheckExcludedNames(t *testing.T) {
	a, p := inputs(t, "one/cache")
	write(t, filepath.Join(a.Project.Path, "资料", "cache"), "existing")
	a.Excluded = []string{filepath.Join(a.Project.Path, "资料", "cache-02")}
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	row := preview.Snapshot().Rows[0]
	if row.Action != "move" || filepath.Base(row.Target) != "cache-03" || !a.allowed(row.Target) {
		t.Fatal("rename bypassed exclusion", row)
	}
}

func TestPreviewExcludesCaseAndUnicodeAliases(t *testing.T) {
	for _, pair := range [][2]string{{"Private", "private"}, {"caf\u00e9", "cafe\u0301"}} {
		t.Run(pair[0], func(t *testing.T) {
			a, p := inputs(t, "one/report.txt")
			actual := filepath.Join(a.Project.Path, "资料", pair[0])
			if err := os.MkdirAll(actual, 0o700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(a.Project.Path, "资料", pair[1])
			left, err := os.Stat(actual)
			if err != nil {
				t.Fatal(err)
			}
			right, err := os.Stat(alias)
			if err != nil || !os.SameFile(left, right) {
				t.Skip("filesystem does not alias these spellings")
			}
			a.Excluded = []string{alias}
			p.Profile.Folders[0] = "资料/" + pair[0]
			p.Suggestions[0].Folder = p.Profile.Folders[0]
			p.Suggestions[0].SuggestedTarget = filepath.Join(actual, "report.txt")
			preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
			if errors.Is(err, ErrScope) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if row := preview.Snapshot().Rows[0]; row.Action != "keep" || preview.Snapshot().HashedBytes != 0 {
				t.Fatal("excluded alias read or scheduled", row)
			}
		})
	}
}

func TestDarwinPreviewCanonicalReservationsAndAbsentExclusions(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS canonical path policy")
	}
	if pathKey("caf\u00e9") != pathKey("cafe\u0301") || pathKey("Straße") != pathKey("STRASSE") || pathKey("/tmp/one") != pathKey("/private/tmp/one") {
		t.Fatal("canonical path keys differ")
	}
	if pathKey("资料/说明.txt") == pathKey("资料/素材") {
		t.Fatal("distinct Chinese names collapsed")
	}
	a, p := inputs(t, "one/caf\u00e9", "two/report.txt")
	p.Profile.Folders = []string{"资料", "资料/cafe\u0301/nested"}
	p.Suggestions[1].Folder = p.Profile.Folders[1]
	p.Suggestions[1].SuggestedTarget = filepath.Join(a.Project.Path, p.Profile.Folders[1], "report.txt")
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	rows := preview.Snapshot().Rows
	if !rows[0].Renamed || rows[0].Action != "move" || rows[1].Action != "move" || within(pathKey(rows[0].Target), pathKey(rows[1].Target)) {
		t.Fatal("canonical file/dir collision", rows)
	}
	a.Excluded = []string{filepath.Join(a.Project.Path, "资料", "cafe\u0301")}
	if _, err = BuildPreview(context.Background(), a, p, PreviewLimits{}); !errors.Is(err, ErrScope) {
		t.Fatal("absent normalized exclusion guessed", err)
	}
}

type changedDeviceInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (i changedDeviceInfo) Sys() any { return i.stat }

func TestDeviceClassificationUsesFileNotItsParent(t *testing.T) {
	a, p := inputs(t, "project/report.txt")
	file, err := os.Stat(p.Suggestions[0].File.Fact.Path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(a.Project.Path)
	if err != nil {
		t.Fatal(err)
	}
	target := directoryFact(a.Project.Path, parent)
	if crossesDevice(file, target) {
		t.Fatal("same volume misclassified")
	}
	// A bind-mounted file can differ from its parent device. This tests that
	// classification, not actual mounts or cross-volume copy execution.
	stat := *file.Sys().(*syscall.Stat_t)
	stat.Dev++
	if !crossesDevice(changedDeviceInfo{file, &stat}, target) {
		t.Fatal("file's own device ignored")
	}
}

func TestPhysicalPreview2267HasNoDroppedItemsOrDiskChanges(t *testing.T) {
	names := make([]string, 2267)
	for i := range names {
		names[i] = fmt.Sprintf("project/loose/file-%04d.txt", i)
	}
	a, p := inputs(t, names...)
	before := disk(t, a.Boundaries[0])
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got := preview.Snapshot()
	if len(got.Rows) != len(names) || got.HashedBytes != int64(len(names)*len("example")) {
		t.Fatal("large preview truncated")
	}
	seen := map[string]bool{}
	for _, row := range got.Rows {
		key := pathKey(row.Target)
		if row.Action != "move" || seen[key] || !strings.HasPrefix(row.Target, a.Project.Path+string(filepath.Separator)) {
			t.Fatal("missing or duplicate destination", row.FileID)
		}
		seen[key] = true
	}
	if !reflect.DeepEqual(before, disk(t, a.Boundaries[0])) {
		t.Fatal("large preview changed source files")
	}
}
