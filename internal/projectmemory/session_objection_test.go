package projectmemory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDirectoryObjectionsRespectConsentAcrossAdapters(t *testing.T) {
	for _, adapter := range []string{"codex", "claude", "gemini"} {
		t.Run(adapter, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "sessions")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			message := func(at time.Time) any {
				stamp := at.Format(time.RFC3339Nano)
				switch adapter {
				case "codex":
					return map[string]any{"type": "response_item", "timestamp": stamp, "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "撤销这次整理"}}}}
				case "claude":
					return map[string]any{"type": "user", "sessionId": "session-1", "cwd": base, "timestamp": stamp, "message": map[string]any{"role": "user", "content": "撤销这次整理"}}
				default:
					return map[string]any{"type": "user", "sessionId": "session-1", "timestamp": stamp, "content": []any{map[string]any{"text": "撤销这次整理"}}}
				}
			}
			var metadata any = map[string]any{"type": "session_meta", "payload": map[string]any{"id": "session-1", "cwd": base}}
			if adapter == "gemini" {
				metadata = map[string]any{"sessionId": "session-1"}
			}
			var data []byte
			for _, row := range []any{metadata, message(now.Add(-time.Minute)), message(now.Add(time.Second))} {
				b, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, append(b, '\n')...)
			}
			if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), data, 0600); err != nil {
				t.Fatal(err)
			}
			scope := Scope{Roots: []string{base}}
			grant, err := NewDirectorySource(adapter, dir, scope)
			if err != nil {
				t.Fatal(err)
			}
			grant.ObjectionsSince = now.Format(time.RFC3339Nano)
			updated, claims, err := ScanDirectorySource(context.Background(), grant, scope)
			if err != nil || len(claims) != 1 || claims[0].Action != "objection" || claims[0].Session != "session-1" {
				t.Fatal(claims, err)
			}
			encoded, err := json.Marshal(updated)
			if err != nil || strings.Contains(string(encoded), "撤销这次整理") {
				t.Fatal("message retained", err)
			}
			updated.ObjectionsSince = grant.ObjectionsSince
			_, claims, err = ScanDirectorySource(context.Background(), updated, scope)
			if err != nil || len(claims) != 0 {
				t.Fatal("objection duplicated", claims, err)
			}
		})
	}
}
