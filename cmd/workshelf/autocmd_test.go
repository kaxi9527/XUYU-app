package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestWorkerRejectCommandRestoresAndQueuesMemorySync(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	os.MkdirAll(cfg.Root, 0700)
	path := filepath.Join(h, "draft.psd")
	os.WriteFile(path, []byte("artifact"), 0600)
	c := autoorganize.Candidate{Project: "设计项目", WorkType: "设计·UI原型", Files: []string{path}}
	o, err := autoorganize.Execute(context.Background(), cfg, h, autoorganize.Policy{Enabled: true, Roots: []string{h}}, c)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(h, "config.json")
	data, _ := json.Marshal(cfg)
	os.WriteFile(configPath, data, 0600)
	var out, stderr bytes.Buffer
	if err = cmdAuto([]string{"list", "--project", "设计项目", "--config", configPath, "--json"}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	var listed []autoorganize.Operation
	if err = json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != o.ID {
		t.Fatalf("worker cannot identify the operation: %s %v", out.String(), err)
	}
	out.Reset()
	if err = cmdAuto([]string{"list", "--project", "其他项目", "--config", configPath, "--json"}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	if err = json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 0 {
		t.Fatalf("project filter returned unrelated operations: %s %v", out.String(), err)
	}
	out.Reset()
	if err = cmdAuto([]string{"reject", o.ID, "--agent", "claude", "--config", configPath, "--json"}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	var result struct {
		Operation autoorganize.Operation `json:"operation"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil || result.Operation.Status != "restored" {
		t.Fatal(out.String(), err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "artifact" {
		t.Fatal("file not restored", err)
	}
	reqs, err := autoorganize.PendingObjections(cfg)
	if err != nil || len(reqs) != 1 || reqs[0].Agent != "claude" {
		t.Fatal(reqs, err)
	}
}
