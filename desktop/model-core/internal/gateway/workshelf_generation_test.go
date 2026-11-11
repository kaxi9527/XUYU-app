package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func workShelfFixture(agent string) provider.Provider {
	return provider.Provider{ID: agent, Account: &provider.Account{Agent: agent, User: "fixture@example.test"}}
}
func TestWorkShelfTextStrictRequestAndSelectedReceiver(t *testing.T) {
	proof, _ := json.Marshal(workShelfProofFixture())
	valid := `{"model":"copilot/fixture","messages":[{"role":"system","content":"instruction"},{"role":"user","content":"synthetic project"}],"max_completion_tokens":64,"stream":false,"recipient":` + string(proof) + `}`
	for _, x := range []struct {
		name, body string
		status     int
	}{
		{"valid", valid, 200},
		{"auto", strings.Replace(valid, "copilot/fixture", "copilot/auto", 1), 400},
		{"group", strings.Replace(valid, "copilot/fixture", "group/fixture", 1), 400},
		{"suffix", strings.Replace(valid, "copilot/fixture", "copilot/fixture:other", 1), 400},
		{"tools", strings.Replace(valid, `"stream":false`, `"stream":false,"tools":[]`, 1), 400},
		{"stream", strings.Replace(valid, `"stream":false`, `"stream":true`, 1), 400},
		{"limit", strings.Replace(valid, ":64", ":8193", 1), 400},
		{"role", strings.Replace(valid, `"role":"user"`, `"role":"assistant"`, 1), 400},
		{"trailing", valid + `{}`, 400},
		{"oversize", strings.Replace(valid, "synthetic project", strings.Repeat("x", 65537), 1), 400},
		{"claude cap unavailable", strings.Replace(valid, "copilot/fixture", "claude/fixture", 1), 422},
	} {
		t.Run(x.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/control/generate-text", strings.NewReader(x.body))
			r = r.WithContext(context.WithValue(r.Context(), workShelfControlKey{}, workShelfTestControl(t)))
			r.Header.Set("X-Magpie-Account", "another")
			w := httptest.NewRecorder()
			calls := 0
			workShelfText(w, r, func(agent string) (provider.Provider, error) { return workShelfFixture(agent), nil }, func(p provider.Provider, model string, body []byte) {
				calls++
				if p.ID != "copilot" || model != "fixture" || len(r.Header) != 0 || workShelfLimit(r.Context()) == nil {
					t.Error("routing boundary lost")
				}
				var parsed map[string]any
				if json.Unmarshal(body, &parsed) != nil || len(parsed) != 4 {
					t.Error("noncanonical request")
				}
			})
			if w.Code != x.status || (calls == 1) != (x.status == 200) {
				t.Fatalf("status=%d calls=%d", w.Code, calls)
			}
		})
	}
	for _, modify := range []func(*provider.Provider){func(p *provider.Provider) { p.ID = "other" }, func(p *provider.Provider) { p.Account.User = "" }, func(p *provider.Provider) { p.Fallback = []string{"other/model"} }} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(valid))
		w := httptest.NewRecorder()
		workShelfText(w, r, func(string) (provider.Provider, error) { p := workShelfFixture("copilot"); modify(&p); return p, nil }, func(provider.Provider, string, []byte) { t.Error("unsafe resolution sent") })
		if w.Code != 409 {
			t.Fatal(w.Code)
		}
	}
}

type workShelfTransport func(*http.Request) (*http.Response, error)

func (f workShelfTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkShelfCodexSendsUncappedOnlyToSelectedEndpoint(t *testing.T) {
	p := workShelfFixture("codex")
	calls := 0
	client := &http.Client{Transport: workShelfTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("fixture")), Request: r}, nil
	})}
	limit := &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, control: workShelfBoundaryPermit()}
	r, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"fixture","input":"synthetic"}`))
	res, err := workShelfSend(p, r, limit, client)
	if err != nil || calls != 1 {
		t.Fatal("codex request failed", err)
	}
	res.Body.Close()
	if _, err := workShelfSend(p, r, limit, client); err == nil || calls != 1 {
		t.Fatal("request sent twice")
	}
	other, _ := http.NewRequest("POST", "https://example.test/responses", strings.NewReader(`{"model":"fixture"}`))
	if _, err := workShelfSend(p, other, &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, control: workShelfBoundaryPermit()}, client); err == nil || calls != 1 {
		t.Fatal("unselected destination accepted")
	}
}

func TestWorkShelfWireCapNoRepeatAndNoRedirect(t *testing.T) {
	for _, status := range []int{200, 307, 400, 401, 429, 500} {
		calls := 0
		client := &http.Client{Transport: workShelfTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://another.example.test"}}, Body: io.NopCloser(strings.NewReader("fixture")), Request: r}, nil
		})}
		p := workShelfFixture("copilot")
		limit := &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, control: workShelfBoundaryPermit()}
		r, _ := http.NewRequest("POST", "https://api.githubcopilot.com/chat/completions", strings.NewReader(`{"max_completion_tokens":64,"messages":[]}`))
		res, err := workShelfSend(p, r, limit, client)
		if err != nil || res.StatusCode != status || calls != 1 {
			t.Fatalf("status=%d calls=%d err=%v", status, calls, err)
		}
		res.Body.Close()
		if _, err := workShelfSend(p, r, limit, client); err == nil || calls != 1 {
			t.Fatal("repeated request sent")
		}
	}
	for _, body := range []string{`{}`, `{"max_tokens":65}`, `{"max_tokens":0}`, `{"max_tokens":1.5}`, `{"max_tokens":"64"}`, `{"max_tokens":64,"max_output_tokens":8192}`} {
		p := workShelfFixture("copilot")
		r, _ := http.NewRequest("POST", "https://api.githubcopilot.com/chat/completions", strings.NewReader(body))
		client := &http.Client{Transport: workShelfTransport(func(*http.Request) (*http.Response, error) { t.Error("uncapped body left process"); return nil, nil })}
		if _, err := workShelfSend(p, r, &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User}, client); err == nil {
			t.Fatal("invalid cap accepted")
		}
	}
}

func TestWorkShelfCodeAssistFinalEnvelopeIsBounded(t *testing.T) {
	for _, x := range []struct {
		body string
		sent bool
	}{
		{`{"request":{"generationConfig":{"maxOutputTokens":64}}}`, true},
		{`{"max_tokens":64,"request":{}}`, false},
		{`{"request":{"generationConfig":{"maxOutputTokens":65}}}`, false},
		{`{"request":{"tools":[{"googleSearch":{}}],"generationConfig":{"maxOutputTokens":64}}}`, false},
	} {
		p := workShelfFixture("gemini")
		r, _ := http.NewRequest("POST", "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", strings.NewReader(x.body))
		calls := 0
		client := &http.Client{Transport: workShelfTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("fixture")), Header: make(http.Header), Request: r}, nil
		})}
		res, err := workShelfSend(p, r, &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, control: workShelfBoundaryPermit()}, client)
		if (err == nil) != x.sent || (calls == 1) != x.sent {
			t.Fatal("Code Assist cap not enforced")
		}
		if res != nil {
			res.Body.Close()
		}
	}
}
