package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

func versionFixture(t *testing.T) (*ShelfService, config.Config, string, string) {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default(base)
	project := filepath.Join(cfg.Root, "品牌项目")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.json")
	writeVersionConfig(t, configPath, cfg)
	t.Setenv("WORKSHELF_CONFIG", configPath)
	return &ShelfService{}, cfg, project, configPath
}
func writeVersionConfig(t *testing.T, path string, cfg config.Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func versionFixtureFile(t *testing.T, project, name, text string) string {
	t.Helper()
	file := filepath.Join(project, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
func versionCatalogPath(cfg config.Config) string {
	return filepath.Join(cfg.Root, cfg.Names.Control, "作品版本", "catalog.json")
}
func versionPreview(t *testing.T, s *ShelfService, path, workID string) VersionPreview {
	t.Helper()
	p, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{path}, WorkID: workID, Title: "首页设计", Role: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionServiceRegistrationConfirmationAndGrouping(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	file1 := versionFixtureFile(t, project, "首页-v1.txt", "version one")
	list, err := s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || list.Total != 0 || list.Revision != 0 {
		t.Fatal(list, err)
	}
	if _, err = os.Stat(filepath.Dir(versionCatalogPath(cfg))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty read created catalog", err)
	}
	p := versionPreview(t, s, file1, "")
	if _, err = os.Stat(versionCatalogPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview persisted metadata", err)
	}
	// The serialized preview is presentation only, not writable server authority.
	p.Items[0].Content.SHA256 = "untrusted"
	p.Items[0].Path = "/untrusted"
	r, err := s.RegisterVersion(p.Token, "register-one")
	if err != nil {
		t.Fatal(err)
	}
	workID, v1 := r.EntityIDs[1], r.EntityIDs[2]
	r, err = s.SetWorkUsable("root:品牌项目", workID, v1, r.Revision, "confirm-one")
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.ProjectWorks("root:品牌项目", "首页", 0)
	if err != nil || len(list.Items) != 1 || !list.Items[0].Usable {
		t.Fatal(list, err)
	}
	file2 := versionFixtureFile(t, project, "首页-v2.txt", "version two")
	p = versionPreview(t, s, file2, workID)
	r, err = s.RegisterVersion(p.Token, "register-two")
	if err != nil {
		t.Fatal(err)
	}
	v2 := r.EntityIDs[2]
	list, err = s.ProjectWorks("root:品牌项目", "v2", 0)
	if err != nil || list.Items[0].CurrentUsableVersionID != v1 || list.Items[0].Versions != 2 {
		t.Fatal(list, err)
	}
	r, err = s.AddVersionSource("root:品牌项目", v2, versions.UserNote{Tool: "用户记得使用的工具", Source: "本地备注"}, r.Revision, "source-note")
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.SetVersionExportSource("root:品牌项目", v2, v1, r.Revision, "export-relation")
	if err != nil {
		t.Fatal(err)
	}
	details, err := s.WorkVersions("root:品牌项目", workID, 0)
	if err != nil || len(details.Items) != 2 || len(details.Relations) != 1 {
		t.Fatal(details, err)
	}
	for _, v := range details.Items {
		if v.Version.ID == v2 && (len(v.Provenance) != 1 || v.Provenance[0].Kind != "user_note" || v.Provenance[0].Agent != "") {
			t.Fatal("source provenance fabricated", v)
		}
	}
	if path, err := s.VersionFile("root:品牌项目", v1); err != nil || path != file1 {
		t.Fatal(path, err)
	}
	if err = os.WriteFile(file1, []byte("changed after confirmation"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err = s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || list.Items[0].Usable || list.Items[0].UsableState != "changed" {
		t.Fatal(list, err)
	}
	if _, err = s.VersionFile("root:品牌项目", v1); err == nil {
		t.Fatal("opened rewritten bytes as old version")
	}
	if _, err = s.SetWorkUsable("root:品牌项目", workID, v1, r.Revision, "confirm-old-again"); !errors.Is(err, versions.ErrContentChanged) {
		t.Fatal(err)
	}
	r, err = s.SetWorkUsable("root:品牌项目", workID, v2, r.Revision, "confirm-two")
	if err != nil {
		t.Fatal(err)
	}
	impact, err := s.PreviewVersionRegroup("root:品牌项目", []string{v2}, "")
	if err != nil || len(impact.ClearsUsable) != 1 || impact.ClearsUsable[0] != workID {
		t.Fatal(impact, err)
	}
	r, err = s.RegroupVersions("root:品牌项目", []string{v2}, "", "独立交付", impact.Revision, "split-two")
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || len(list.Items) != 2 {
		t.Fatal(list, err)
	}
	for _, w := range list.Items {
		if w.CurrentUsableVersionID != "" {
			t.Fatal("regroup silently kept usable marker", w)
		}
	}
	restarted := &ShelfService{}
	ack, err := restarted.VersionOperation("root:品牌项目", "register-one")
	if err != nil || !ack.Found || ack.Receipt.EntityIDs[2] != v1 {
		t.Fatal(ack, err)
	}
	if _, err = restarted.RegisterVersion(p.Token, "register-two"); err == nil {
		t.Fatal("restart accepted nonpersistent preview")
	}
	data, _ := os.ReadFile(file2)
	if string(data) != "version two" {
		t.Fatal("metadata operations changed source bytes")
	}
}

func TestVersionPreviewExpiryScopeDriftAndChangedContent(t *testing.T) {
	for _, mode := range []string{"expired", "configuration", "content", "symlink", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			s, cfg, project, configPath := versionFixture(t)
			file := versionFixtureFile(t, project, "draft.txt", "before")
			p := versionPreview(t, s, file, "")
			switch mode {
			case "expired":
				s.versions.mu.Lock()
				s.versions.entries[p.Token].expires = time.Now().Add(-time.Second)
				s.versions.mu.Unlock()
			case "configuration":
				cfg.Timezone = "UTC"
				writeVersionConfig(t, configPath, cfg)
			case "content":
				if err := os.WriteFile(file, []byte("after"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := versionFixtureFile(t, project, "other.txt", "other")
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, file); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.MkdirAll(filepath.Dir(versionCatalogPath(cfg)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(versionCatalogPath(cfg), []byte("broken catalog"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RegisterVersion(p.Token, "changed-preview"); err == nil {
				t.Fatal("invalidated preview committed")
			}
			data, err := os.ReadFile(versionCatalogPath(cfg))
			if mode == "corrupt" {
				if err != nil || string(data) != "broken catalog" {
					t.Fatal("corruption overwritten")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed registration persisted metadata", err)
			}
		})
	}
}

func TestVersionRegistrationRetryAndConcurrentRevisions(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	file := versionFixtureFile(t, project, "draft.txt", "contents")
	p := versionPreview(t, s, file, "")
	second := versionPreview(t, s, file, "")
	var group sync.WaitGroup
	receipts := make(chan versions.Receipt, 6)
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			r, err := s.RegisterVersion(p.Token, "same-request")
			if err != nil {
				t.Error(err)
				return
			}
			receipts <- r
		}()
	}
	group.Wait()
	close(receipts)
	first := ""
	for r := range receipts {
		if first == "" {
			first = r.EntityIDs[2]
		}
		if r.Revision != 1 || r.EntityIDs[2] != first {
			t.Fatal("retry duplicated registration", r)
		}
	}
	if _, err := s.RegisterVersion(p.Token, "different-request"); err == nil {
		t.Fatal("same preview accepted changed operation")
	}
	if _, err := s.RegisterVersion(second.Token, "other-preview"); !errors.Is(err, versions.ErrConflict) {
		t.Fatal(err)
	}
	list, err := s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || list.Total != 1 || list.Items[0].Versions != 1 {
		t.Fatal(list, err)
	}
}

func TestVersionProjectHiddenSensitiveAndForeignWorkBoundaries(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	for _, name := range []string{".env", ".codex/sessions/session.json", ".git/config", "node_modules/package/data.txt", "资料/敏感/客户.txt"} {
		file := versionFixtureFile(t, project, name, "fixture only")
		if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{file}, IncludeHidden: true}); err == nil {
			t.Fatal("protected path accepted", name)
		}
	}
	hidden := versionFixtureFile(t, project, ".draft.txt", "ordinary hidden draft")
	if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{hidden}}); err == nil {
		t.Fatal("hidden file registered without explicit selection")
	}
	if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{hidden}, IncludeHidden: true}); err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(cfg.Root, "其他项目")
	foreign := versionFixtureFile(t, otherDir, "foreign.txt", "outside selected project")
	if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{foreign}}); err == nil {
		t.Fatal("other project read")
	}
	if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:../其他项目", Paths: []string{foreign}}); err == nil {
		t.Fatal("escaped project key")
	}
	p, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:其他项目", Paths: []string{foreign}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RegisterVersion(p.Token, "foreign-register")
	if err != nil {
		t.Fatal(err)
	}
	normal := versionFixtureFile(t, project, "normal.txt", "normal")
	if _, err = s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{normal}, WorkID: r.EntityIDs[1]}); err == nil {
		t.Fatal("foreign work accepted")
	}
	ack, err := s.VersionOperation("root:品牌项目", "foreign-register")
	if err != nil || ack.Found {
		t.Fatal("foreign receipt exposed", ack, err)
	}
	if _, err = s.SetWorkUsable("root:品牌项目", r.EntityIDs[1], r.EntityIDs[2], r.Revision, "foreign-confirm"); err == nil {
		t.Fatal("foreign version confirmed")
	}
}

func TestVersionSearchAndPaginationCoverUnshownRecords(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	for i := 0; i < 53; i++ {
		file := versionFixtureFile(t, project, fmt.Sprintf("item-%02d.txt", i), "item")
		preview, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RegisterVersion(preview.Token, fmt.Sprintf("item-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ProjectWorks("root:品牌项目", "", 0)
	if err != nil || first.Total != 53 || len(first.Items) != 50 {
		t.Fatal(first, err)
	}
	last, err := s.ProjectWorks("root:品牌项目", "", 50)
	if err != nil || last.Total != 53 || len(last.Items) != 3 {
		t.Fatal(last, err)
	}
	found, err := s.ProjectWorks("root:品牌项目", "item-00.txt", 0)
	if err != nil || found.Total != 1 || len(found.Items) != 1 {
		t.Fatal("search missed nonvisible work", found, err)
	}
	paths := []string{}
	for i := 0; i < 45; i++ {
		paths = append(paths, versionFixtureFile(t, project, fmt.Sprintf("batch-%02d.txt", i), "batch"))
	}
	preview, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: "root:品牌项目", Paths: paths, WorkID: found.Items[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterVersion(preview.Token, "batch-versions"); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, 20, 40} {
		page, err := s.WorkVersions("root:品牌项目", found.Items[0].ID, offset)
		expected := 20
		if offset == 40 {
			expected = 6
		}
		if err != nil || page.Total != 46 || len(page.Items) != expected {
			t.Fatal(page, err)
		}
	}
	if s.CancelVersionPreview("finished-request") {
		t.Fatal("nonexistent preview reported cancelled")
	}
}

func TestDiscardVersionPreviewCannotCommit(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	file := versionFixtureFile(t, project, "draft.txt", "draft")
	p := versionPreview(t, s, file, "")
	if !s.DiscardRegisterPreview(p.Token) || s.DiscardRegisterPreview(p.Token) {
		t.Fatal("discard result inaccurate")
	}
	if _, err := s.RegisterVersion(p.Token, "after-discard"); err == nil {
		t.Fatal("discarded preview committed")
	}
	if _, err := os.Stat(versionCatalogPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discard wrote catalog", err)
	}
}

func TestVersionInPlaceExactMembershipAndRevocation(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	loose := filepath.Join(cfg.Root, "散落区")
	member := versionFixtureFile(t, loose, "member.txt", "member")
	sibling := versionFixtureFile(t, loose, "sibling.txt", "not a member")
	reg := registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "fixture-project", Name: "品牌项目", Path: project, Members: []registry.Member{{Path: member, Kind: registry.KindFile}}}}}
	path := filepath.Join(registry.Dir(cfg), registry.FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	save := func() {
		data, err := json.Marshal(reg)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	key := "place:fixture-project"
	if _, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: key, Paths: []string{sibling}}); err == nil {
		t.Fatal("exact file membership granted siblings")
	}
	p, err := s.PreviewRegisterVersion(VersionPreviewInput{ProjectKey: key, Paths: []string{member}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RegisterVersion(p.Token, "member-register")
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.SetWorkUsable(key, r.EntityIDs[1], r.EntityIDs[2], r.Revision, "member-confirm")
	if err != nil {
		t.Fatal(err)
	}
	reg.Projects[0].Members = nil
	save()
	list, err := s.ProjectWorks(key, "", 0)
	if err != nil || list.Items[0].Usable || list.Items[0].UsableState != "restricted" {
		t.Fatal("stale location acted as authorization", list, err)
	}
	if _, err = s.VersionFile(key, list.Items[0].CurrentUsableVersionID); err == nil {
		t.Fatal("revoked location opened")
	}
}
