package versions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func previewFile(t *testing.T, root, name, text, role string) RegisterItem {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := Fingerprint(context.Background(), root, path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return RegisterItem{Path: path, Root: root, Content: content, Role: role}
}
func registration(t *testing.T, s *Store) (RegisterRequest, Receipt) {
	t.Helper()
	root := t.TempDir()
	item := previewFile(t, root, "draft.txt", "v1", "draft")
	req := RegisterRequest{Project: ProjectInput{Key: "root:项目", Name: "项目", Path: root}, Title: "方案", Items: []RegisterItem{item}}
	r, err := s.Register(context.Background(), 0, "register-v1", req, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return req, r
}
func allowAll(Location) error { return nil }

func TestRegisterConfirmNewDraftAndActualChange(t *testing.T) {
	s, _ := testStore(t)
	req, r := registration(t, s)
	projectID, workID, v1 := r.EntityIDs[0], r.EntityIDs[1], r.EntityIDs[2]
	confirmed, err := s.SetUsable(context.Background(), r.Revision, "usable-v1", UsableRequest{ProjectID: projectID, WorkID: workID, VersionID: v1}, allowAll, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	req.WorkID = workID
	req.Items = []RegisterItem{previewFile(t, req.Project.Path, "draft2.txt", "v2", "draft")}
	second, err := s.Register(context.Background(), confirmed.Revision, "register-v2", req, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Works[0].CurrentUsableVersionID != v1 || len(c.Versions) != 2 || len(c.Provenance) != 0 {
		t.Fatal("new draft replaced usable or invented provenance", c)
	}
	state, err := c.InspectVersion(context.Background(), projectID, v1, allowAll, &ReadBudget{Remaining: 1 << 20})
	if err != nil || !state.Usable {
		t.Fatal(state, err)
	}
	if err = os.WriteFile(c.Locations[0].Path, []byte("overwritten"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = c.InspectVersion(context.Background(), projectID, v1, allowAll, &ReadBudget{Remaining: 1 << 20})
	if err != nil || state.Usable || !state.RecordedUsable || state.State != "changed" || len(state.Confirmations) != 1 {
		t.Fatal(state, err)
	}
	_, err = s.SetUsable(context.Background(), second.Revision, "confirm-changed", UsableRequest{ProjectID: projectID, WorkID: workID, VersionID: v1}, allowAll, 1<<20)
	if !errors.Is(err, ErrContentChanged) {
		t.Fatal(err)
	}
	after, _ := s.Read(context.Background())
	if after.Revision != second.Revision || after.Works[0].CurrentUsableVersionID != v1 {
		t.Fatal("failed confirmation rewrote history")
	}
}

func TestRegistrationReplayBatchAndDuplicatePath(t *testing.T) {
	s, _ := testStore(t)
	req, r := registration(t, s)
	replay, err := s.Register(context.Background(), 0, "register-v1", req, 1<<20)
	if err != nil || replay.Revision != r.Revision {
		t.Fatal(replay, err)
	}
	req.WorkID = r.EntityIDs[1]
	again, err := s.Register(context.Background(), r.Revision, "same-file-again", req, 1<<20)
	if err != nil || again.EntityIDs[2] != r.EntityIDs[2] {
		t.Fatal(again, err)
	}
	c, _ := s.Read(context.Background())
	if len(c.Versions) != 1 || len(c.Works[0].VersionIDs) != 1 {
		t.Fatal("same path created redundant version")
	}
	a := previewFile(t, req.Project.Path, "a.txt", "a", "source")
	b := previewFile(t, req.Project.Path, "b.txt", "b", "export")
	req.Items = []RegisterItem{a, b}
	if err = os.WriteFile(b.Path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Register(context.Background(), again.Revision, "changed-batch", req, 1<<20)
	if !errors.Is(err, ErrContentChanged) {
		t.Fatal(err)
	}
	c, _ = s.Read(context.Background())
	if len(c.Versions) != 1 || c.Revision != again.Revision {
		t.Fatal("partial batch persisted")
	}
	req.Items = []RegisterItem{a, a}
	if _, err = s.Register(context.Background(), again.Revision, "duplicate-selection", req, 1<<20); err == nil {
		t.Fatal("duplicate selection accepted")
	}
}

func TestStableProjectBindingAndCrossProjectRejection(t *testing.T) {
	s, _ := testStore(t)
	req, r := registration(t, s)
	req.WorkID = r.EntityIDs[1]
	req.Project.Key = "place:new-key-same-path"
	alias, err := s.Register(context.Background(), r.Revision, "alias", req, 1<<20)
	if err != nil || alias.EntityIDs[0] != r.EntityIDs[0] {
		t.Fatal(alias, err)
	}
	req.Project.Key = "root:项目"
	req.Project.Path = t.TempDir()
	if _, err = s.Register(context.Background(), alias.Revision, "moved-project", req, 1<<20); !errors.Is(err, ErrProjectLocationChanged) {
		t.Fatal(err)
	}
	req.Project.Key = "root:other"
	req.Project.Name = "other"
	if _, err = s.Register(context.Background(), alias.Revision, "cross-project", req, 1<<20); err == nil {
		t.Fatal("foreign work accepted")
	}
	if _, err = s.SetUsable(context.Background(), alias.Revision, "foreign-confirmation", UsableRequest{ProjectID: "foreign", WorkID: r.EntityIDs[1], VersionID: r.EntityIDs[2]}, allowAll, 1<<20); err == nil {
		t.Fatal("foreign confirmation accepted")
	}
}

func TestRegroupExportAndUserSourceLeaveBytesUnchanged(t *testing.T) {
	s, _ := testStore(t)
	req, r := registration(t, s)
	projectID, workID, v1 := r.EntityIDs[0], r.EntityIDs[1], r.EntityIDs[2]
	confirmation, err := s.SetUsable(context.Background(), r.Revision, "confirm", UsableRequest{ProjectID: projectID, WorkID: workID, VersionID: v1}, allowAll, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	item := previewFile(t, req.Project.Path, "export.txt", "v1", "export")
	req.WorkID = workID
	req.Items = []RegisterItem{item}
	r, err = s.Register(context.Background(), confirmation.Revision, "register-export", req, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	v2 := r.EntityIDs[2]
	if v2 == v1 {
		t.Fatal("same bytes different role merged")
	}
	r, err = s.SetExportRelation(context.Background(), r.Revision, "export-link", ExportRelationRequest{ProjectID: projectID, VersionID: v2, ParentVersionID: v1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetExportRelation(context.Background(), r.Revision, "cycle", ExportRelationRequest{ProjectID: projectID, VersionID: v1, ParentVersionID: v2}); err == nil {
		t.Fatal("cycle accepted")
	}
	r, err = s.AddUserNote(context.Background(), r.Revision, "note", NoteRequest{ProjectID: projectID, VersionID: v2, Note: UserNote{Tool: "用户记得来自工具 A"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Regroup(context.Background(), r.Revision, "split", RegroupRequest{ProjectID: projectID, VersionIDs: []string{v1}, Title: "独立作品"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Read(context.Background())
	if c.Works[0].CurrentUsableVersionID != "" || c.Works[1].CurrentUsableVersionID != "" || len(c.Confirmations) != 1 || len(c.Relations) != 1 || c.Provenance[0].Kind != "user_note" || c.Provenance[0].Agent != "" {
		t.Fatal(c)
	}
	for _, loc := range c.Locations {
		if string(mustReadFile(t, loc.Path)) != "v1" {
			t.Fatal("regroup changed bytes")
		}
	}
	if len(c.Works[0].VersionIDs) != 1 || len(c.Works[1].VersionIDs) != 1 {
		t.Fatal("membership lost")
	}
}

func TestAvailabilityLimitsMissingAndRevokedGrant(t *testing.T) {
	s, _ := testStore(t)
	_, r := registration(t, s)
	c, _ := s.Read(context.Background())
	projectID, v := r.EntityIDs[0], r.EntityIDs[2]
	state, err := c.InspectVersion(context.Background(), projectID, v, allowAll, &ReadBudget{Remaining: 0})
	if err != nil || state.State != "unchecked" || state.Usable {
		t.Fatal(state, err)
	}
	state, err = c.InspectVersion(context.Background(), projectID, v, func(Location) error { return errors.New("scope revoked") }, &ReadBudget{Remaining: 1024})
	if err != nil || state.State != "restricted" {
		t.Fatal(state, err)
	}
	if err = os.Remove(c.Locations[0].Path); err != nil {
		t.Fatal(err)
	}
	state, err = c.InspectVersion(context.Background(), projectID, v, allowAll, &ReadBudget{Remaining: 1024})
	if err != nil || state.State != "missing" {
		t.Fatal(state, err)
	}
	if len(c.Versions) != 1 {
		t.Fatal("missing content deleted identity")
	}
}
