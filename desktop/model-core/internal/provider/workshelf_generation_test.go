package provider

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestWorkShelfSelectedUsesOnlyOneOwnNativeAccount(t *testing.T) {
	t.Setenv("WORKSHELF_MODEL_HOME", t.TempDir())
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	account := savedLogin{Agent: "codex", User: "fixture@example.test", On: true, Auth: json.RawMessage(`{"tokens":{"access_token":"synthetic"}}`)}
	if err := writeLogins([]savedLogin{account}); err != nil {
		t.Fatal(err)
	}
	p, err := WorkShelfSelected("codex")
	if err != nil || p.Responses != CodexBase || p.Account.User != account.User || len(p.Fallback) != 0 {
		t.Fatalf("not native: %v", err)
	}
	other := account
	other.User = "second@example.test"
	if err := writeLogins([]savedLogin{account, other}); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkShelfSelected("codex"); err == nil {
		t.Fatal("ambiguous account accepted")
	}
	account.On = false
	if err := writeLogins([]savedLogin{account}); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkShelfSelected("codex"); err == nil {
		t.Fatal("disabled account accepted")
	}
	account.On, account.Lapsed = true, "expired"
	if err := writeLogins([]savedLogin{account}); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkShelfSelected("codex"); err == nil {
		t.Fatal("expired account accepted")
	}
	if _, err := WorkShelfSelected("alias"); err == nil {
		t.Fatal("custom alias accepted")
	}
}

func TestWorkShelfDestinationLimitsReceiver(t *testing.T) {
	for _, x := range []struct {
		agent, address string
		valid          bool
	}{
		{"copilot", "https://api.githubcopilot.com/chat/completions", true},
		{"gemini", "https://cloudcode-pa.googleapis.com/v1internal:generateContent", true},
		{"antigravity", "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", true},
		{"copilot", "https://api.githubcopilot.com.evil.test/chat", false},
		{"copilot", "http://api.githubcopilot.com/chat", false},
		{"copilot", "https://api.githubcopilot.com:9443/chat", false},
		{"copilot", "https://user@api.githubcopilot.com/chat", false},
		{"copilot", "https://api.githubcopilot.com/chat?route=other", false},
		{"gemini", "https://api.githubcopilot.com/chat", false},
		{"codex", "https://chatgpt.com/backend-api/codex/responses", true},
		{"codex", "https://chatgpt.com/backend-api/codex/responses?route=other", false},
		{"claude", "https://api.anthropic.com/v1/messages", false},
	} {
		u, _ := url.Parse(x.address)
		if WorkShelfDestination(x.agent, u) != x.valid {
			t.Errorf("unexpected destination allowance: %s", x.address)
		}
	}
}
