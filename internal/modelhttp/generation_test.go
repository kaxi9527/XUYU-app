package modelhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestGenerationFourProtocolsWithReportedUsage(t *testing.T) {
	usages := map[models.Protocol]string{
		models.Chat:      `"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}`,
		models.Responses: `"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}`,
		models.Anthropic: `"usage":{"input_tokens":12,"output_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":4}`,
		models.Gemini:    `"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"totalTokenCount":16,"cachedContentTokenCount":2,"thoughtsTokenCount":1}`,
	}
	for protocol, reply := range probeReplies {
		t.Run(string(protocol), func(t *testing.T) {
			var count atomic.Int32
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				data, _ := io.ReadAll(r.Body)
				var body map[string]any
				if json.Unmarshal(data, &body) != nil {
					t.Error("invalid JSON")
				}
				if !strings.Contains(string(data), "context-digest") || !strings.Contains(string(data), "trusted instruction") || strings.Contains(string(data), probeText) {
					t.Error("wrong generation input")
				}
				if _, ok := body["tools"]; ok {
					t.Error("unsolicited tool capability")
				}
				var limit any
				switch protocol {
				case models.Chat:
					limit = body["max_completion_tokens"]
				case models.Responses:
					limit = body["max_output_tokens"]
					if body["store"] != false {
						t.Error("response persistence")
					}
				case models.Anthropic:
					limit = body["max_tokens"]
					if body["system"] != "trusted instruction" {
						t.Error("missing system")
					}
				case models.Gemini:
					limit = body["generationConfig"].(map[string]any)["maxOutputTokens"]
				}
				if limit != float64(321) {
					t.Errorf("wrong limit %v", limit)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, strings.TrimSuffix(reply, "}")+","+usages[protocol]+"}")
			})
			target := models.Target{Protocol: protocol, BaseURL: server.URL, ModelID: "test"}
			if protocol == models.Chat {
				target.Options.ChatTokenField = "max_completion_tokens"
			}
			result, err := client.Generate(context.Background(), target, testSecret(t), models.GenerationRequest{System: "trusted instruction", Input: "context-digest", MaxOutputTokens: 321})
			if err != nil || result.Text != "OK" || result.Usage.InputTokens == nil || *result.Usage.InputTokens != 12 || result.Usage.OutputTokens == nil || *result.Usage.OutputTokens != 3 || count.Load() != 1 {
				t.Fatalf("generation failed: %+v %v", result, err)
			}
			if protocol == models.Anthropic && result.Usage.TotalTokens != nil {
				t.Fatal("invented total")
			}
		})
	}
}
func TestGenerationRejectsInvalidAndAmbiguousOutput(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"a"}},{"finish_reason":"stop","message":{"role":"assistant","content":"b"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text","tool_calls":[{}]}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text","refusal":"blocked"}}]}`,
		strings.TrimSuffix(probeReplies[models.Chat], "}") + `,"usage":{"prompt_tokens":-1}}`,
		strings.TrimSuffix(probeReplies[models.Chat], "}") + `,"usage":{"prompt_tokens":1.5}}`,
		probeReplies[models.Chat] + `{"hidden":"second object"}`,
		strings.Replace(probeReplies[models.Chat], `"OK"`, `"`+strings.Repeat("x", models.MaxGeneratedBytes+1)+`"`, 1),
	} {
		if result, err := DecodeGeneration(models.Chat, []byte(body)); err == nil || result.Text != "" {
			t.Fatal("unsafe output accepted")
		}
	}
	for protocol, body := range probeReplies {
		result, err := DecodeGeneration(protocol, []byte(body))
		if err != nil || result.Usage.InputTokens != nil || result.Usage.OutputTokens != nil {
			t.Fatal("unknown usage must remain unknown", err)
		}
	}
}
func TestGenerationLimitsCancellationAndNoRetry(t *testing.T) {
	var calls atomic.Int32
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"do-not-echo-secret"}}`)
	})
	target := models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "x"}
	valid := models.GenerationRequest{Input: "input", MaxOutputTokens: 10}
	for _, in := range []models.GenerationRequest{{Input: "", MaxOutputTokens: 1}, {Input: strings.Repeat("x", models.MaxPromptBytes+1), MaxOutputTokens: 1}, {Input: "x", MaxOutputTokens: models.MaxGenerationTokens + 1}, {Input: "\xff", MaxOutputTokens: 1}} {
		if _, err := client.Generate(context.Background(), target, nil, in); err == nil {
			t.Fatal("invalid request sent")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request caused network access")
	}
	_, err := client.Generate(context.Background(), target, nil, valid)
	assertCode(t, err, "rate_limited")
	if calls.Load() != 1 || strings.Contains(err.Error(), "do-not-echo-secret") {
		t.Fatal("retried or leaked body")
	}
	entered := make(chan struct{})
	stopped := make(chan struct{})
	cancelClient, cancelServer := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(stopped)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	target.BaseURL = cancelServer.URL
	go func() { _, err := cancelClient.Generate(ctx, target, nil, valid); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("request remained live")
	}
}
