package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func workShelfProofFixture() provider.WorkShelfRecipient {
	return provider.WorkShelfRecipient{Schema: 1, ProofID: strings.Repeat("a", 64), AccountID: strings.Repeat("b", 64), AccountLabel: "Copilot · te••••st", Agent: "copilot", ModelID: "copilot/fixture", Endpoint: "https://api.githubcopilot.com/chat/completions", WireModel: "fixture"}
}
func workShelfProofStore(t *testing.T) (*workShelfProofs, *atomic.Int64, provider.WorkShelfRecipient) {
	t.Helper()
	var generation atomic.Int64
	generation.Store(1)
	r := workShelfProofFixture()
	s := &workShelfProofs{resolve: func(agent string) (provider.Provider, error) { return workShelfFixture(agent), nil }, identity: func(provider.Provider) (string, string, string, error) {
		return r.AccountID, r.AccountLabel, time.Unix(generation.Load(), 0).String(), nil
	}}
	ticket, seal, e := s.begin(r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.publish(context.Background(), r.ModelID, ticket, seal, workShelfProofEntry{public: r, seal: seal}); e != nil {
		t.Fatal(e)
	}
	return s, &generation, r
}
func workShelfTestControl(t *testing.T) *workShelfControl {
	s, _, _ := workShelfProofStore(t)
	return &workShelfControl{proofs: s}
}

// These fixtures isolate the pre-existing cap/replay tests. The recipient tests
// below exercise the real authorization path; this seam is never HTTP-accessible.
func workShelfBoundaryPermit() *workShelfControl {
	return &workShelfControl{permit: func(ctx context.Context, _ provider.Provider, _ *http.Request, _ map[string]any, _ string, _ *provider.WorkShelfRecipient) (context.Context, func(), error) {
		return ctx, func() {}, nil
	}}
}
func TestWorkShelfProofRevocationAndLatePublish(t *testing.T) {
	s, g, r := workShelfProofStore(t)
	if got, e := s.current(r.ModelID); e != nil || got != r {
		t.Fatal(e)
	}
	g.Store(2)
	if _, e := s.current(r.ModelID); e == nil {
		t.Fatal("same account reauthorization retained proof")
	}
	g.Store(1)
	one, seal, e := s.begin(r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.current(r.ModelID); e == nil {
		t.Fatal("probe did not revoke old proof")
	}
	two, _, e := s.begin(r.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	entry := workShelfProofEntry{public: r, seal: seal}
	if e = s.publish(context.Background(), r.ModelID, one, seal, entry); e == nil {
		t.Fatal("late probe restored proof")
	}
	s.abandon(r.ModelID, one)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.publish(ctx, r.ModelID, two, seal, entry); e == nil {
		t.Fatal("cancelled probe published")
	}
	s.abandon(r.ModelID, two)
	if _, e = s.current(r.ModelID); e == nil {
		t.Fatal("failed probe restored old proof")
	}
	fresh := &workShelfProofs{resolve: s.resolve, identity: s.identity}
	if _, e = fresh.current(r.ModelID); e == nil {
		t.Fatal("restart retained proof")
	}
}
func TestWorkShelfFinalProofBindingsAndCancellation(t *testing.T) {
	for _, kind := range []string{"same", "path", "wire", "tenant", "generation", "retest"} {
		t.Run(kind, func(t *testing.T) {
			s, g, proof := workShelfProofStore(t)
			p := workShelfFixture("copilot")
			ctrl := &workShelfControl{proofs: s}
			url := proof.Endpoint
			body := map[string]any{"model": "fixture"}
			switch kind {
			case "path":
				url = "https://api.githubcopilot.com/responses"
			case "wire":
				body["model"] = "different"
			case "tenant":
				body["project"] = "another"
			case "generation":
				g.Store(2)
			case "retest":
				s.begin(proof.ModelID)
			}
			req, _ := http.NewRequest("POST", url, nil)
			ctx, done, e := ctrl.authorize(context.Background(), p, req, body, proof.ModelID, &proof)
			if kind != "same" {
				if e == nil {
					done()
					t.Fatal("changed recipient authorized")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer done()
			s.begin(proof.ModelID)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("retest did not cancel already-started use")
			}
			if _, _, e = ctrl.authorize(context.Background(), p, req, body, proof.ModelID, &proof); e == nil {
				t.Fatal("revoked proof dispatched")
			}
		})
	}
}
func TestWorkShelfFixedProbeAndReadOnlyQuery(t *testing.T) {
	s, _, proof := workShelfProofStore(t)
	server := &Server{}
	server.workShelf.resolve = s.resolve
	server.workShelf.identity = s.identity
	for _, body := range []string{`{"model":"copilot/fixture","messages":[{"content":"project"}]}`, `{"model":"copilot/fixture","max_completion_tokens":64}`, `{"model":"copilot/fixture","model":"other"}`, `{"model":"claude/fixture"}`, `{"model":"copilot/fixture"}{}`} {
		w := httptest.NewRecorder()
		server.WorkShelfProbe(w, httptest.NewRequest("POST", "/control/probe-text", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	// The public input parser can supply no text. The constructed request is
	// checked a second time, before any signing or native transport is reached.
	for _, content := range []string{workShelfProbeText, "project content"} {
		body, _ := json.Marshal(map[string]any{"model": proof.ModelID, "messages": []map[string]string{{"role": "user", "content": content}}, "max_completion_tokens": 64, "stream": false})
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		r = r.WithContext(context.WithValue(r.Context(), workShelfControlKey{}, &workShelfControl{proofs: s, probe: true}))
		calls := 0
		w := httptest.NewRecorder()
		workShelfText(w, r, s.resolve, func(provider.Provider, string, []byte) { calls++ })
		if (calls == 1) != (content == workShelfProbeText) {
			t.Fatal("caller controlled probe body")
		}
	}
	// Install only a local cache entry; lookup invokes neither native signing
	// nor generation and returns the exact proof, without a refresh/test call.
	ticket, seal, e := server.workShelf.begin(proof.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	server.workShelf.publish(context.Background(), proof.ModelID, ticket, seal, workShelfProofEntry{public: proof, seal: seal})
	w := httptest.NewRecorder()
	server.WorkShelfRecipient(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"model":"copilot/fixture"}`)))
	var got provider.WorkShelfRecipient
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got != proof {
		t.Fatal("local lookup changed proof")
	}
}
func TestWorkShelfProbeCompletionContract(t *testing.T) {
	valid := `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}],"usage":{"completion_tokens":1}}`
	if !workShelfProbeComplete([]byte(valid)) {
		t.Fatal("valid response")
	}
	for _, bad := range []string{strings.Replace(valid, `"stop"`, `"length"`, 1), strings.Replace(valid, `"OK"`, `""`, 1), strings.Replace(valid, `"OK"`, `"\u0000"`, 1), strings.Replace(valid, `"content":"OK"`, `"content":"OK","tool_calls":[{}]`, 1), strings.Replace(valid, `"content":"OK"`, `"content":"OK","refusal":"denied"`, 1), strings.Replace(valid, `"completion_tokens":1`, `"completion_tokens":-1`, 1), strings.Replace(valid, `"choices":`, `"error":{},"choices":`, 1), valid + `{}`} {
		if workShelfProbeComplete([]byte(bad)) {
			t.Fatal("invalid probe published")
		}
	}
}
func TestWorkShelfProofFinalTLSBoundary(t *testing.T) {
	var calls atomic.Int32
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "synthetic-project") {
			t.Error("wrong body")
		}
		calls.Add(1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer tlsServer.Close()
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", tlsServer.Listener.Addr().String())
	}}
	defer tr.CloseIdleConnections()
	s, g, proof := workShelfProofStore(t)
	p := workShelfFixture("copilot")
	for _, kind := range []string{"valid", "path", "wire", "same-user-reauth"} {
		url, model := proof.Endpoint, "fixture"
		if kind == "path" {
			url = "https://api.githubcopilot.com/responses"
		}
		if kind == "wire" {
			model = "changed"
		}
		if kind == "same-user-reauth" {
			g.Store(2)
		}
		req, _ := http.NewRequest("POST", url, strings.NewReader(`{"model":"`+model+`","max_completion_tokens":64,"messages":[{"role":"user","content":"synthetic-project"}]}`))
		limit := &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, model: proof.ModelID, expected: &proof, control: &workShelfControl{proofs: s}}
		response, e := workShelfSend(p, req, limit, &http.Client{Transport: tr})
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		if (e == nil) != (kind == "valid") || calls.Load() != 1 {
			t.Fatal("TLS egress binding", kind, e, calls.Load())
		}
	}
}
