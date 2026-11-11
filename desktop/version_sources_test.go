package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

func TestVersionSourcesDistinguishClaimsIndexAndUserNotes(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	file := versionFixtureFile(t, project, "draft.txt", "source content")
	first := versionPreview(t, s, file, "")
	content := first.Items[0].Content
	rows := []map[string]any{
		{"file": "draft.txt", "agent": "fixture-agent", "tool": "FixtureStudio", "task": "生成候选", "sha256": content.SHA256, "time": "2026-11-08T10:00:00-08:00", "parameters": map[string]any{"seed": 42, "model": "fixture-v1"}},
		{"file": "draft.txt", "agent": "old-content", "sha256": strings.Repeat("f", 64)},
		{"file": "../outside", "agent": "unrelated"},
		{"file": "draft.txt", "agent": "filename-claim"},
	}
	var sidecar bytes.Buffer
	for _, row := range rows {
		data, _ := json.Marshal(row)
		sidecar.Write(data)
		sidecar.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(project, activity.SidecarName), sidecar.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.IndexShards, cfg.Names.IndexFile+".fixture.csv")
	rel, _ := filepath.Rel(cfg.Root, file)
	if _, err := put.AppendRows(index, []put.Row{{Project: "品牌项目", Task: "交付登记", Path: filepath.ToSlash(rel), Agent: "index-agent", Status: "已交", Source: file, Size: content.Size, SHA256: content.SHA256, Time: "2026-11-08T11:00:00-08:00"}}); err != nil {
		t.Fatal(err)
	}
	preview := versionPreview(t, s, file, "")
	if len(preview.Items[0].Provenance) != 3 || len(preview.Warnings) == 0 {
		t.Fatal(preview)
	}
	preview.Items[0].Provenance[0].Agent = "tampered presentation"
	receipt, err := s.RegisterVersion(preview.Token, "source-register")
	if err != nil {
		t.Fatal(err)
	}
	workID, versionID := receipt.EntityIDs[1], receipt.EntityIDs[2]
	result, err := s.WorkVersions("root:品牌项目", workID, 0)
	if err != nil {
		t.Fatal(err)
	}
	claims := result.Items[0].Provenance
	seen := map[string]versions.Provenance{}
	for _, claim := range claims {
		seen[claim.Agent] = claim
	}
	if seen["fixture-agent"].Kind != "tool_declaration" || seen["fixture-agent"].Binding != "content_match" || seen["fixture-agent"].Tool != "FixtureStudio" || !strings.Contains(seen["fixture-agent"].Parameters, "42") {
		t.Fatal(claims)
	}
	if seen["filename-claim"].Binding != "path_only" || seen["filename-claim"].ClaimedAt != "" || seen["filename-claim"].Tool != "" {
		t.Fatal("missing source details fabricated", claims)
	}
	if seen["index-agent"].Kind != "index_record" || seen["index-agent"].Binding != "content_match" {
		t.Fatal(claims)
	}
	if _, ok := seen["old-content"]; ok {
		t.Fatal("old content claim attached")
	}
	if _, ok := seen["tampered presentation"]; ok {
		t.Fatal("preview metadata mutated server request")
	}
	list, err := s.ProjectWorks("root:品牌项目", "FixtureStudio", 0)
	if err != nil || list.Total != 1 || list.Items[0].Usable || list.Items[0].CurrentUsableVersionID != "" {
		t.Fatal("index approval was promoted to usable", list, err)
	}
	_, err = s.AddVersionSource("root:品牌项目", versionID, versions.UserNote{Source: "用户补充来源"}, receipt.Revision, "user-note")
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.WorkVersions("root:品牌项目", workID, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Items[0].Provenance[3]
	if last.Kind != "user_note" || last.Binding != "user_supplied" || last.Tool != "" || last.Agent != "" {
		t.Fatal(last)
	}
}

func TestVersionSourceUnreadableAndLegacyRecorderAreHonest(t *testing.T) {
	s, cfg, project, _ := versionFixture(t)
	file := versionFixtureFile(t, project, "draft.txt", "source")
	outside := versionFixtureFile(t, t.TempDir(), "fake.jsonl", `{"file":"draft.txt","agent":"must-not-read"}`+"\n")
	if err := os.Symlink(outside, filepath.Join(project, activity.SidecarName)); err != nil {
		t.Fatal(err)
	}
	preview := versionPreview(t, s, file, "")
	if len(preview.Items[0].Provenance) != 0 || len(preview.Warnings) == 0 {
		t.Fatal("sidecar link read or hidden failure", preview)
	}
	indexDir := filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.IndexShards)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var csvData bytes.Buffer
	writer := csv.NewWriter(&csvData)
	_ = writer.Write(put.OldHeader)
	_ = writer.Write([]string{time.Now().Format(time.RFC3339), "品牌项目", "旧任务", "draft.txt", file, file, "6", "", "某位登记者"})
	writer.Flush()
	if err := os.WriteFile(filepath.Join(indexDir, cfg.Names.IndexFile+".legacy.csv"), csvData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	preview = versionPreview(t, s, file, "")
	claims := preview.Items[0].Provenance
	if len(claims) != 1 || claims[0].Agent != "" || claims[0].Recorder != "某位登记者" || claims[0].Binding != "path_only" {
		t.Fatal("legacy person inferred as AI", claims)
	}
}
