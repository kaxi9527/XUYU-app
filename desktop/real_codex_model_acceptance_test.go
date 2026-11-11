//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

// Opt-in: the account is real; the catalog, sessions and files are isolated.
func TestRealCodexSubscriptionAutomaticFlow(t *testing.T) {
	core := os.Getenv("WORKSHELF_REAL_CODEX_CORE")
	accounts := os.Getenv("WORKSHELF_REAL_CODEX_ACCOUNTS")
	if core == "" || accounts == "" {
		t.Skip("requires explicit real-subscription acceptance")
	}
	base := t.TempDir()
	cfg := config.Default(base)
	cfg.Root = filepath.Join(base, "Projects")
	cfg.ScanPaths = []string{base}
	configPath := filepath.Join(base, "config.json")
	writeVersionConfig(t, configPath, cfg)
	t.Setenv("WORKSHELF_CONFIG", configPath)
	s := &ShelfService{}
	s.connections.accounts.binary, s.connections.accounts.directory = core, accounts
	client := modelhttp.NewClient()
	store, err := models.NewStore(filepath.Join(base, "models"), nil, &accountModelAdapter{direct: client, request: s.connections.accounts.requestContext})
	if err != nil {
		t.Fatal(err)
	}
	s.connections.store, s.connections.client, s.connections.protocolReady = store, client, true
	defer s.closeModels()
	prepared, err := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "ChatGPT / Codex 订阅", Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/codex", ModelID: "codex/gpt-6.1-terra", ModelName: "gpt-6.1-terra"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(context.Background(), prepared, "real-codex-save")
	prepared.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Log("verifying the selected subscription with fixed test text")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	verified, err := store.VerifyModel(ctx, saved.Revision, "real-codex-verify", saved.ModelID)
	cancel()
	if err != nil || !verified.Verified {
		t.Fatal("real subscription verification failed", err, verified.Receipt.Problem)
	}
	if _, err = store.SetMode(context.Background(), verified.Receipt.Revision, "real-codex-use", "agent", saved.ModelID); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(base, "sessions")
	workdir := filepath.Join(base, "品牌项目UI界面设计")
	files := []string{filepath.Join(base, "Desktop", "UI界面设计", "首页.txt"), filepath.Join(base, "Downloads", "UI界面设计", "封面.txt")}
	for _, dir := range []string{cfg.Root, sessions, workdir, filepath.Dir(files[0]), filepath.Dir(files[1])} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range files {
		if err = os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetAutoOrganize(true, []string{base}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetAutoObjections(true); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("WORKSHELF_REAL_CODEX_RESTART_BEFORE_CONSENT") == "1" {
		s.closeModels()
		s = &ShelfService{}
		s.connections.accounts.binary, s.connections.accounts.directory = core, accounts
		defer s.closeModels()
		t.Log("restarting before creating the continuous grant")
	}
	preview, err := s.PreviewAutoModel()
	if err != nil || !preview.UncappedOutput {
		t.Fatal("codex preview failed", err)
	}
	if _, err = s.SetAutoModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewAutoObjectionModel()
	if err != nil || !preview.UncappedOutput {
		t.Fatal("codex objection preview failed", err)
	}
	if _, err = s.SetAutoObjectionModel(true, preview.RecipientSHA, preview.ScopeSHA); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(sessions, "synthetic.jsonl")
	patch := "*** Begin Patch\n*** Add File: " + files[0] + "\n+synthetic\n*** Add File: " + files[1] + "\n+synthetic\n*** End Patch\n"
	rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": "synthetic-session", "cwd": workdir}}, map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "write", "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "write", "output": "Success. Updated the following files:\nA " + files[0] + "\nA " + files[1] + "\n"}}}
	var data []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		data = append(data, append(b, '\n')...)
	}
	if err = os.WriteFile(session, data, 0600); err != nil {
		t.Fatal(err)
	}
	restartBeforeClassification := os.Getenv("WORKSHELF_REAL_CODEX_RESTART_BEFORE_CLASSIFICATION") == "1"
	if restartBeforeClassification {
		s.closeModels()
		s = &ShelfService{}
		s.connections.accounts.binary, s.connections.accounts.directory = core, accounts
		defer s.closeModels()
		t.Log("restarting the model service before classification")
	}
	t.Log("classifying two synthetic file names")
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal("automatic classification failed", err)
	}
	operations, err := autoorganize.List(cfg)
	if err != nil || len(operations) != 1 || operations[0].Status != "organized" || operations[0].Summary.Moved != 2 {
		state, _ := autoorganize.ReadModelState(cfg)
		view, _ := s.AutoOrganizeStatus()
		t.Logf("synthetic state: attempts=%+v candidates=%+v unassigned=%+v issues=%v operations=%d", state.Attempts, view.Candidates, view.Unassigned, view.SessionIssues, len(operations))
		t.Fatal("model did not organize both synthetic files", err)
	}
	if !restartBeforeClassification {
		s.closeModels()
		s = &ShelfService{}
		s.connections.accounts.binary, s.connections.accounts.directory = core, accounts
		defer s.closeModels()
		t.Log("restarting the model service before the objection")
	}
	message := map[string]any{"type": "response_item", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "这样不妥，请把刚刚挪走的文件放回原来的地方"}}}}
	b, _ := json.Marshal(message)
	f, err := os.OpenFile(session, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("classifying the synthetic objection")
	if err = s.runAutoOrganize(context.Background()); err != nil {
		t.Fatal("objection processing failed", err)
	}
	operations, err = autoorganize.List(cfg)
	if err != nil || len(operations) != 1 || operations[0].Status != "restored" {
		t.Fatal("objection did not restore the files", err)
	}
	for _, path := range files {
		if content, err := os.ReadFile(path); err != nil || string(content) != "synthetic" {
			t.Fatal("synthetic file not restored", err)
		}
	}
	memory, err := s.ProjectMemory("root:" + operations[0].Project)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ProjectMemoryEvents("root:"+operations[0].Project, memory.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Items {
		if event.Kind == "automatic_project_restored" && event.Auto != nil && event.Auto.Objection != nil && event.Auto.Objection.Model == "gpt-6.1-terra" {
			found = true
		}
	}
	if !found {
		t.Fatal("real model objection missing from project memory")
	}
}
