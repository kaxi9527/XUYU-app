package organize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func mergeRequest(sources ...string) Request {
	return Request{Project: "合并验收", Sources: sources, Merge: true, Profile: &worktype.Profile{Name: "验收目录", Folders: []string{"资料"}, Roles: map[string]string{"source": "资料", "draft": "资料", "reference": "资料", "brief": "资料", "delivery": "资料"}}}
}

func TestMergeNewProjectMarkerRejectsOldPlan(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			h := t.TempDir()
			cfg := config.Default(h)
			source := filepath.Join(h, "Downloads", "素材", "image.png")
			write(t, source, "unchanged")
			p, err := Build(cfg, h, mergeRequest(source))
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(filepath.Dir(source), ".git")
			if target {
				marker = filepath.Join(filepath.Dir(p.Rows[0].Target), ".git")
			}
			write(t, marker, "gitdir: elsewhere")
			if _, err = p.Apply(cfg, []string{p.Rows[0].ID}); err == nil {
				t.Fatal("stale project guard accepted")
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != "unchanged" {
				t.Fatal("source moved", err)
			}
		})
	}
}

func TestMergeFileDirectoryConflictAndIndependentNames(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "项目"), filepath.Join(h, "Downloads", "项目")
	write(t, filepath.Join(a, "素材", "a.txt"), "same")
	write(t, filepath.Join(b, "素材", "b.txt"), "same")
	want := filepath.Join(cfg.Root, "合并验收", "资料", "素材")
	write(t, want, "existing file")
	p, err := Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(selected(p)) != 2 {
		t.Fatalf("distinct names must both be kept: %+v", p.Rows)
	}
	var parents []string
	for _, r := range p.Rows {
		if r.Selected {
			parents = append(parents, filepath.Dir(r.Target))
		}
	}
	if parents[0] != parents[1] || parents[0] == want {
		t.Fatal("incoming folders did not share the renamed directory", parents)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Failed != 0 || sum.Moved != 2 {
		t.Fatalf("%+v %v", sum, err)
	}
	if got, _ := os.ReadFile(want); string(got) != "existing file" {
		t.Fatal("file replaced by directory")
	}
}

func TestMergeThousandsStayAutomatic(t *testing.T) {
	cfg, h := fixture(t)
	a := filepath.Join(h, "Desktop", "项目")
	b := filepath.Join(h, "Downloads", "项目")
	for i := 0; i < 2400; i++ {
		source := a
		if i%2 == 1 {
			source = b
		}
		write(t, filepath.Join(source, "素材", fmt.Sprintf("item-%04d.txt", i)), fmt.Sprintf("content-%d", i))
	}
	started := time.Now()
	p, err := Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(selected(p)) != 2400 || len(p.Warnings) != 0 {
		t.Fatalf("automatic selection=%d warnings=%v", len(selected(p)), p.Warnings)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Failed != 0 || sum.Moved != 2400 {
		t.Fatalf("%+v %v", sum, err)
	}
	t.Logf("2400 automatically planned and moved in %s", time.Since(started))
	u, err := home.Undo(cfg, sum.UndoPath)
	if err != nil || u.Failed != 0 || u.Restored != 2400 {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestMergePreservesLinkedDesignProjectAndCaseRules(t *testing.T) {
	cfg, h := fixture(t)
	a := filepath.Join(h, "Desktop", "设计工程")
	write(t, filepath.Join(a, "封面.psd"), "editable")
	write(t, filepath.Join(a, "linked", "image.png"), "linked")
	p, err := Build(cfg, h, mergeRequest(a))
	if err != nil {
		t.Fatal(err)
	}
	if len(selected(p)) != 1 || !p.Rows[0].Directory {
		t.Fatalf("linked design project split: %+v", p.Rows)
	}
	var planner mergePlanner
	if planner.key("A.txt") == planner.key("a.txt") {
		t.Fatal("case-sensitive paths conflated")
	}
	planner.caseInsensitive = true
	if planner.key("A.txt") != planner.key("a.txt") {
		t.Fatal("case-insensitive collision missed")
	}
}

func TestMergeFoldersDuplicatesVersionsAndUndo(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "项目"), filepath.Join(h, "Downloads", "项目")
	target := filepath.Join(cfg.Root, "合并验收", "资料", "素材")
	inputs := map[string]string{
		filepath.Join(a, "素材", "same.txt"): "first", filepath.Join(b, "素材", "same.txt"): "other",
		filepath.Join(a, "素材", "duplicate.txt"): "identical", filepath.Join(b, "素材", "duplicate.txt"): "identical",
		filepath.Join(a, "素材", "existing.txt"): "already", filepath.Join(b, "素材", "sub", "extra.txt"): "extra",
		filepath.Join(a, "素材", "poster-v1.png"): "version1", filepath.Join(b, "素材", "poster-v2.png"): "version2",
		filepath.Join(a, "素材", ".codex", "config.toml"): "keep hidden",
		filepath.Join(a, "素材", "repo", ".git", "HEAD"):  "ref: refs/heads/main", filepath.Join(a, "素材", "repo", "main.go"): "keep repository",
	}
	for path, content := range inputs {
		write(t, path, content)
	}
	write(t, filepath.Join(target, "existing.txt"), "already")
	p, err := Build(cfg, h, mergeRequest(b, a))
	if err != nil {
		t.Fatal(err)
	}
	duplicates, groups := 0, map[string]bool{}
	for _, row := range p.Rows {
		if row.Operation == "duplicate" {
			duplicates++
			if row.Source == filepath.Join(b, "素材", "duplicate.txt") {
				t.Fatal("source priority lost")
			}
		}
		if row.VersionGroup != "" {
			groups[row.VersionGroup] = true
		}
	}
	if duplicates != 2 || len(groups) < 2 || p.MergedFolders < 1 {
		t.Fatalf("duplicate=%d groups=%v folders=%d rows=%+v", duplicates, groups, p.MergedFolders, p.Rows)
	}
	for path, content := range inputs {
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Fatal("preview wrote source", path)
		}
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Failed != 0 || sum.Duplicates != 2 || sum.Moved != 6 {
		t.Fatalf("result=%+v err=%v", sum, err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, "existing.txt")); string(got) != "already" {
		t.Fatal("existing target replaced")
	}
	if got, _ := os.ReadFile(filepath.Join(target, "sub", "extra.txt")); string(got) != "extra" {
		t.Fatal("nested directory was not merged")
	}
	var receipt home.Summary
	data, err := os.ReadFile(sum.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &receipt); err != nil || receipt.Duplicates != 2 {
		t.Fatal("duplicate result not durable", err)
	}
	u, err := home.Undo(cfg, sum.UndoPath)
	if err != nil || u.Failed != 0 || u.Restored != 6 {
		t.Fatalf("undo=%+v %v", u, err)
	}
	for path, content := range inputs {
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Fatal("undo did not preserve original bytes", path)
		}
	}
}

func TestMergeRechecksComparedContentAndUndo(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "same.txt"), filepath.Join(h, "Downloads", "same.txt")
	content := strings.Repeat("x", 256*1024)
	write(t, a, content)
	write(t, b, content)
	p, err := Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(a)
	write(t, a, "y"+content[1:])
	os.Chtimes(a, fi.ModTime(), fi.ModTime())
	if _, err = p.Apply(cfg, selected(p)); err == nil {
		t.Fatal("same-size/time content mutation was accepted")
	}
	write(t, a, content)
	p, err = Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Moved != 1 || sum.Duplicates != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	to := sum.Items[0].Target
	fi, _ = os.Stat(to)
	write(t, to, "z"+content[1:])
	os.Chtimes(to, fi.ModTime(), fi.ModTime())
	u, err := home.Undo(cfg, sum.UndoPath)
	if err != nil || u.Failed != 1 || u.Restored != 0 {
		t.Fatalf("changed destination was undone: %+v %v", u, err)
	}
}

func TestMergeDuplicateNeedsSuccessfulRepresentative(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "same.txt"), filepath.Join(h, "Downloads", "same.txt")
	write(t, a, "same")
	write(t, b, "same")
	p, err := Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Apply(cfg, nil); err == nil {
		t.Fatal("unselected representative counted as archived")
	}
	// Force the duplicate check to occur before its pending representative. The
	// executor must report it as incomplete, never fabricate a successful group.
	var moves []home.ExactMove
	for _, m := range p.duplicates {
		moves = append(moves, m)
	}
	for _, m := range p.moves {
		moves = append(moves, m)
	}
	sum, err := home.ApplyExact(cfg, moves)
	if err != nil || sum.Failed != 1 || sum.Duplicates != 0 || sum.Moved != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if got, _ := os.ReadFile(b); string(got) != "same" {
		t.Fatal("duplicate source changed")
	}
}

func TestMergeExistingDuplicateChangeAndOnlyDuplicates(t *testing.T) {
	cfg, h := fixture(t)
	src := filepath.Join(h, "Desktop", "same.txt")
	dst := filepath.Join(cfg.Root, "合并验收", "资料", "same.txt")
	write(t, src, "same")
	write(t, dst, "same")
	p, err := Build(cfg, h, mergeRequest(src))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dst, "different")
	if _, err = p.Apply(cfg, nil); err == nil {
		t.Fatal("changed existing target accepted")
	}
	write(t, dst, "same")
	p, err = Build(cfg, h, mergeRequest(src))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := p.Apply(cfg, nil)
	if err != nil || sum.Moved != 0 || sum.Duplicates != 1 || sum.UndoPath != "" {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestMergeCancellationLimitsAndCanonicalNames(t *testing.T) {
	cfg, h := fixture(t)
	a, b := filepath.Join(h, "Desktop", "café.txt"), filepath.Join(h, "Downloads", "cafe\u0301.txt")
	write(t, a, "same")
	write(t, b, "same")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildContext(ctx, cfg, h, mergeRequest(a, b)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preview continued", err)
	}
	planner := mergePlanner{ctx: context.Background(), bytes: 1, deadline: time.Now().Add(time.Second), hashes: map[string]string{}}
	if _, err := planner.hash(a); err == nil {
		t.Fatal("comparison byte budget ignored")
	}
	if runtime.GOOS == "darwin" && !nativeUnicodeNormalization {
		t.Skip("native macOS filename comparison requires the desktop CGO build")
	}
	p, err := Build(cfg, h, mergeRequest(a, b))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := p.Apply(cfg, selected(p))
	if err != nil || sum.Failed != 0 {
		t.Fatalf("canonical collision not resolved: %+v %v", sum, err)
	}
	if runtime.GOOS == "darwin" && (sum.Moved != 1 || sum.Duplicates != 1) {
		t.Fatalf("canonical paths must share one slot: %+v", sum)
	}
}
