package sourceevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExplicitUserObjectionDoesNotRetainConversation(t *testing.T) {
	p := Parser{Session: "session-1"}
	cases := []struct {
		adapter string
		line    string
		want    bool
	}{
		{"codex", `{"type":"response_item","timestamp":"2026-11-10T12:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"撤销这次整理"}]}}`, true},
		{"claude", `{"type":"user","timestamp":"2026-11-10T12:00:00Z","message":{"role":"user","content":"我不同意这次整理，请恢复原状"}}`, true},
		{"gemini", `{"type":"user","timestamp":"2026-11-10T12:00:00Z","content":[{"text":"恢复这次整理"}]}`, true},
		{"codex", `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"撤销这次整理"}]}}`, false},
		{"claude", `{"type":"user","message":{"role":"user","content":"不要撤销这次整理"}}`, false},
		{"claude", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"撤销这次整理"}]}}`, false},
		{"gemini", `{"type":"user","content":[{"text":"如果以后要撤销这次整理怎么办？"}]}`, false},
	}
	for _, tc := range cases {
		claim, ok := p.FeedObjection(tc.adapter, 4, []byte(tc.line), false)
		if ok != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.adapter, ok, tc.want)
		}
		if ok && (claim.Session != "session-1" || claim.Action != "objection" || claim.Path != "" || claim.RecordSHA == "") {
			t.Fatalf("unexpected evidence: %+v", claim)
		}
	}
	line := []byte(`{"type":"response_item","timestamp":"2026-11-10T12:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"这样不好，请把刚才移动的文件放回去"}]}}`)
	claim, ok := p.FeedObjection("codex", 5, line, true)
	if !ok || claim.Action != "objection_candidate" || claim.Message == "" {
		t.Fatal("natural message not made available for authorized judgment")
	}
	encoded, err := json.Marshal(claim)
	if err != nil || strings.Contains(string(encoded), claim.Message) {
		t.Fatal("message text would be persisted", err)
	}
}
