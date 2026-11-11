package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func autoModelFixture(t *testing.T, handler func(http.ResponseWriter, *http.Request, string)) (*ShelfService, *atomic.Int32, string) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Messages) == 0 {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		input := in.Messages[len(in.Messages)-1].Content
		w.Header().Set("Content-Type", "application/json")
		if input == "Reply with the word OK." {
			fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)
			return
		}
		calls.Add(1)
		handler(w, r, input)
	}))
	t.Cleanup(server.Close)
	s, _, _, _ := versionFixture(t)
	t.Cleanup(s.closeModels)
	client := modelhttp.NewClient()
	modelDir := filepath.Join(t.TempDir(), "models")
	store, err := models.NewStore(modelDir, nil, &accountModelAdapter{direct: client})
	if err != nil {
		t.Fatal(err)
	}
	s.connections.store, s.connections.client, s.connections.protocolReady = store, client, true
	p, err := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "本机模拟", Protocol: models.Chat, BaseURL: server.URL + "/v1", ModelID: "fixture", ModelName: "测试模型"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	saved, err := store.Save(context.Background(), p, "save")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyModel(context.Background(), saved.Revision, "verify", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetMode(context.Background(), verified.Receipt.Revision, "use", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	return s, calls, modelDir
}

func TestObjectionModelRequiresSeparateConsentAndRestores(t *testing.T) {
	var received []string
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Message string `json:"message"`
			Project string `json:"project"`
		}
		if json.Unmarshal([]byte(input), &body) != nil || body.Project != "自然项目" {
			t.Error("unexpected input")
			w.WriteHeader(400)
			return
		}
		received = append(received, body.Message)
		answer := `{"objection":false}`
		if body.Message == "我还没想好" {
			answer = "不确定"
		}
		if body.Message == "这样不妥，请把刚刚挪走的文件放回原来的地方" {
			answer = "```json\n{\"objection\":true}\n```"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	sessions, cwd := filepath.Join(base, "sessions"), filepath.Join(base, "Projects", "自然项目")
	file := filepath.Join(base, "Desktop", "图.psd")
	for _, dir := range []string{sessions, cwd, filepath.Dir(file)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("private artwork"), 0600); err != nil {
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
			b, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(append(b, '\n')); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendRows(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "natural-session", "cwd": cwd}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": "*** Begin Patch\n*** Add File: " + file + "\n+artwork\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Success. Updated the following files:\nA " + file + "\n"}})
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" {
		t.Fatal("organization failed", err)
	}
	groupPreview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, groupPreview.RecipientSHA, groupPreview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	message := func(text string) any {
		return map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}
	}
	appendRows(message("这样不妥，请把刚刚挪走的文件放回原来的地方"))
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("message sent without separate consent")
	}
	preview, err := s.PreviewAutoObjectionModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjectionModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	appendRows(message("这个项目是如何整理的？"))
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, _ = s.AutoOrganizeStatus()
	if view.Operations[0].Status != "organized" {
		t.Fatal("question caused restore")
	}
	appendRows(message("我还没想好"))
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = s.AutoOrganizeStatus()
	if err != nil || view.Operations[0].Status != "organized" || !strings.Contains(strings.Join(view.SessionIssues, " "), "模型判断未完成") {
		t.Fatal("invalid model result did not remain visible", err)
	}
	connections, err := s.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	modelID := connections.State.Settings.ActiveModelID
	mode := s.SetWorkShelfMode("tool", "", connections.State.Revision, "objection-tool-mode")
	if mode.Error != "" {
		t.Fatal(mode.Error)
	}
	appendRows(message("这样不妥，请把刚刚挪走的文件放回原来的地方"))
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = s.AutoOrganizeStatus()
	if err != nil || !view.ObjectionModel.ModelChanged || view.Operations[0].Status != "organized" || calls.Load() != 2 {
		t.Fatal("changed model was used without consent", err)
	}
	if !strings.Contains(strings.Join(view.SessionIssues, " "), "模型判断未完成") {
		t.Fatal("model failure was not shown")
	}
	connections, err = s.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	mode = s.SetWorkShelfMode("agent", modelID, connections.State.Revision, "objection-agent-mode")
	if mode.Error != "" {
		t.Fatal(mode.Error)
	}
	preview, err = s.PreviewAutoObjectionModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjectionModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	text := "这样不妥，请把刚刚挪走的文件放回原来的地方"
	appendRows(message(text))
	if err := s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = s.AutoOrganizeStatus()
	if err != nil || view.Operations[0].Status != "restored" {
		t.Fatal("natural objection did not restore", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "private artwork" {
		t.Fatal("file not restored", err)
	}
	if calls.Load() != 3 || len(received) != 3 || received[2] != text {
		t.Fatal("unexpected model requests", calls.Load())
	}
	memory, err := s.ProjectMemory("root:自然项目")
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ProjectMemoryEvents("root:自然项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	encodedHistory, err := json.Marshal(history)
	if err != nil || strings.Contains(string(encodedHistory), text) {
		t.Fatal("message retained in project history", err)
	}
	found := false
	for _, event := range history.Items {
		if event.Kind == "automatic_project_restored" && event.Auto != nil && event.Auto.Objection != nil {
			e := event.Auto.Objection
			found = e.Provider == "本机模拟" && e.Model == "测试模型" && e.SourcePath == autoMemoryPath(log) && e.RecordSHA != "" && e.Line > 0
		}
	}
	if !found {
		t.Fatal("model objection source was not recorded in project memory")
	}
	graph, err := s.ProjectMemoryGraph("root:自然项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, node := range graph.Nodes {
		if node.Name == "本机模拟 / 测试模型" {
			found = true
		}
	}
	if !found {
		t.Fatal("objection model missing from graph")
	}
	for _, name := range []string{"会话进度.json", "模型判断.json"} {
		b, err := os.ReadFile(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", name))
		if err != nil || strings.Contains(string(b), text) {
			t.Fatal("message retained", name, err)
		}
	}
	requestsDir := filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "反对记录")
	entries, err := os.ReadDir(requestsDir)
	if err != nil || len(entries) == 0 {
		t.Fatal("objection receipt missing", err)
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(requestsDir, entry.Name()))
		if err != nil || strings.Contains(string(b), text) {
			t.Fatal("message retained in objection receipt", err)
		}
	}
	if _, err := s.SetAutoObjections(false); err != nil {
		t.Fatal(err)
	}
	view, err = s.AutoOrganizeStatus()
	if err != nil || view.ObjectionModel.Enabled || !view.Model.Enabled {
		t.Fatal("model consent survived local revocation", err)
	}
}

func TestDisablingObjectionModelCancelsInFlightMessage(t *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	s, _, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	dir := filepath.Join(base, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoOrganize(false, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: dir}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoObjectionModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjectionModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	state, err := autoorganize.ReadModelState(cfg)
	if err != nil || state.ObjectionConsent == nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.judgeSessionObjection(context.Background(), cfg, *state.ObjectionConsent, "请把刚才的文件放回去", "项目")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model request did not start")
	}
	if _, err := s.SetAutoObjectionModel(false, "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("model request was not cancelled")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled model request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("model request did not finish")
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || view.ObjectionModel.Enabled {
		t.Fatal("model consent not removed", err)
	}
}

func TestAutoModelClassifiesGroupsAndPreservesMemory(t *testing.T) {
	var payload string
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		payload = input
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 {
			t.Error("unexpected groups", err)
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "模型品牌项目", "workType": "设计·UI原型", "confident": true, "reason": "设计文件属于同一会话"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	files := []string{filepath.Join(base, "Desktop", "首页.psd"), filepath.Join(base, "Downloads", "封面.psd")}
	for _, file := range files {
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, []byte("PRIVATE_FILE_CONTENT"), 0600); err != nil {
			t.Fatal(err)
		}
		r := activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Time: time.Now().Format(time.RFC3339), Note: `{"session":"session-1","sourcePath":"/synthetic/session.jsonl"}`}
		if err = activity.Append(activity.Dir(cfg), time.UTC, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("model used before consent")
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	if err = s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || strings.Contains(payload, base) || strings.Contains(payload, "PRIVATE_FILE_CONTENT") {
		t.Fatal("unexpected model request", calls.Load())
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || len(view.Operations[0].ModelDecisions) != 1 {
		t.Fatal(view, err)
	}
	memory, err := s.ProjectMemory("root:模型品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:模型品牌项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events.Items {
		if e.Auto != nil && e.Kind == "automatic_project_organized" && len(e.Auto.ModelDecisions) == 1 && e.Auto.ModelDecisions[0].Model == "测试模型" && len(e.Auto.Items) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("model decision missing from project memory", events)
	}
	graph, err := s.ProjectMemoryGraph("root:模型品牌项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, node := range graph.Nodes {
		if node.Name == "本机模拟 / 测试模型" {
			found = true
		}
	}
	if !found {
		t.Fatal("model decision missing from graph")
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if data, e := os.ReadFile(file); e != nil || string(data) != "PRIVATE_FILE_CONTENT" {
			t.Fatal("file not restored", e)
		}
	}
	if err = s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("same rejected files were classified again", calls.Load())
	}
}

func TestAutoModelCollectsIntoExistingProjectAndRestores(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 || !strings.Contains(input, "已有品牌项目") {
			t.Error("existing project or group missing", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "已有品牌项目", "workType": "设计·UI原型", "confident": true, "reason": "归入已有品牌项目"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(cfg.Root, "已有品牌项目")
	w, err := worktype.Resolve("设计·UI原型")
	if err != nil {
		t.Fatal(err)
	}
	var originals []string
	for _, folder := range worktype.LayoutFor(w.Template).Folders {
		original := filepath.Join(project, folder, "首页.psd")
		if err = os.MkdirAll(filepath.Dir(original), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(original, []byte("existing version"), 0600); err != nil {
			t.Fatal(err)
		}
		originals = append(originals, original)
	}
	file := filepath.Join(base, "Desktop", "首页.psd")
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("new version"), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(base, "sessions-existing")
	if err = os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	rows := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "existing-project-session", "cwd": base}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": "*** Begin Patch\n*** Add File: " + file + "\n+new version\n*** End Patch\n"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA output\n"}},
	}
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
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || view.Operations[0].CreatedProject || view.Operations[0].Summary.Moved != 1 {
		t.Fatal(view, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected model calls", calls.Load())
	}
	item := view.Operations[0].Summary.Items[0]
	if data, e := os.ReadFile(item.Target); e != nil || string(data) != "new version" {
		t.Fatal("new version missing", e)
	}
	for _, original := range originals {
		if data, e := os.ReadFile(original); e != nil || string(data) != "existing version" {
			t.Fatal("existing version changed", original, e)
		}
	}
	memory, err := s.ProjectMemory("root:已有品牌项目")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:已有品牌项目", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Items {
		if event.Kind == "automatic_project_organized" && event.Auto != nil && len(event.Auto.ModelDecisions) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing project memory for model decision")
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(file); e != nil || string(data) != "new version" {
		t.Fatal("new version not restored", e)
	}
	for _, original := range originals {
		if data, e := os.ReadFile(original); e != nil || string(data) != "existing version" {
			t.Fatal("existing version not preserved", original, e)
		}
	}
	if _, err = os.Stat(project); err != nil {
		t.Fatal("existing project removed", err)
	}
}

func TestAutoModelClassifiesRegisteredExternalProject(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 || body.Groups[0].Project != "品牌/原地设计" {
			t.Error("external project group missing", err)
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "品牌/原地设计", "workType": "设计·UI原型", "confident": true, "reason": "设计需求"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(base, "Projects", "原地设计")
	file := filepath.Join(base, "Desktop", "需求说明.md")
	sessions := filepath.Join(base, "sessions-external-model")
	for _, dir := range []string{project, filepath.Dir(file), sessions, registry.Dir(cfg)} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(file, []byte("design brief"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "external-model", Name: "品牌/原地设计", Path: project}}}
	data, _ := json.Marshal(reg)
	if err = os.WriteFile(filepath.Join(registry.Dir(cfg), registry.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "external-model-session", "cwd": project}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": "*** Begin Patch\n*** Add File: " + file + "\n+design brief\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA output\n"}}}
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
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || view.Operations[0].ProjectKey != "place:external-model" || len(view.Operations[0].ModelDecisions) != 1 || calls.Load() != 1 {
		t.Fatal(view, err, calls.Load())
	}
	memory, err := s.ProjectMemory("place:external-model")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.ProjectMemoryGraph("place:external-model", memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range graph.Nodes {
		if n.Name == "本机模拟 / 测试模型" {
			found = true
		}
	}
	if !found {
		t.Fatal("model source missing from external project graph")
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(file); e != nil || string(data) != "design brief" {
		t.Fatal("file not restored", e)
	}
}

func TestAutoModelMatchesExternalProjectWithoutWorkingDirectory(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Groups   []autoorganize.ModelTask `json:"groups"`
			Projects []string                 `json:"existingProjects"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 {
			t.Error("missing file group", err)
			w.WriteHeader(400)
			return
		}
		found := false
		for _, name := range body.Projects {
			if name == "品牌/原地项目" {
				found = true
			}
			if name == "范围外项目" {
				t.Error("model received project outside selected scope")
			}
		}
		if !found {
			t.Error("external project not provided to model")
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "品牌/原地项目", "workType": "设计·UI原型", "confident": true, "reason": "同一品牌"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	project := filepath.Join(base, "Projects", "品牌原地项目")
	private := filepath.Join(base, "Private", "范围外项目")
	desktop := filepath.Join(base, "Desktop")
	sessions := filepath.Join(base, "sessions-no-cwd")
	for _, dir := range []string{project, private, desktop, sessions, registry.Dir(cfg)} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(desktop, "品牌封面.png")
	if err = os.WriteFile(file, []byte("design"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := registry.Registry{Version: registry.Version, Projects: []registry.Project{{ID: "brand-existing", Name: "品牌/原地项目", Path: project}, {ID: "private", Name: "范围外项目", Path: private}}}
	data, _ := json.Marshal(reg)
	if err = os.WriteFile(filepath.Join(registry.Dir(cfg), registry.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{desktop, project}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "no-project-cwd", "cwd": base}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": "*** Begin Patch\n*** Add File: " + file + "\n+design\n*** End Patch\n"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA output\n"}}}
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
	if err != nil || len(view.Operations) != 1 || view.Operations[0].ProjectKey != "place:brand-existing" || view.Operations[0].Target != project || view.Operations[0].Status != "organized" || calls.Load() != 1 {
		t.Fatal(view, err, calls.Load())
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(file); e != nil || string(data) != "design" {
		t.Fatal("file not restored", e)
	}
	if _, e := os.Stat(filepath.Join(cfg.Root, "品牌原地项目")); !os.IsNotExist(e) {
		t.Fatal("duplicate project created", e)
	}
}

func TestAutoModelCanSeeProjectsBeyondFirstTwoHundred(t *testing.T) {
	_, cfg, _, _ := versionFixture(t)
	base := filepath.Dir(cfg.Root)
	reg := registry.Registry{Version: registry.Version}
	for i := 0; i < 250; i++ {
		name := fmt.Sprintf("项目-%03d", i)
		reg.Projects = append(reg.Projects, registry.Project{ID: fmt.Sprintf("id-%03d", i), Name: name, Path: filepath.Join(base, "Projects", name)})
	}
	if err := os.MkdirAll(registry.Dir(cfg), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(filepath.Join(registry.Dir(cfg), registry.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	input, _, err := autoModelPayload(cfg, autoorganize.Policy{Roots: []string{base}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Projects []string `json:"existingProjects"`
	}
	if err = json.Unmarshal([]byte(input), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range body.Projects {
		if name == "项目-249" {
			found = true
		}
	}
	if !found {
		t.Fatal("later registered project missing from model context")
	}
}

func TestAutoModelRecoversDecisionAfterInterruptedPolicyWrite(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 {
			t.Error("unexpected group", err)
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "恢复测试项目", "workType": "设计·UI原型", "confident": true, "reason": "同一会话"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "恢复.psd")
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	record := activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Time: time.Now().Format(time.RFC3339), Note: `{"session":"recovery-session","sourcePath":"/synthetic/session.jsonl"}`}
	if err = activity.Append(activity.Dir(cfg), time.UTC, record); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	p, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoModel(context.Background(), cfg, p, []activity.Record{record}); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the decision journal was saved, before policy was saved.
	p, err = autoorganize.ReadPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.SessionProjects, p.ProjectTrades = nil, nil
	if err = autoorganize.SavePolicy(cfg, p); err != nil {
		t.Fatal(err)
	}
	if err = s.RunAutoOrganize(); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" || len(view.Operations[0].ModelDecisions) != 1 {
		t.Fatal(view, err)
	}
	if calls.Load() != 1 {
		t.Fatal("decision was requested again", calls.Load())
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "fixture" {
		t.Fatal("file not restored", err)
	}
}

func TestAutoModelUsesNewSessionWithoutSendingConversation(t *testing.T) {
	var sent string
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		sent = input
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 {
			t.Error("unexpected group", err)
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "新会话项目", "workType": "设计·UI原型", "confident": true, "reason": "同一组设计成果"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	sessions := filepath.Join(base, "sessions")
	files := []string{filepath.Join(base, "Desktop", "一.psd"), filepath.Join(base, "Downloads", "二.psd")}
	for _, file := range files {
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, []byte("artifact\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n"
	for _, file := range files {
		patch += "*** Add File: " + file + "\n+artifact\n"
	}
	patch += "*** End Patch\n"
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "new-session", "cwd": base}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "text": "PRIVATE_CONVERSATION"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write-1", "input": patch}}, map[string]any{"type": "response_item", "timestamp": "2026-11-09T10:00:00Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write-1", "output": "Success. Updated the following files:\nA output\n"}}}
	var data []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		data = append(data, append(b, '\n')...)
	}
	if err = os.WriteFile(filepath.Join(sessions, "new.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || strings.Contains(sent, "PRIVATE_CONVERSATION") || strings.Contains(sent, base) || strings.Contains(sent, "artifact") {
		t.Fatal("unexpected model data or call count", calls.Load())
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Summary.Moved != 2 {
		t.Fatal(view, err)
	}
	if len(view.Operations[0].ModelDecisions) != 1 || view.Operations[0].Signals[0].Session != "new-session" {
		t.Fatal("provenance missing", view.Operations[0])
	}
	if _, err = s.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if _, err = os.Stat(file); err != nil {
			t.Fatal("file not restored", err)
		}
	}
}

func TestAutoModelRequiresNewConsentWhenScopeOrModeChanges(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		t.Error("unexpected model request")
		w.WriteHeader(500)
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{filepath.Join(base, "Desktop")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err == nil {
		t.Fatal("stale scope consent accepted")
	}
	preview, err = s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	state, err := s.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	result := s.SetWorkShelfMode("tool", "", state.State.Revision, "tool-mode")
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || !view.Model.ModelChanged {
		t.Fatal("model change not visible", view.Model, err)
	}
	if calls.Load() != 0 {
		t.Fatal("model contacted unexpectedly")
	}
}

func TestDisablingAutoModelCancelsInFlightJudgment(t *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "未分类.psd")
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Time: time.Now().Format(time.RFC3339), Note: `{"session":"one","sourcePath":"/synthetic/one.jsonl"}`}
	if err = activity.Append(activity.Dir(cfg), time.UTC, rec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.runAutoOrganize(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	if _, err = s.SetAutoModel(false, "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("request not cancelled")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected calls", calls.Load())
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file changed after stop", err)
	}
}

func TestAutoModelGrantContinuesAfterApplicationRestart(t *testing.T) {
	s, calls, modelDir := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		var body struct {
			Groups []autoorganize.ModelTask `json:"groups"`
		}
		if err := json.Unmarshal([]byte(input), &body); err != nil || len(body.Groups) != 1 {
			t.Error("unexpected groups", err)
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"id": body.Groups[0].ID, "project": "重启后的项目", "workType": "设计·UI原型", "confident": true}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(answer)}}}})
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "restart.psd")
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Note: `{"session":"after-restart","sourcePath":"/synthetic/restart.jsonl"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	s.closeModels()
	client := modelhttp.NewClient()
	store, err := models.NewStore(modelDir, nil, &accountModelAdapter{direct: client})
	if err != nil {
		t.Fatal(err)
	}
	restarted := &ShelfService{}
	restarted.connections.store, restarted.connections.client, restarted.connections.protocolReady = store, client, true
	t.Cleanup(restarted.closeModels)
	if err = restarted.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("persisted grant did not resume", calls.Load())
	}
	view, err := restarted.AutoOrganizeStatus()
	if err != nil || len(view.Operations) != 1 || view.Operations[0].Status != "organized" {
		t.Fatal(view, err)
	}
	if _, err = restarted.RejectAutoOrganize(view.Operations[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("file not restored", err)
	}
}

func TestAutoModelInvalidResponseKeepsFilesAndDoesNotLoop(t *testing.T) {
	s, calls, _ := autoModelFixture(t, func(w http.ResponseWriter, r *http.Request, input string) {
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"not JSON"}}]}`)
	})
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(cfg.Root)
	file := filepath.Join(base, "Desktop", "unknown.psd")
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = activity.Append(activity.Dir(cfg), time.UTC, activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Note: `{"session":"bad-response","sourcePath":"/synthetic/bad.jsonl"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewAutoModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	if err = s.runAutoOrganize(context.Background()); err == nil {
		t.Fatal("invalid response appeared successful")
	}
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := s.AutoOrganizeStatus()
	if err != nil || view.Model.Problem == "" || calls.Load() != 1 {
		t.Fatal(view.Model, calls.Load(), err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "draft" {
		t.Fatal("file changed", err)
	}
}
