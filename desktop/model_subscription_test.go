package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"strings"
	"testing"
)

func TestSubscriptionAdapterRouting(t *testing.T) {
	calls := []string{}
	a := accountModelAdapter{request: func(ctx context.Context, method, path string, in, out any) error {
		calls = append(calls, method+" "+path)
		var data string
		if method == "GET" {
			data = `{"models":["codex/fixture-model","claude/foreign-model"]}`
		} else {
			body := in.(map[string]string)
			if path != "/control/probe-text" || body["model"] != "codex/fixture-model" {
				t.Fatal("wrong probe")
			}
			proof := subscriptionRecipientFixture()
			proof.Agent, proof.ModelID, proof.WireModel, proof.Endpoint = "codex", "codex/fixture-model", "fixture-model", "https://chatgpt.com/backend-api/codex/responses"
			encoded, _ := json.Marshal(map[string]any{"recipient": proof, "result": json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)})
			data = string(encoded)
		}
		return json.Unmarshal([]byte(data), out)
	}}
	target := models.Target{Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/codex", ModelID: "codex/fixture-model"}
	page, err := a.ListModels(context.Background(), target, nil, "")
	if err != nil || len(page.Models) != 1 {
		t.Fatal("subscription list", err)
	}
	cap, err := a.Verify(context.Background(), target, nil)
	if err != nil || cap.Text != "supported" || cap.Tools != "unknown" {
		t.Fatal("subscription probe", err)
	}
	target.BaseURL = "https://example.test"
	if _, err := a.Verify(context.Background(), target, nil); err == nil {
		t.Fatal("external subscription URL accepted")
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "?agent=codex") {
		t.Fatal("unexpected routing", calls)
	}
	a.request = func(context.Context, string, string, any, any) error { return errors.New("fixture error") }
	target.BaseURL = models.SubscriptionEndpoint + "/codex"
	if _, err := a.Verify(context.Background(), target, nil); err == nil {
		t.Fatal("failed probe verified")
	}
}

func TestCodexGenerationRequiresUncappedConsent(t *testing.T) {
	proof := subscriptionRecipientFixture()
	proof.Agent, proof.ModelID, proof.WireModel, proof.Endpoint = "codex", "codex/fixture", "fixture", "https://chatgpt.com/backend-api/codex/responses"
	target := models.Target{Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/codex", ModelID: proof.ModelID, Subscription: &proof}
	calls := 0
	a := accountModelAdapter{request: func(_ context.Context, _, path string, in, out any) error {
		calls++
		if path != "/control/generate-text" || in.(map[string]any)["model"] != proof.ModelID {
			t.Fatal("wrong model route")
		}
		return json.Unmarshal([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`), out)
	}}
	input := models.GenerationRequest{Input: "synthetic", MaxOutputTokens: 64}
	if _, err := a.Generate(context.Background(), target, nil, input); err == nil || calls != 0 {
		t.Fatal("uncapped model accepted without consent")
	}
	input.AllowUncappedOutput = true
	if result, err := a.Generate(context.Background(), target, nil, input); err != nil || result.Text != "ok" || calls != 1 {
		t.Fatal("authorized codex request failed", err)
	}
}

func TestSubscriptionGenerationUsesOwnedTransportAndStrictOutput(t *testing.T) {
	calls := 0
	a := accountModelAdapter{request: func(ctx context.Context, method, path string, in, out any) error {
		calls++
		body := in.(map[string]any)
		if method != "POST" || path != "/control/generate-text" || body["model"] != "copilot/fixture" || body["max_completion_tokens"] != 123 || body["stream"] != false {
			t.Fatal("wrong subscription request")
		}
		return json.Unmarshal([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"project advice"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`), out)
	}}
	target := models.Target{Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/copilot", ModelID: "copilot/fixture"}
	proof := subscriptionRecipientFixture()
	target.Subscription = &proof
	input := models.GenerationRequest{System: "trusted", Input: "project packet", MaxOutputTokens: 123}
	result, err := a.Generate(context.Background(), target, nil, input)
	if err != nil || result.Text != "project advice" || result.Usage.InputTokens == nil {
		t.Fatal("generation", err)
	}
	target.BaseURL = models.SubscriptionEndpoint + "/claude"
	if _, err = a.Generate(context.Background(), target, nil, input); err == nil || calls != 1 {
		t.Fatal("cross-account route accepted")
	}
	for _, agent := range []string{"codex", "claude"} {
		blocked := models.Target{Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/" + agent, ModelID: agent + "/fixture"}
		if _, err = a.Generate(context.Background(), blocked, nil, input); err == nil || calls != 1 {
			t.Fatal("unbounded subscription invoked")
		}
	}
	target.BaseURL = models.SubscriptionEndpoint + "/copilot"
	a.request = func(_ context.Context, _, _ string, _ any, out any) error {
		return json.Unmarshal([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{}]}}]}`), out)
	}
	if _, err = a.Generate(context.Background(), target, nil, input); err == nil {
		t.Fatal("tool response accepted")
	}
}

func subscriptionRecipientFixture() models.SubscriptionRecipient {
	return models.SubscriptionRecipient{Schema: 1, ProofID: strings.Repeat("a", 64), AccountID: strings.Repeat("b", 64), Agent: "copilot", AccountLabel: "Copilot · te••••st", ModelID: "copilot/fixture", Endpoint: "https://api.githubcopilot.com/chat/completions", WireModel: "fixture"}
}

func TestSubscriptionProbeAndRecipientQueryAreSeparate(t *testing.T) {
	proof := subscriptionRecipientFixture()
	calls := []string{}
	a := accountModelAdapter{request: func(_ context.Context, method, path string, in, out any) error {
		calls = append(calls, path)
		data, _ := json.Marshal(in)
		if method != "POST" || string(data) != `{"model":"copilot/fixture"}` {
			t.Fatal("probe/query accepted project input")
		}
		var response any = proof
		if path == "/control/probe-text" {
			response = map[string]any{"recipient": proof, "result": json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)}
		} else if path != "/control/generation-recipient" {
			t.Fatal(path)
		}
		data, _ = json.Marshal(response)
		return json.Unmarshal(data, out)
	}}
	target := models.Target{Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/copilot", ModelID: proof.ModelID}
	if _, e := a.Verify(context.Background(), target, nil); e != nil {
		t.Fatal(e)
	}
	if got, e := a.SubscriptionRecipient(context.Background(), target); e != nil || got != proof {
		t.Fatal(e)
	}
	if len(calls) != 2 || calls[0] == calls[1] {
		t.Fatal("lookup silently probed")
	}
	proof.Endpoint = "https://api.githubcopilot.com/other"
	if _, e := a.Verify(context.Background(), target, nil); e == nil {
		t.Fatal("unsafe probe accepted")
	}
	if _, e := a.SubscriptionRecipient(context.Background(), target); e == nil {
		t.Fatal("unsafe recipient accepted")
	}
}
