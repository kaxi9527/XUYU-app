package main

import "testing"

func TestVersionActionAcknowledgementAndOldUsable(t *testing.T) {
	s, _, project, _ := versionFixture(t)
	path := versionFixtureFile(t, project, "v1.txt", "first")
	preview := versionPreview(t, s, path, "")
	input := VersionActionInput{Kind: "register", ProjectKey: "root:品牌项目", Token: preview.Token, OperationID: "action-register"}
	wrong := input
	wrong.ProjectKey = "root:其他项目"
	if got := s.ApplyVersionAction(wrong); got.Error == "" || got.Receipt != nil || got.Uncertain {
		t.Fatal(got)
	}
	got := s.ApplyVersionAction(input)
	if got.Receipt == nil || got.Error != "" {
		t.Fatal(got)
	}
	workID, versionID := got.Receipt.EntityIDs[1], got.Receipt.EntityIDs[2]
	confirm := s.ApplyVersionAction(VersionActionInput{Kind: "usable", ProjectKey: input.ProjectKey, WorkID: workID, VersionID: versionID, Revision: got.Receipt.Revision, OperationID: "action-usable"})
	if confirm.Receipt == nil {
		t.Fatal(confirm)
	}
	restarted := &ShelfService{}
	replay, replayErr := restarted.VersionOperation(input.ProjectKey, input.OperationID)
	if replayErr != nil || !replay.Found || replay.Receipt.OperationID != input.OperationID {
		t.Fatal(replay, replayErr)
	}
	detail, err := restarted.VersionDetail(input.ProjectKey, versionID)
	if err != nil || !detail.Item.Usable || detail.Revision != confirm.Receipt.Revision {
		t.Fatal(detail, err)
	}
	versionFixtureFile(t, project, "v1.txt", "edited")
	detail, err = restarted.VersionDetail(input.ProjectKey, versionID)
	if err != nil || detail.Item.Usable || detail.Item.State != "changed" {
		t.Fatal(detail, err)
	}
	failed := restarted.ApplyVersionAction(VersionActionInput{Kind: "usable", ProjectKey: input.ProjectKey, WorkID: workID, VersionID: versionID, Revision: detail.Revision, OperationID: "action-changed"})
	if failed.Receipt != nil || failed.Error == "" || failed.Uncertain {
		t.Fatal(failed)
	}
	if _, err = restarted.VersionDetail(input.ProjectKey, "foreign"); err == nil {
		t.Fatal("unknown version accepted")
	}
}
