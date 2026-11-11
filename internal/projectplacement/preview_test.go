//go:build (darwin && cgo) || linux

package projectplacement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func inputs(t *testing.T, names ...string) (Authority, projectcontext.PlacementProposal) {
	t.Helper()
	base := t.TempDir()
	project := projectmemory.Project{ID: "project-one", Name: "示例项目", Path: filepath.Join(base, "project")}
	if err := os.MkdirAll(project.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	scope := projectmemory.Scope{Roots: []string{project.Path}}
	p := projectcontext.PlacementProposal{Project: project, Profile: worktype.Profile{Name: "设计", Folders: []string{"资料"}}, References: []projectcontext.PlacementReference{{Kind: "single", RequestID: "analysis-one", ContextSHA: strings.Repeat("a", 64), AdviceSHA: strings.Repeat("b", 64)}}}
	for i, name := range names {
		path := filepath.Join(base, name)
		write(t, path, "example")
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		fact := projectmemory.Fact{Path: path, Kind: "file", Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano), ObjectKey: projectmemory.ObjectIdentity(info), HashState: "verified", SHA256: sum([]byte("example"))}
		file := projectmemory.File{ID: fmt.Sprintf("file-%04d", i), Status: "present", Fact: fact, FirstObservedAt: fact.ModifiedAt, LastEvent: "event-one"}
		p.Suggestions = append(p.Suggestions, projectcontext.PlacementSuggestion{File: file, Folder: "资料", SuggestedTarget: filepath.Join(project.Path, "资料", filepath.Base(path)), Reason: "归入资料"})
		if !within(project.Path, path) {
			scope.Files = append(scope.Files, path)
		}
	}
	p.Scope = scope
	return Authority{Project: project, Scope: scope, Boundaries: []string{base}}, p
}

func disk(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			out[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = sum(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPreviewReadOnlyInPlaceScopeAndDetachedSnapshot(t *testing.T) {
	a, p := inputs(t, "project/notes.txt", "loose/image.png")
	before := disk(t, a.Boundaries[0])
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got := preview.Snapshot()
	if len(got.Rows) != 2 || len(got.Directories) < 3 || got.HashedBytes != 14 || got.Authority.Project != a.Project || len(preview.SHA256()) != 64 {
		t.Fatalf("scope or coverage: %+v", got)
	}
	for i, row := range got.Rows {
		if row.Action != "move" || row.Code != "proposed" || row.FileID != p.Suggestions[i].File.ID || row.Before.SHA256 != sum([]byte("example")) || row.Target != p.Suggestions[i].SuggestedTarget || row.Object == "" {
			t.Fatalf("row: %+v", row)
		}
	}
	data, _ := json.Marshal(got)
	if sum(data) != preview.SHA256() {
		t.Fatal("fingerprint differs")
	}
	if !reflect.DeepEqual(before, disk(t, a.Boundaries[0])) {
		t.Fatal("preview changed disk")
	}
	got.Rows[0].Target = "/outside"
	got.Authority.Scope.Roots[0] = "/outside"
	p.Profile.Folders[0] = "changed"
	a.Scope.Roots[0] = "/outside"
	if preview.Snapshot().Rows[0].Target == "/outside" || preview.Snapshot().Authority.Scope.Roots[0] == "/outside" || preview.Snapshot().Proposal.Profile.Folders[0] != "资料" {
		t.Fatal("preview shares mutable state")
	}
}

func TestPreviewCollisionRetainsIdenticalAndDifferentContent(t *testing.T) {
	a, p := inputs(t, "one/report.txt", "two/report.txt")
	write(t, filepath.Join(a.Project.Path, "资料", "report.txt"), "example")
	write(t, filepath.Join(a.Project.Path, "资料", "report-02.txt"), "older version")
	before := disk(t, a.Boundaries[0])
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got := preview.Snapshot()
	for i, row := range got.Rows {
		if row.Action != "move" || !row.Renamed || filepath.Base(row.Target) != fmt.Sprintf("report-%02d.txt", i+3) {
			t.Fatalf("collision overwrites or overlaps: %+v", row)
		}
	}
	if !reflect.DeepEqual(before, disk(t, a.Boundaries[0])) {
		t.Fatal("duplicate or old version was changed")
	}
}

func TestPreviewAlreadyPlacedAndConservativeCaseReservations(t *testing.T) {
	a, p := inputs(t, "project/资料/placed.txt", "one/File.txt", "two/file.txt")
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	rows := preview.Snapshot().Rows
	if rows[0].Action != "keep" || rows[0].Code != "already_placed" || rows[0].Target != "" {
		t.Fatal(rows[0])
	}
	if rows[1].Action != "move" || rows[2].Action != "move" || !rows[2].Renamed || strings.EqualFold(rows[1].Target, rows[2].Target) {
		t.Fatal("case collision not reserved")
	}
}

func TestPreviewProtectsRootEngineeringAndAncestors(t *testing.T) {
	for _, tc := range []struct{ name, marker string }{{"project-root", "project/.git"}, {"source-root", "loose/package.json"}, {"source-ancestor", "loose/package.json"}, {"target", "project/资料/pyproject.toml"}, {"worktree", "project/.git"}, {"host-boundary", "go.mod"}, {"video", "loose/edit.prproj"}} {
		t.Run(tc.name, func(t *testing.T) {
			name := "loose/a.txt"
			if tc.name == "source-ancestor" {
				name = "loose/nested/a.txt"
			}
			a, p := inputs(t, name)
			write(t, filepath.Join(a.Boundaries[0], tc.marker), "marker")
			preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
			if err != nil {
				t.Fatal(err)
			}
			row := preview.Snapshot().Rows[0]
			if row.Action != "keep" || row.Code != "protected_project" || preview.Snapshot().HashedBytes != 0 {
				t.Fatalf("engineering root missed: %+v", row)
			}
		})
	}
}

func TestPreviewProtectsHiddenEnvironmentAndDatabaseFiles(t *testing.T) {
	for _, name := range []string{"project/.codex/state.json", "project/node_modules/a.txt", "project/data.sqlite", "project/client.app/a.txt", "project/private.pem", "project/Library/a.txt"} {
		t.Run(filepath.Base(name), func(t *testing.T) {
			a, p := inputs(t, name)
			preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
			if err != nil {
				t.Fatal(err)
			}
			if r := preview.Snapshot().Rows[0]; r.Code != "protected_path" || r.Action != "keep" || preview.Snapshot().HashedBytes != 0 {
				t.Fatal(r)
			}
		})
	}
}

func TestPreviewRejectsDirectoryLinksAndSpecialObjects(t *testing.T) {
	for _, kind := range []string{"source-parent", "target-parent", "file-link", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			a, p := inputs(t, "loose/a.txt")
			base, src := a.Boundaries[0], p.Suggestions[0].File.Fact.Path
			switch kind {
			case "source-parent":
				if err := os.Rename(filepath.Join(base, "loose"), filepath.Join(base, "elsewhere")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(base, "loose")); err != nil {
					t.Fatal(err)
				}
			case "target-parent":
				if err := os.Symlink(filepath.Join(base, "loose"), filepath.Join(a.Project.Path, "资料")); err != nil {
					t.Fatal(err)
				}
			case "file-link":
				if err := os.Rename(src, filepath.Join(base, "other.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(base, "other.txt"), src); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(src, filepath.Join(base, "other.txt")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(src); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(src, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
			if err != nil {
				t.Fatal(err)
			}
			r := preview.Snapshot().Rows[0]
			if r.Action != "keep" || r.Target != "" || preview.Snapshot().HashedBytes != 0 {
				t.Fatal("unsafe object read", r)
			}
		})
	}
}

func TestPreviewChangesFullHashBudgetAndCancellation(t *testing.T) {
	t.Run("same-size-and-time", func(t *testing.T) {
		a, p := inputs(t, "project/a.txt")
		path := p.Suggestions[0].File.Fact.Path
		old, _ := os.Stat(path)
		write(t, path, "changed")
		if err := os.Chtimes(path, old.ModTime(), old.ModTime()); err != nil {
			t.Fatal(err)
		}
		preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if r := preview.Snapshot().Rows[0]; r.Code != "source_changed" || r.Action != "keep" {
			t.Fatal("metadata alone accepted", r)
		}
	})
	t.Run("explicit-budget-coverage", func(t *testing.T) {
		a, p := inputs(t, "project/a.txt", "project/b.txt", "project/c.txt")
		preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{MaxBytes: 7})
		if err != nil {
			t.Fatal(err)
		}
		rows := preview.Snapshot().Rows
		if len(rows) != 3 || rows[0].Action != "move" || rows[1].Code != "verification_limit" || rows[2].Code != "verification_limit" || preview.Snapshot().HashedBytes != 7 {
			t.Fatal("budget silently dropped rows")
		}
		preview, err = BuildPreview(context.Background(), a, p, PreviewLimits{MaxFileBytes: 1})
		if err != nil || len(preview.Snapshot().Rows) != 3 || preview.Snapshot().HashedBytes != 0 {
			t.Fatal("per-file limit", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		a, p := inputs(t, "project/a.txt")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		preview, err := BuildPreview(ctx, a, p, PreviewLimits{})
		if !errors.Is(err, context.Canceled) || preview.SHA256() != "" {
			t.Fatal("partial approval-like preview returned", err)
		}
	})
}

func TestPreviewRefusesForeignScopeAndRules(t *testing.T) {
	for _, kind := range []string{"sibling", "project", "target", "excluded", "rule", "boundary", "duplicate", "invalid-fact"} {
		t.Run(kind, func(t *testing.T) {
			a, p := inputs(t, "loose/a.txt")
			switch kind {
			case "sibling":
				p.Suggestions[0].File.Fact.Path = filepath.Join(a.Boundaries[0], "loose", "b.txt")
			case "project":
				a.Project.ID = "other"
			case "target":
				p.Suggestions[0].SuggestedTarget = filepath.Join(a.Boundaries[0], "elsewhere.txt")
			case "excluded":
				a.Excluded = []string{filepath.Join(a.Project.Path, "资料")}
			case "rule":
				p.Profile.Folders = append(p.Profile.Folders, "源文件")
				p.Profile.Rules = []worktype.Rule{{Kind: "extension", Value: "txt", Folder: "源文件"}}
			case "boundary":
				a.Boundaries = []string{"/"}
			case "duplicate":
				p.Suggestions = append(p.Suggestions, p.Suggestions[0])
			case "invalid-fact":
				p.Suggestions[0].File.Fact.SHA256 = "bad"
			}
			if _, err := BuildPreview(context.Background(), a, p, PreviewLimits{}); !errors.Is(err, ErrScope) {
				t.Fatal("scope widened or invalid facts accepted", err)
			}
		})
	}
}

func TestPreviewDirectoryDriftAndBoundedInspection(t *testing.T) {
	a, p := inputs(t, "project/a.txt", "loose/b.txt")
	newBuilder := func() *previewBuilder {
		return &previewBuilder{ctx: context.Background(), authority: a, limits: PreviewLimits{MaxEntries: 100}, directories: map[string]Directory{}, checks: map[string]directoryCheck{}, reserved: map[string]bool{}}
	}
	b := newBuilder()
	r, _, _, err := b.inspect(a.Project.Path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	write(t, filepath.Join(a.Project.Path, "package.json"), "{}")
	if !errors.Is(b.checkDirectories(), ErrChanged) {
		t.Fatal("new engineering marker undetected")
	}
	b = newBuilder()
	r, _, _, err = b.inspect(filepath.Join(a.Boundaries[0], "loose"))
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if err := os.Rename(filepath.Join(a.Boundaries[0], "loose"), filepath.Join(a.Boundaries[0], "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(a.Boundaries[0], "moved"), filepath.Join(a.Boundaries[0], "loose")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(b.checkDirectories(), ErrChanged) {
		t.Fatal("ancestor substitution accepted")
	}
	preview, err := BuildPreview(context.Background(), a, p, PreviewLimits{MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range preview.Snapshot().Rows {
		if row.Code != "inspection_limit" || row.Action != "keep" {
			t.Fatal("directory limit guessed", row)
		}
	}
	// A depleted quota must not call Readdirnames(0), which means unlimited.
	b = newBuilder()
	b.limits.MaxEntries, b.entries = 1, 2
	r, _, check, err := b.inspect(a.Project.Path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if check.code != "inspection_limit" || b.entries != 2 {
		t.Fatal("depleted quota caused unbounded read")
	}
}

func TestObservedAdviceResolvesToPhysicalPreview(t *testing.T) {
	a, _ := inputs(t, "project/notes.txt", "loose/reference.png")
	observed, err := projectmemory.Scan(context.Background(), a.Scope, projectmemory.ScanOptions{})
	if err != nil || !observed.Complete {
		t.Fatal("observe", err)
	}
	v := projectmemory.View{HeadSHA: strings.Repeat("a", 64), State: projectmemory.State{Project: a.Project, Revision: 2, Enabled: true, Scope: a.Scope, LastObservedAt: observed.EndedAt, ObservationComplete: true}}
	for i, fact := range observed.Files {
		v.State.Files = append(v.State.Files, projectmemory.File{ID: fmt.Sprintf("file-%d", i), Fact: fact, Status: "present", FirstObservedAt: observed.StartedAt, LastEvent: "event-2"})
	}
	profile := worktype.Profile{Name: "设计", Folders: []string{"资料"}}
	packet, err := projectcontext.Build(v, profile, nil, projectcontext.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, file := range packet.Body.Files {
		if file.Kind == "file" && file.Status == "present" {
			ids = append(ids, file.ID)
		}
	}
	sort.Strings(ids)
	advice, _ := json.Marshal(projectcontext.Advice{ProjectID: a.Project.ID, ContextSHA: packet.SHA256, MemoryRevision: 2, Groups: []projectcontext.Group{{FolderID: packet.Body.Folders[0].ID, FileIDs: ids, Reason: "放入资料目录"}}, UncertainIDs: []string{}})
	recipient := models.Recipient{Epoch: 1, ProviderID: "fixture", ProviderVersion: 1, ProviderName: "Fixture", Protocol: models.Chat, BaseURL: "https://fixture.invalid/v1", ModelRecordID: "fixture-model", ModelID: "fixture", ModelName: "Fixture"}
	proposal, err := projectcontext.ResolvePlacements(v, profile, []projectcontext.PlacementEvidence{{Kind: "single", RequestID: "analysis-one", Packet: packet, Advice: advice, Recipient: recipient}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := BuildPreview(context.Background(), a, proposal, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Snapshot().Rows) != 2 || preview.Snapshot().Proposal.References[0].ContextSHA != packet.SHA256 {
		t.Fatal("lost original evidence")
	}
	for _, row := range preview.Snapshot().Rows {
		if row.Action != "move" || row.Before.HashState != "verified" {
			t.Fatal("real observed advice not resolved", row)
		}
	}
}
