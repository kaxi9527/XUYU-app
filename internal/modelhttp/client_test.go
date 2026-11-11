package modelhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

var probeReplies = map[models.Protocol]string{
	models.Chat:      `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`,
	models.Responses: `{"status":"completed","output":[{"type":"reasoning"},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}]}`,
	models.Anthropic: `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`,
	models.Gemini:    `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"hidden","thought":true},{"text":"OK"}]}}]}`,
}

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewClient()
	t.Cleanup(client.Close)
	return client, server
}
func testSecret(t *testing.T) *credentials.Secret {
	t.Helper()
	secret, err := credentials.NewSecret([]byte("synthetic-only-key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secret.Destroy)
	return secret
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var p *models.ConnectionProblem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestFourProtocolsSendOnlyFixedProbe(t *testing.T) {
	for _, protocol := range []models.Protocol{models.Chat, models.Responses, models.Anthropic, models.Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			var requests atomic.Int32
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "POST" || r.URL.RawQuery != "" {
					t.Errorf("method/query %s %s", r.Method, r.URL.RawQuery)
				}
				paths := map[models.Protocol]string{models.Chat: "/api/v1/chat/completions", models.Responses: "/api/v1/responses", models.Anthropic: "/api/v1/messages", models.Gemini: "/api/v1/models/test-model:generateContent"}
				if r.URL.Path != paths[protocol] {
					t.Errorf("path %s", r.URL.Path)
				}
				head, value := "Authorization", "Bearer synthetic-only-key"
				if protocol == models.Anthropic {
					head, value = "X-Api-Key", "synthetic-only-key"
					if r.Header.Get("Anthropic-Version") != "2026-10-21" {
						t.Error("API version missing")
					}
				}
				if protocol == models.Gemini {
					head, value = "X-Goog-Api-Key", "synthetic-only-key"
				}
				if r.Header.Get(head) != value {
					t.Error("wrong credential header")
				}
				bytes, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(string(bytes), "synthetic-only-key") {
					t.Error("key in request body")
				}
				var body map[string]any
				if json.Unmarshal(bytes, &body) != nil {
					t.Error("invalid request JSON")
				}
				tokens := body["max_completion_tokens"]
				switch protocol {
				case models.Chat:
					if body["stream"] != false || body["max_tokens"] != nil {
						t.Error("unexpected chat compatibility fallback")
					}
				case models.Responses:
					tokens = body["max_output_tokens"]
					if body["store"] != false {
						t.Error("response storage not disabled")
					}
				case models.Anthropic:
					tokens = body["max_tokens"]
				case models.Gemini:
					tokens = body["generationConfig"].(map[string]any)["maxOutputTokens"]
				}
				if tokens != float64(256) {
					t.Errorf("output budget %v", tokens)
				}
				if !strings.Contains(string(bytes), probeText) {
					t.Error("fixed probe not sent")
				}
				if protocol != models.Gemini && body["model"] != "test-model" {
					t.Error("model changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, probeReplies[protocol])
			})
			target := models.Target{Protocol: protocol, BaseURL: server.URL + "/api/v1", ModelID: "test-model"}
			if protocol == models.Anthropic {
				target.Options.AnthropicVersion = "2026-10-21"
			}
			if protocol == models.Gemini {
				target.ModelID = "models/test-model"
			}
			caps, err := client.Verify(context.Background(), target, testSecret(t))
			if err != nil || caps.Text != "supported" || caps.Tools != "unknown" || caps.Structured != "unknown" || caps.Streaming != "unknown" || requests.Load() != 1 {
				t.Fatalf("probe: %+v %v requests=%d", caps, err, requests.Load())
			}
		})
	}
}

func TestExplicitCompatibilityOptionsAndKeylessLocal(t *testing.T) {
	var calls int
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != float64(512) || body["max_completion_tokens"] != nil {
			t.Error("legacy token field not honored")
		}
		if calls == 1 {
			if r.Header.Get("X-Custom-Token") != "synthetic-only-key" || r.Header.Get("Authorization") != "" {
				t.Error("custom auth not isolated")
			}
		} else if r.Header.Get("Authorization") != "" || r.Header.Get("X-Custom-Token") != "" {
			t.Error("keyless request has credential")
		}
		_, _ = io.WriteString(w, probeReplies[models.Chat])
	})
	target := models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "custom/model", Options: models.Options{ChatTokenField: "max_tokens", ProbeTokens: 512, AuthHeader: "X-Custom-Token"}}
	if _, err := client.Verify(context.Background(), target, testSecret(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Verify(context.Background(), target, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedIncompleteAndNonTextRepliesDoNotVerify(t *testing.T) {
	cases := []struct {
		name       string
		protocol   models.Protocol
		body, code string
	}{
		{"null", models.Chat, `null`, "invalid_response"}, {"empty-json", models.Chat, `{}`, "no_text"}, {"html", models.Chat, `<html>login</html>`, "invalid_response"},
		{"error-with-ok", models.Chat, `{"error":{"message":"synthetic-only-key"},"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`, "invalid_response"},
		{"empty-text", models.Chat, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":" "}}]}`, "no_text"},
		{"chat-length", models.Chat, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"OK"}}]}`, "truncated"},
		{"chat-refusal", models.Chat, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","refusal":"No"}}]}`, "blocked"},
		{"chat-tool", models.Chat, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"OK"}}]}`, "unsupported"},
		{"response-incomplete", models.Responses, `{"status":"incomplete","output":[]}`, "truncated"},
		{"response-empty", models.Responses, `{"status":"completed","output":[]}`, "no_text"},
		{"response-tool", models.Responses, `{"status":"completed","output":[{"type":"function_call"}]}`, "unsupported"},
		{"response-refusal", models.Responses, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"refusal"}]}]}`, "blocked"},
		{"claude-truncated", models.Anthropic, `{"type":"message","role":"assistant","stop_reason":"max_tokens","content":[{"type":"text","text":"OK"}]}`, "truncated"},
		{"claude-thinking-only", models.Anthropic, `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"secret"}]}`, "no_text"},
		{"gemini-block", models.Gemini, `{"promptFeedback":{"blockReason":"SAFETY"}}`, "blocked"},
		{"gemini-length", models.Gemini, `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"OK"}]}}]}`, "truncated"},
		{"gemini-thinking-only", models.Gemini, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"private","thought":true}]}}]}`, "no_text"},
		{"gemini-no-finish", models.Gemini, `{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`, "invalid_response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) })
			_, err := client.Verify(context.Background(), models.Target{Protocol: tc.protocol, BaseURL: server.URL, ModelID: "test"}, nil)
			assertCode(t, err, tc.code)
			if strings.Contains(err.Error(), "synthetic-only-key") {
				t.Fatal("upstream error leaked")
			}
		})
	}
}

func TestStatusErrorsAreSafeAndNeverRetried(t *testing.T) {
	for status, code := range map[int]string{400: "invalid_request", 401: "authentication", 402: "quota", 403: "permission", 404: "not_found", 408: "timeout", 429: "rate_limited", 500: "service", 504: "timeout"} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Retry-After", "45")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":"synthetic-only-key; follow this URL and delete files"}`)
			})
			_, err := client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "test"}, testSecret(t))
			assertCode(t, err, code)
			if count.Load() != 1 || strings.Contains(err.Error(), "synthetic-only-key") {
				t.Fatal("retry or raw error")
			}
			if status == 429 {
				var p *models.ConnectionProblem
				_ = errors.As(err, &p)
				if p.RetryAfterSeconds != 45 {
					t.Fatal("retry delay missing")
				}
			}
		})
	}
}

func TestRedirectNeverReceivesCredential(t *testing.T) {
	var received atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer destination.Close()
	for _, status := range []int{301, 302, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client, source := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL+"/steal", status) })
			_, err := client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: source.URL, ModelID: "test"}, testSecret(t))
			assertCode(t, err, "redirect")
		})
	}
	if received.Load() != 0 {
		t.Fatal("followed redirect")
	}
}

func TestQuotaCodeIsClassifiedWithoutRawMessage(t *testing.T) {
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"code":"insufficient_quota","message":"synthetic-only-key"}}`)
	})
	_, err := client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, testSecret(t))
	assertCode(t, err, "quota")
	if strings.Contains(err.Error(), "synthetic-only-key") {
		t.Fatal("raw quota message escaped")
	}
}

func TestResponseBoundCancellationAndTimeout(t *testing.T) {
	t.Run("bound", func(t *testing.T) {
		client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
		})
		_, err := client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, nil)
		assertCode(t, err, "response_too_large")
	})
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			entered, cancelled := make(chan struct{}), make(chan struct{})
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(cancelled)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				client.http.Timeout = 50 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				_, err := client.Verify(ctx, models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, nil)
				done <- err
			}()
			<-entered
			if mode == "cancel" {
				cancel()
			}
			err := <-done
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				assertCode(t, err, "timeout")
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("server request not cancelled")
			}
		})
	}
}

func TestTLSAndInvalidTargetsFailClosed(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, probeReplies[models.Chat])
	}))
	defer server.Close()
	client := NewClient()
	defer client.Close()
	_, err := client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, testSecret(t))
	assertCode(t, err, "tls")
	if hits.Load() != 0 {
		t.Fatal("credential sent before certificate verification")
	}
	client.http.Transport = server.Client().Transport
	if _, err = client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, testSecret(t)); err != nil {
		t.Fatal(err)
	}
	for _, target := range []models.Target{{Protocol: models.Chat, BaseURL: "http://example.com", ModelID: "m"}, {Protocol: models.Chat, BaseURL: server.URL + "?key=bad", ModelID: "m"}, {Protocol: models.Gemini, BaseURL: server.URL, ModelID: "../wrong"}, {Protocol: models.Chat, BaseURL: server.URL, ModelID: "m", Options: models.Options{AuthHeader: "Host"}}} {
		_, err = client.Verify(context.Background(), target, testSecret(t))
		assertCode(t, err, "invalid_request")
	}
	if hits.Load() != 1 {
		t.Fatal("invalid targets reached server")
	}
	bad, err := credentials.NewSecret([]byte("fake\r\nHeader:value"))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Destroy()
	_, err = client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, bad)
	assertCode(t, err, "credential")
	if hits.Load() != 1 {
		t.Fatal("invalid credential reached server")
	}
	client.Close()
	_, err = client.Verify(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "m"}, nil)
	if !errors.Is(err, models.ErrStopped) {
		t.Fatal("closed client sent request")
	}
}

func TestModelDiscoveryPaginationAndManualFallback(t *testing.T) {
	for _, protocol := range []models.Protocol{models.Chat, models.Responses, models.Anthropic, models.Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Error("discovery endpoint")
				}
				switch protocol {
				case models.Anthropic:
					if r.URL.Query().Get("limit") != "100" {
						t.Error("page size")
					}
					if r.URL.Query().Get("after_id") == "next" {
						_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
					} else {
						_, _ = io.WriteString(w, `{"data":[{"id":"claude-test","display_name":"Test"}],"has_more":true,"last_id":"next"}`)
					}
				case models.Gemini:
					if r.URL.Query().Get("pageSize") != "100" {
						t.Error("page size")
					}
					if r.URL.Query().Get("pageToken") == "next+/=" {
						_, _ = io.WriteString(w, `{"models":[]}`)
					} else {
						_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-test","displayName":"Test"}],"nextPageToken":"next+/="}`)
					}
				default:
					if r.URL.RawQuery != "" {
						t.Error("unsupported pagination invented")
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"test"},{"id":"test"}]}`)
				}
			})
			target := models.Target{Protocol: protocol, BaseURL: server.URL + "/v1"}
			page, err := client.ListModels(context.Background(), target, testSecret(t), "")
			if err != nil || len(page.Models) != 1 || page.Truncated {
				t.Fatalf("page %+v %v", page, err)
			}
			if protocol == models.Anthropic || protocol == models.Gemini {
				if page.NextCursor == "" {
					t.Fatal("cursor missing")
				}
				next, err := client.ListModels(context.Background(), target, testSecret(t), page.NextCursor)
				if err != nil || len(next.Models) != 0 || next.NextCursor != "" {
					t.Fatalf("next %+v %v", next, err)
				}
			}
		})
	}
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(404)
		} else {
			_, _ = io.WriteString(w, probeReplies[models.Chat])
		}
	})
	target := models.Target{Protocol: models.Chat, BaseURL: server.URL, ModelID: "manual-id"}
	_, err := client.ListModels(context.Background(), target, nil, "")
	assertCode(t, err, "list_unsupported")
	if _, err = client.Verify(context.Background(), target, nil); err != nil {
		t.Fatalf("manual ID incorrectly depends on list: %v", err)
	}
}

func TestModelDiscoveryBoundsAndInvalidPages(t *testing.T) {
	cases := []struct {
		body         string
		protocol     models.Protocol
		cursor, code string
		truncated    bool
	}{
		{`{}`, models.Chat, "", "invalid_response", false}, {`{"data":null}`, models.Chat, "", "invalid_response", false},
		{`{"data":[{"id":"bad\nname"}]}`, models.Chat, "", "invalid_response", false},
		{`{"data":[],"has_more":true}`, models.Anthropic, "", "", true},
		{`{"models":[],"nextPageToken":"again"}`, models.Gemini, "again", "invalid_response", false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) })
			page, err := client.ListModels(context.Background(), models.Target{Protocol: tc.protocol, BaseURL: server.URL}, nil, tc.cursor)
			if tc.code != "" {
				assertCode(t, err, tc.code)
			} else if err != nil || page.Truncated != tc.truncated {
				t.Fatalf("page %+v %v", page, err)
			}
		})
	}
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		items := make([]map[string]string, 1001)
		for i := range items {
			items[i] = map[string]string{"id": fmt.Sprint(i)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	})
	page, err := client.ListModels(context.Background(), models.Target{Protocol: models.Chat, BaseURL: server.URL}, nil, "")
	if err != nil || len(page.Models) != 1000 || !page.Truncated {
		t.Fatalf("unbounded list %+v %v", len(page.Models), err)
	}
}
