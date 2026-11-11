package modelhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestDebitSourceAndAmbiguity(t *testing.T) {
	target := models.Target{Protocol: models.Chat, BaseURL: "https://openrouter.ai/api/v1"}
	for _, raw := range []string{`{}`, `{"usage":null}`, `{"usage":{"cost":null}}`, `{"usage":{"cost":"0.1"}}`, `{"usage":{"cost":-1}}`, `{"usage":{"cost":1e-19}}`, `{"usage":{"cost":0.1,"cost":0}}`, `{"usage":{"cost":0.1},"usage":{"cost":0}}`, `{"usage":{"cost":0.1,"is_byok":"false"}}`, `{"usage":{"cost":0.1,"is_byok":true,"is_byok":false}}`, `{"usage":{"cost":0.1}} {}`} {
		if d := openRouterDebit(target, []byte(raw)); d != nil {
			t.Fatalf("ambiguous receipt accepted: %s", raw)
		}
	}
	for _, endpoint := range []string{"https://fixture.invalid/api/v1", "https://openrouter.ai.attacker.invalid/api/v1", "https://openrouter.ai/other", "https://openrouter.ai/api/v1?route=other", "http://openrouter.ai/api/v1", "https://eu.openrouter.ai/api/v1"} {
		foreign := target
		foreign.BaseURL = endpoint
		if openRouterDebit(foreign, []byte(`{"usage":{"cost":0}}`)) != nil {
			t.Fatalf("unverified endpoint: %s", endpoint)
		}
	}
	for _, protocol := range []models.Protocol{models.Subscription, models.Anthropic, models.Gemini} {
		foreign := target
		foreign.Protocol = protocol
		if openRouterDebit(foreign, []byte(`{"usage":{"cost":0}}`)) != nil {
			t.Fatal("wrong protocol")
		}
	}
	for _, byok := range []string{"true", "false", "null"} {
		d := openRouterDebit(target, []byte(`{"usage":{"cost":0,"is_byok":`+byok+`}}`))
		if d == nil || d.Amount != "0" || (byok == "null") != (d.BYOK == nil) || d.BYOK != nil && *d.BYOK != (byok == "true") {
			t.Fatalf("byok %s: %+v", byok, d)
		}
	}
}

type debitFixtureTransport func(*http.Request) (*http.Response, error)

func (f debitFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Production request/decoder pipeline with the final network hop redirected by
// a test-only transport to local HTTP. No provider account or TLS claim is made.
func TestGenerationDebitLocalHTTP(t *testing.T) {
	for _, protocol := range []models.Protocol{models.Chat, models.Responses} {
		for _, cost := range []string{"1.23456789012345678e-1", "0", `"invalid"`} {
			t.Run(string(protocol)+cost, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body, _ := io.ReadAll(r.Body)
					if strings.Contains(string(body), `"usage"`) || r.Header.Get("Authorization") == "" {
						t.Error("unexpected request change")
					}
					io.WriteString(w, strings.TrimSuffix(probeReplies[protocol], "}")+`,"usage":{"cost":`+cost+`,"is_byok":true}}`)
				}))
				defer server.Close()
				local, _ := url.Parse(server.URL)
				client := NewClient()
				defer client.Close()
				client.http.Transport = debitFixtureTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host != "openrouter.ai" {
						t.Fatal("recipient changed")
					}
					r = r.Clone(r.Context())
					r.URL.Scheme = local.Scheme
					r.URL.Host = local.Host
					return server.Client().Do(r)
				})
				result, err := client.Generate(context.Background(), models.Target{Protocol: protocol, BaseURL: "https://openrouter.ai/api/v1", ModelID: "fixture"}, testSecret(t), models.GenerationRequest{Input: "synthetic", MaxOutputTokens: 64})
				if err != nil || result.Text != "OK" || calls != 1 {
					t.Fatalf("generation failed: %+v %v %d", result, err, calls)
				}
				if cost == `"invalid"` {
					if result.Usage.OpenRouterDebit != nil {
						t.Fatal("invented debit")
					}
					return
				}
				want, _ := models.NormalizeDebitAmount(cost)
				if d := result.Usage.OpenRouterDebit; d == nil || d.Amount != want || d.BYOK == nil || !*d.BYOK {
					t.Fatal("lost reported debit")
				}
			})
		}
	}
}
