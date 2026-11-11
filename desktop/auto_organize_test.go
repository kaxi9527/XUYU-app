package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestRegisteredExternalProjectCollectsSessionFilesAndRestores(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(base, "Projects", "design-worktree")
	code := filepath.Join(project, "main.go")
	loose := filepath.Join(base, "Desktop", "设计稿.psd")
	sessions := filepath.Join(base, "sessions")
	for _, dir := range []string{project, filepath.Join(project, ".git"), filepath.Dir(loose), sessions, registry.Dir(cfg)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(code, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := worktype.Profile{Name: "自己的模板", Folders: []string{"资料", "设计成品"}, Roles: map[string]string{"source": "资料"}, Rules: []worktype.Rule{{Kind: "extension", Value: "psd", Folder: "设计成品"}}}
	profileData, _ := json.Marshal(profile)
	if err := os.WriteFile(filepath.Join(project, worktype.ProfileFile), profileData, 0600); err != nil {
		t.Fatal(err)
	}
	reg := registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "existing-external", Name: "品牌/原地项目", Path: project}}}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(registry.Dir(cfg), registry.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Add File: " + code + "\n+package main\n*** Add File: " + loose + "\n+design\n*** End Patch\n"
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "external-session", "cwd": project}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA files\n"}}}
	var lines []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		lines = append(lines, append(b, '\n')...)
	}
	if err = os.WriteFile(filepath.Join(sessions, "new.jsonl"), lines, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || view.Operations[0].ProjectKey != "place:existing-external" || view.Operations[0].Target != project || view.Operations[0].Summary.Moved != 1 {
		t.Fatal(view, err)
	}
	if data, e := os.ReadFile(filepath.Join(project, "设计成品", "设计稿.psd")); e != nil || string(data) != "design" {
		t.Fatal("custom placement not used", e)
	}
	if content, e := os.ReadFile(code); e != nil || string(content) != "package main\n" {
		t.Fatal("existing code moved or changed", e)
	}
	memory, err := s.ProjectMemory("place:existing-external")
	if err != nil || !memory.Enabled {
		t.Fatal(memory, err)
	}
	events, err := s.ProjectMemoryEvents("place:existing-external", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Items {
		if event.Kind == "automatic_project_organized" && event.Auto != nil && len(event.Auto.Items) == 1 && event.Auto.Items[0].Session == "external-session" && strings.Contains(event.Auto.Items[0].PlacementReason, "匹配自定义规则") {
			found = true
		}
	}
	if !found {
		t.Fatal("project memory lacks the session and file")
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if content, e := os.ReadFile(loose); e != nil || string(content) != "design" {
		t.Fatal("loose file not restored", e)
	}
	if content, e := os.ReadFile(code); e != nil || string(content) != "package main\n" {
		t.Fatal("existing code changed", e)
	}
	if _, e := os.Stat(filepath.Join(cfg.Root, "原地项目")); !os.IsNotExist(e) {
		t.Fatal("duplicate project created", e)
	}
}

func TestSessionDiscoversUnregisteredProjectAndRestores(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(base, "Projects", "新发现项目")
	workdir := filepath.Join(project, "src")
	code := filepath.Join(workdir, "main.go")
	loose := filepath.Join(base, "Downloads", "封面.psd")
	sessions := filepath.Join(base, "sessions-discovery")
	for _, dir := range []string{workdir, filepath.Join(project, ".git"), filepath.Dir(loose), sessions, registry.Dir(cfg)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(code, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "Projects", "已有登记")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	initial := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "previous", Name: "已有登记", Path: other}}}
	if _, err := registry.Save(cfg, initial, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Add File: " + code + "\n+package main\n*** Add File: " + loose + "\n+design\n*** End Patch\n"
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "new-external-session", "cwd": workdir}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA files\n"}}}
	var lines []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		lines = append(lines, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(sessions, "new.jsonl"), lines, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	registered := reg.Owner(project)
	if registered == nil || registered.Path != project || !registered.FromSession || reg.ByID("previous") == nil {
		t.Fatal("project discovery lost registry data", reg)
	}
	key := "place:" + registered.ID
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || view.Operations[0].ProjectKey != key || view.Operations[0].Target != project || view.Operations[0].Summary.Moved != 1 {
		t.Fatal(view, err)
	}
	if data, e := os.ReadFile(code); e != nil || string(data) != "package main\n" {
		t.Fatal("project code changed", e)
	}
	memory, err := s.ProjectMemory(key)
	if err != nil || !memory.Enabled {
		t.Fatal(memory, err)
	}
	graph, err := s.ProjectMemoryGraph(key, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range graph.Nodes {
		if node.Name == "new-external-session" {
			found = true
		}
	}
	if !found {
		t.Fatal("session missing from project graph")
	}
	if _, err = registry.Save(cfg, &registry.Registry{Version: registry.Version}, base); err != nil {
		t.Fatal(err)
	}
	refreshed, err := registry.Load(cfg)
	if err != nil || refreshed.ByID(registered.ID) == nil {
		t.Fatal("session project lost on refresh", err)
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(loose); e != nil || string(data) != "design" {
		t.Fatal("file not restored", e)
	}
	if data, e := os.ReadFile(code); e != nil || string(data) != "package main\n" {
		t.Fatal("project code changed", e)
	}
	if _, err = os.Stat(filepath.Join(cfg.Root, "新发现项目")); !os.IsNotExist(err) {
		t.Fatal("duplicate project created", err)
	}
}

func TestGeminiNewSessionAutomaticAgentFlow(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	source := filepath.Join(base, "Desktop")
	sessions := filepath.Join(base, "gemini", "tmp")
	projectRoot := filepath.Join(base, "Projects", "品牌设计")
	for _, dir := range []string{source, projectRoot, sessions} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "gemini", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	s.autoOrganize.interval = 200 * time.Millisecond
	s.startAutoOrganize()
	defer s.closeAutoOrganize()
	file := filepath.Join(source, "draft.psd")
	if err := os.WriteFile(file, []byte("gemini output"), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sessions, "project-hash", "chats", "session-new.jsonl")
	if err := os.MkdirAll(filepath.Dir(log), 0700); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"sessionId": "gem-session", "projectHash": sourceevidence.Hash([]byte(projectRoot))})
	message, _ := json.Marshal(map[string]any{"id": "message-1", "type": "gemini", "timestamp": "2026-11-09T10:00:00Z", "toolCalls": []any{map[string]any{"id": "call-1", "name": "write_file", "status": "success", "args": map[string]any{"file_path": "../../Desktop/draft.psd", "content": "gemini output"}}}})
	if err := os.WriteFile(log, append(append(append(meta, '\n'), message...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	var view AutoOrganizeView
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		view, _ = s.AutoOrganizeStatus()
		if len(view.Operations) == 1 && view.Operations[0].MemoryInitialized {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(view.Operations) != 1 || !view.Operations[0].MemoryInitialized || view.Operations[0].Status != "organized" {
		t.Fatal("new Gemini session not organized", view)
	}
	memory, err := s.ProjectMemory("root:品牌设计")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:品牌设计", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_organized" && e.Auto != nil && len(e.Auto.Items) == 1 && e.Auto.Items[0].Agent == "gemini" && e.Auto.Items[0].Session == "gem-session" && e.Auto.Items[0].File != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("Gemini origin missing", events)
	}
	graph, err := s.ProjectMemoryGraph("root:品牌设计", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, n := range graph.Nodes {
		if n.Kind == "memory_source" && n.Name == "gemini" {
			found = true
		}
	}
	if !found {
		t.Fatal("Gemini relation missing")
	}
	o, err := s.RejectAutoOrganize(view.Operations[0].ID)
	if err != nil || o.Status != "restored" {
		t.Fatal(o, err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "gemini output" {
		t.Fatal("file not restored", err)
	}
}

func TestInterruptedAutoOrganizationRebuildsProjectMemory(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "interrupted.psd")
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	p := autoorganize.Policy{Enabled: true, Roots: []string{base}}
	if err := autoorganize.SavePolicy(cfg, p); err != nil {
		t.Fatal(err)
	}
	c := autoorganize.Candidate{Project: "中断后恢复", WorkType: "设计·UI原型", Files: []string{file}, Signals: []autoorganize.FileSignal{{Path: file, Agent: "codex", Session: "session-interrupted"}}}
	o, err := autoorganize.Execute(context.Background(), cfg, base, p, c)
	if err != nil {
		t.Fatal(err)
	}
	// The process stopped after moving the file, before saving its batch result.
	o.Status, o.Summary = "organizing", nil
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(autoorganize.ReceiptPath(cfg, o.ID), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	memory, err := s.ProjectMemory("root:" + c.Project)
	if err != nil || memory.Tracked == 0 {
		t.Fatal(memory, err)
	}
	events, err := s.ProjectMemoryEvents("root:"+c.Project, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_organized" && e.Auto != nil && len(e.Auto.Items) == 1 && e.Auto.Items[0].Agent == "codex" && e.Auto.Items[0].File != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("recovered file origin missing from memory", events)
	}
	result, err := s.RejectAutoOrganize(o.ID)
	if err != nil || result.Status != "restored" {
		t.Fatal(result, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file not restored", err)
	}
}

func TestWorkerObjectionAfterInterruptedAppRestoresAndWritesMemory(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "offline-agent.psd")
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	p := autoorganize.Policy{Enabled: true, Roots: []string{base}}
	if err := autoorganize.SavePolicy(cfg, p); err != nil {
		t.Fatal(err)
	}
	c := autoorganize.Candidate{Project: "离线恢复", WorkType: "设计·UI原型", Files: []string{file}, Signals: []autoorganize.FileSignal{{Path: file, Agent: "codex", Session: "offline-session"}}}
	o, err := autoorganize.Execute(context.Background(), cfg, base, p, c)
	if err != nil {
		t.Fatal(err)
	}
	o.Status, o.Summary = "organizing", nil
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(autoorganize.ReceiptPath(cfg, o.ID), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = autoorganize.RequestObjection(cfg, o.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	// The worker command restores immediately, before the desktop app restarts.
	if restored, err := autoorganize.RestoreForObjection(cfg, o.ID); err != nil || restored.Status != "restored" {
		t.Fatal(restored, err)
	}
	p.Enabled = false
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(o.Target); !os.IsNotExist(err) {
		t.Fatal("temporary project scaffold was not removed after memory sync", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file not restored", err)
	}
	requests, err := autoorganize.PendingObjections(cfg)
	if err != nil || len(requests) != 0 {
		t.Fatal("objection not completed", requests, err)
	}
	memory, err := s.ProjectMemory("root:" + c.Project)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:"+c.Project, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	organized, restored := false, false
	for _, e := range events.Items {
		if e.Auto == nil || len(e.Auto.Items) != 1 || e.Auto.Items[0].Agent != "codex" {
			continue
		}
		if e.Kind == "automatic_project_organized" {
			organized = true
		}
		if e.Kind == "automatic_project_restored" && e.Auto.RequestedBy == "claude" {
			restored = true
		}
	}
	if !organized || !restored {
		t.Fatal("offline operation history incomplete", events)
	}
}

func TestUserCanRejectBeforeInterruptedMemoryWasInitialized(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "early-reject.psd")
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	p := autoorganize.Policy{Enabled: true, Roots: []string{base}}
	if err := autoorganize.SavePolicy(cfg, p); err != nil {
		t.Fatal(err)
	}
	c := autoorganize.Candidate{Project: "直接反对", WorkType: "设计·UI原型", Files: []string{file}, Signals: []autoorganize.FileSignal{{Path: file, Agent: "codex"}}}
	o, err := autoorganize.Execute(context.Background(), cfg, base, p, c)
	if err != nil {
		t.Fatal(err)
	}
	o.Status, o.Summary = "organizing", nil
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(autoorganize.ReceiptPath(cfg, o.ID), b, 0600); err != nil {
		t.Fatal(err)
	}
	o, err = s.RejectAutoOrganize(o.ID)
	if err != nil || o.Status != "restored" || !o.MemoryInitialized {
		t.Fatal(o, err)
	}
	if _, err = os.Stat(o.Target); !os.IsNotExist(err) {
		t.Fatal("empty project remained", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file not restored", err)
	}
	memory, err := s.ProjectMemory("root:" + c.Project)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:"+c.Project, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_restored" && e.Auto != nil && e.Auto.RequestedBy == "用户" && len(e.Auto.Items) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("direct objection missing from project history", events)
	}
}

func TestAutoOrganizeSwitchWorkerAndObjection(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "自动稿.psd")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	record := activity.Record{Path: file, Project: "自动项目", WorkType: "设计·UI原型", Agent: "codex", Time: time.Now().Format(time.RFC3339)}
	if err := activity.Append(activity.Dir(cfg), time.UTC, record); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("disabled worker moved file", err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	s.startAutoOrganize()
	defer s.closeAutoOrganize()
	deadline := time.Now().Add(8 * time.Second)
	var view AutoOrganizeView
	var err error
	for time.Now().Before(deadline) {
		view, err = s.AutoOrganizeStatus()
		if err == nil && len(view.Operations) == 1 && view.Operations[0].MemoryInitialized {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" {
		t.Fatal(view, err)
	}
	if !view.Operations[0].MemoryInitialized {
		t.Fatal("project memory not initialized")
	}
	memory, err := s.ProjectMemory("root:自动项目")
	if err != nil || !memory.Enabled || !memory.Automatic || memory.Tracked == 0 {
		t.Fatal("project memory missing", memory, err)
	}
	events, err := s.ProjectMemoryEvents("root:自动项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_organized" && e.Auto != nil && len(e.Auto.Items) == 1 && e.Auto.Items[0].Agent == "codex" && e.Auto.Items[0].File != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("automatic receipt or linked source missing from project memory", events)
	}
	graph, err := s.ProjectMemoryGraph("root:自动项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, n := range graph.Nodes {
		if n.Kind == "memory_source" && n.Name == "codex" {
			linked = true
		}
	}
	if !linked {
		t.Fatal("agent not linked in project graph")
	}
	result, err := s.RejectAutoOrganize(view.Operations[0].ID)
	if err != nil || result.Status != "restored" {
		t.Fatal(result, err)
	}
	memory, err = s.ProjectMemory("root:自动项目")
	if err != nil {
		t.Fatal(err)
	}
	events, err = s.ProjectMemoryEvents("root:自动项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_restored" && e.Auto != nil && e.Auto.Status == "restored" {
			found = true
		}
	}
	if !found {
		t.Fatal("opposition restoration missing from project history")
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "draft" {
		t.Fatal("file was not restored", err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("rejected operation re-ran", err)
	}
	if _, err := s.SetAutoOrganize(false, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAutoOrganizeShowsWhyProjectFilesStay(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(base, "existing-project")
	file := filepath.Join(project, "references", "design.png")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(project, "package.json"): `{"name":"project"}`, file: "image"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: file, Project: "自动项目", WorkType: "设计·UI原型", Agent: "codex", Time: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	s.startAutoOrganize()
	defer s.closeAutoOrganize()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		view, err := s.AutoOrganizeStatus()
		if err == nil && strings.Contains(view.Error, "保持完整工程的内部结构") {
			if data, e := os.ReadFile(file); e != nil || string(data) != "image" {
				t.Fatal("protected project file changed", e)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("background status did not explain why the file stayed")
}

func TestUnidentifiedSessionIsGroupedAndAssignedOnce(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	source := filepath.Join(base, "Desktop")
	sessions := filepath.Join(base, "sessions")
	os.MkdirAll(source, 0700)
	os.MkdirAll(sessions, 0700)
	files := []string{filepath.Join(source, "图一.png"), filepath.Join(source, "图二.png")}
	for _, p := range files {
		if err := os.WriteFile(p, []byte("image\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	patch := "*** Begin Patch\n*** Add File: " + files[0] + "\n+image\n*** Add File: " + files[1] + "\n+image\n*** End Patch\n"
	lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "unknown-session", "cwd": base}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call-1", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call-1", "output": "Success. Updated the following files:\nA files\n"}}}
	var data []byte
	for _, line := range lines {
		b, _ := json.Marshal(line)
		data = append(data, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(sessions, "session.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Unassigned) != 1 || view.Unassigned[0].Count != 2 || len(view.Unassigned[0].Examples) != 2 || len(view.Operations) != 0 {
		t.Fatal("session was not grouped", view, err)
	}
	existing := filepath.Join(base, "Projects", "自选项目", "原有文件.txt")
	if err = os.MkdirAll(filepath.Dir(existing), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(existing, []byte("原有内容"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := &registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "user-selected", Name: "已有/自选项目", Path: filepath.Dir(existing)}}}
	if _, err = registry.Save(cfg, reg, base); err != nil {
		t.Fatal(err)
	}
	view, err = s.ConfirmAutoSession(view.Unassigned[0].ID, "已有/自选项目", "设计·UI原型")
	if err != nil || len(view.Operations) != 1 || view.Operations[0].CreatedProject || view.Operations[0].ProjectKey != "place:user-selected" || view.Operations[0].Summary.Moved != 2 {
		t.Fatal(view, err)
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if _, err = os.Stat(p); err != nil {
			t.Fatal("file was not restored", p, err)
		}
	}
	if content, err := os.ReadFile(existing); err != nil || string(content) != "原有内容" {
		t.Fatal("existing project changed", err)
	}
}

func TestAutomaticCandidatesVisibleBeforeEnabling(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	source := filepath.Join(base, "Desktop", "preview.psd")
	sessions := filepath.Join(base, "sessions")
	for _, dir := range []string{filepath.Dir(source), sessions} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("preview"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := activity.Mark(activity.MarkOptions{Config: cfg, Files: []string{source}, Agent: "codex", Project: "预览项目", WorkType: "设计·UI原型"}); err != nil {
		t.Fatal(err)
	}
	view, err := s.SetAutoOrganize(false, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}})
	if err != nil || view.Policy.Enabled || view.ProjectRoot != cfg.Root || len(view.Candidates) != 1 || view.Candidates[0].Project != "预览项目" || len(view.Candidates[0].Files) != 1 {
		t.Fatalf("observation preview: %+v %v", view, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("preview moved the source", err)
	}
	ops, err := autoorganize.List(cfg)
	if err != nil || len(ops) != 0 {
		t.Fatal("preview created an operation", ops, err)
	}
}

func TestSessionDiscoveryWithoutAutomaticPlacement(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	source := filepath.Join(base, "Desktop")
	sessions := filepath.Join(base, "sessions")
	cwd := filepath.Join(base, "Projects", "观察项目")
	for _, dir := range []string{source, sessions, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	sources := []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}
	if _, err := s.SetAutoOrganize(false, []string{base}, sources); err != nil {
		t.Fatal(err)
	}
	writeSession := func(name string) string {
		t.Helper()
		path := filepath.Join(source, name+".psd")
		if err := os.WriteFile(path, []byte("artwork\n"), 0600); err != nil {
			t.Fatal(err)
		}
		lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": name, "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": "*** Begin Patch\n*** Add File: " + path + "\n+artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + path + "\n"}}}
		var data []byte
		for _, line := range lines {
			b, _ := json.Marshal(line)
			data = append(data, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(sessions, name+".jsonl"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	first := writeSession("first")
	s.autoOrganize.interval = 100 * time.Millisecond
	s.startAutoOrganize()
	defer s.closeAutoOrganize()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		view, err := s.AutoOrganizeStatus()
		if err == nil && view.SessionFileCount == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	policy, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := autoorganize.ReadSessionEvidence(cfg, policy)
	if err != nil || len(records) != 1 {
		t.Fatalf("authorized session was not discovered: records=%d err=%v", len(records), err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || view.SessionFileCount != 1 {
		t.Fatalf("session status missing: %+v %v", view, err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("file moved while automatic placement was disabled: %v", err)
	}
	second := writeSession("second")
	until = time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		view, err := s.AutoOrganizeStatus()
		if err == nil && view.SessionFileCount == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	records, _, err = autoorganize.ReadSessionEvidence(cfg, policy)
	if err != nil || len(records) != 2 {
		t.Fatalf("new authorized session was not discovered: records=%d err=%v", len(records), err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("new file moved while automatic placement was disabled: %v", err)
	}
	ops, err := autoorganize.List(cfg)
	if err != nil || len(ops) != 0 {
		t.Fatalf("unexpected automatic placement: %+v %v", ops, err)
	}
	if _, err := s.SetAutoOrganize(false, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	writeSession("third")
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, _, err = autoorganize.ReadSessionEvidence(cfg, policy)
	if err != nil || len(records) != 2 {
		t.Fatalf("session was read after authorization was removed: records=%d err=%v", len(records), err)
	}
}

func TestLegacyDisabledSessionPolicyDoesNotRead(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	sessions := filepath.Join(base, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	policy := autoorganize.Policy{Roots: []string{base}, Sources: []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}}
	if err := autoorganize.SavePolicy(cfg, policy); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "legacy.psd")
	if err := os.WriteFile(path, []byte("artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "legacy", "cwd": base}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": "*** Begin Patch\n*** Add File: " + path + "\n+artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + path + "\n"}}}
	var data []byte
	for _, line := range lines {
		b, _ := json.Marshal(line)
		data = append(data, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(sessions, "new.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, issues, err := autoorganize.ReadSessionEvidence(cfg, policy)
	if err != nil || len(records) != 0 || len(issues) != 0 {
		t.Fatalf("legacy disabled policy read sessions: %v %v %v", records, issues, err)
	}
}

func TestSessionObjectionConsentIsBoundToSavedSources(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	first, second := filepath.Join(base, "sessions-a"), filepath.Join(base, "sessions-b")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := []autoorganize.SessionSource{{Adapter: "codex", Path: first}}
	if err := autoorganize.SavePolicy(cfg, autoorganize.Policy{Roots: []string{base}, Sources: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoObjections(true); err == nil {
		t.Fatal("legacy disabled source unexpectedly authorized messages")
	}
	if _, err := s.SetAutoOrganize(false, []string{base}, a); err != nil {
		t.Fatal(err)
	}
	view, err := s.SetAutoObjections(true)
	if err != nil || view.Policy.ObjectionEnabledAt == "" {
		t.Fatal(view, err)
	}
	stamp := view.Policy.ObjectionEnabledAt
	view, err = s.SetAutoOrganize(true, []string{base}, a)
	if err != nil || view.Policy.ObjectionEnabledAt != stamp {
		t.Fatal("unchanged scope lost consent", view, err)
	}
	view, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: second}})
	if err != nil || view.Policy.ObjectionEnabledAt != "" {
		t.Fatal("changed source retained consent", view, err)
	}
}

func TestOptInSessionObjectionRestoresAutomaticOperation(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	sessions := filepath.Join(base, "sessions")
	cwd := filepath.Join(base, "Projects", "会话项目")
	source := filepath.Join(base, "Desktop", "作品.psd")
	for _, dir := range []string{sessions, cwd, filepath.Dir(source)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(sessions, "session.jsonl")
	appendRows := func(rows ...any) {
		t.Helper()
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, row := range rows {
			b, e := json.Marshal(row)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write(append(b, '\n')); e != nil {
				t.Fatal(e)
			}
		}
	}
	appendRows(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "session-1", "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": "*** Begin Patch\n*** Add File: " + source + "\n+artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + source + "\n"}})
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" {
		t.Fatal("automatic organization failed", view, err)
	}
	message := func() any {
		return map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "撤销这次整理"}}}}
	}
	appendRows(message())
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, _ = s.AutoOrganizeStatus()
	if view.Operations[0].Status != "organized" {
		t.Fatal("message read without consent", view.Operations[0].Status)
	}
	if _, err = s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjections(false); err != nil {
		t.Fatal(err)
	}
	appendRows(message())
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, _ = s.AutoOrganizeStatus()
	if view.Operations[0].Status != "organized" {
		t.Fatal("message read after consent was revoked", view.Operations[0].Status)
	}
	if _, err = s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(false, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	appendRows(message())
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = s.AutoOrganizeStatus()
	if err != nil || view.Operations[0].Status != "restored" {
		t.Fatal("explicit objection did not restore", view, err)
	}
	if b, err := os.ReadFile(source); err != nil || string(b) != "artwork" {
		t.Fatal("file was not restored", err)
	}
	memory, err := s.ProjectMemory("root:会话项目")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:会话项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Items {
		if event.Kind == "automatic_project_restored" && event.Auto != nil && event.Auto.RequestedBy == "用户" {
			found = true
		}
	}
	if !found {
		t.Fatal("user objection not recorded in project memory")
	}
	state, err := os.ReadFile(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json"))
	if err != nil || bytes.Contains(state, []byte("撤销这次整理")) {
		t.Fatal("message text was retained", err)
	}
}

func TestBackgroundDiscoversNewSessionAndWorkerObjection(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	source := filepath.Join(base, "Desktop")
	sessions := filepath.Join(base, "sessions")
	cwd := filepath.Join(base, "Projects", "后台项目")
	for _, dir := range []string{source, sessions, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeSession := func(name, filename string) {
		t.Helper()
		path := filepath.Join(source, filename)
		if err := os.WriteFile(path, []byte("artwork\n"), 0600); err != nil {
			t.Fatal(err)
		}
		lines := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": name, "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": "*** Begin Patch\n*** Add File: " + path + "\n+artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + path + "\n"}}}
		var data []byte
		for _, line := range lines {
			b, _ := json.Marshal(line)
			data = append(data, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(sessions, name+".jsonl"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSession("first", "first.psd")
	if _, err := s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	s.autoOrganize.interval = 200 * time.Millisecond
	s.startAutoOrganize()
	defer s.closeAutoOrganize()
	wait := func(match func(AutoOrganizeView) bool) AutoOrganizeView {
		t.Helper()
		until := time.Now().Add(12 * time.Second)
		for time.Now().Before(until) {
			view, err := s.AutoOrganizeStatus()
			if err == nil && match(view) {
				return view
			}
			time.Sleep(50 * time.Millisecond)
		}
		view, _ := s.AutoOrganizeStatus()
		t.Fatalf("background did not complete: %+v", view)
		return view
	}
	first := wait(func(v AutoOrganizeView) bool { return len(v.Operations) == 1 && v.Operations[0].MemoryInitialized })
	writeSession("second", "second.psd")
	second := wait(func(v AutoOrganizeView) bool {
		return len(v.Operations) == 2 && v.Operations[0].MemoryInitialized && v.Operations[1].MemoryInitialized
	})
	var later autoorganize.Operation
	for _, o := range second.Operations {
		if o.ID != first.Operations[0].ID {
			later = o
		}
	}
	if _, err := autoorganize.RequestObjection(cfg, later.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	view := wait(func(v AutoOrganizeView) bool {
		for _, o := range v.Operations {
			if o.ID == later.ID {
				return o.Status == "restored"
			}
		}
		return false
	})
	if _, err := os.Stat(filepath.Join(source, "second.psd")); err != nil {
		t.Fatal("worker objection did not restore file", view, err)
	}
}

func TestLargeAutomaticProjectLinksAndRestoresAllBatches(t *testing.T) {
	s, cfg, _, configPath := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	if volume := os.Getenv("WORKSHELF_STRESS_VOLUME"); volume != "" {
		cfg.Root = filepath.Join(volume, "batch-projects")
		if err := os.MkdirAll(cfg.Root, 0700); err != nil {
			t.Fatal(err)
		}
		writeVersionConfig(t, configPath, cfg)
	}
	source := filepath.Join(base, "Desktop")
	os.MkdirAll(source, 0700)
	count := 130
	if os.Getenv("WORKSHELF_STRESS_BATCH") == "1" {
		count = 2267
	}
	records := make([]activity.Record, 0, count)
	for i := 0; i < count; i++ {
		file := filepath.Join(source, fmt.Sprintf("设计稿-%03d.psd", i))
		if err := os.WriteFile(file, []byte(fmt.Sprint(i)), 0600); err != nil {
			t.Fatal(err)
		}
		records = append(records, activity.Record{Path: file, Project: "批量项目", WorkType: "设计·UI原型", Agent: "codex", Time: time.Now().Format(time.RFC3339)})
	}
	if err := activity.Append(activity.Dir(cfg), time.UTC, records...); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Summary.Moved != count {
		t.Fatal(view, err)
	}
	for _, item := range view.Operations[0].Summary.Items {
		if _, err := os.Stat(item.Source); !os.IsNotExist(err) {
			t.Fatal("source remains after automatic move", err)
		}
		if os.Getenv("WORKSHELF_STRESS_VOLUME") != "" && !item.CrossDevice {
			t.Fatal("batch item did not cross devices")
		}
	}
	key := "root:批量项目"
	memory, err := s.ProjectMemory(key)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents(key, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_organized" && e.Auto != nil && len(e.Auto.Items) == count {
			found = true
		}
	}
	if !found {
		t.Fatal("batch receipt missing from project memory")
	}
	graph, err := s.ProjectMemoryGraph(key, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, n := range graph.Nodes {
		if n.Kind == "memory_file" {
			files++
		}
	}
	if files < count {
		t.Fatal("batch file relations missing", files)
	}
	restored, err := s.RejectAutoOrganize(view.Operations[0].ID)
	if err != nil || restored.Restore.Restored != count {
		t.Fatal(restored, err)
	}
	for i, r := range records {
		b, err := os.ReadFile(r.Path)
		if err != nil || string(b) != fmt.Sprint(i) {
			t.Fatal("source not restored", i, err)
		}
	}
}

func TestWorkerObjectionSynchronizesEvenAfterAutomaticModeStops(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "worker.psd")
	if err := os.WriteFile(file, []byte("worker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: file, Project: "工作者项目", WorkType: "设计·UI原型", Agent: "codex", Time: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 {
		t.Fatal(view, err)
	}
	id := view.Operations[0].ID
	if _, err = autoorganize.RequestObjection(cfg, id, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err = autoorganize.Restore(cfg, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(false, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests, err := autoorganize.PendingObjections(cfg)
	if err != nil || len(requests) != 0 {
		t.Fatal(requests, err)
	}
	status, err := s.ProjectMemory("root:工作者项目")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:工作者项目", status.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Kind == "automatic_project_restored" && e.Auto != nil && e.Auto.RequestedBy == "claude" {
			found = true
		}
	}
	if !found {
		t.Fatal("worker objection missing from memory")
	}
	graph, err := s.ProjectMemoryGraph("root:工作者项目", status.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, node := range graph.Nodes {
		if node.Kind == "memory_source" && node.Name == "claude" {
			found = true
		}
	}
	if !found {
		t.Fatal("worker objection missing from graph")
	}
}

func TestConfirmUnknownTradeOnceForProject(t *testing.T) {
	s, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "brief.md")
	os.WriteFile(file, []byte("brief"), 0600)
	if err := activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: file, Project: "文档项目", Agent: "codex", Time: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Candidates) != 1 || view.Candidates[0].Reason != "尚未识别工种" {
		t.Fatal(view, err)
	}
	view, err = s.ConfirmAutoProjectTrade("文档项目", "设计·UI原型")
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" {
		t.Fatal(view, err)
	}
	policy, err := autoorganize.ReadPolicy(cfg)
	if err != nil || policy.ProjectTrades["文档项目"] != "设计·UI原型" {
		t.Fatal(policy, err)
	}
	if _, err = s.SetAutoOrganize(false, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	policy, err = autoorganize.ReadPolicy(cfg)
	if err != nil || policy.ProjectTrades["文档项目"] != "设计·UI原型" {
		t.Fatal("saved trade lost", policy, err)
	}
}
