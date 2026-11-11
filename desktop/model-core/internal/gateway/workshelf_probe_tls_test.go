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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// Exercises the actual dedicated handlers, gateway encoding and final send.
// Only account resolution/identity and a loopback TLS trust root are synthetic.
// The CONNECT proxy refuses every host except this fixture's native destination.
func TestWorkShelfProbeAndGenerateNativePipelineTLS(t *testing.T) {
	t.Setenv("WORKSHELF_MODEL_HOME", t.TempDir())
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	var calls atomic.Int32
	var invalid atomic.Bool
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var b struct {
			Model    string
			Max      int `json:"max_completion_tokens"`
			Messages []struct{ Role, Content string }
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Model != "fixture" || b.Max != 64 || len(b.Messages) != 1 || r.URL.Path != "/chat/completions" {
			t.Error("wire contract lost")
		}
		if b.Messages[0].Content != workShelfProbeText && b.Messages[0].Content != "synthetic-project" {
			t.Error("unexpected content")
		}
		finish := "stop"
		if invalid.Load() {
			finish = "length"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"role": "assistant", "content": "OK"}}}})
	}))
	defer up.Close()
	var tunnels sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "api.githubcopilot.com:443" {
			t.Error("escaped native host")
			w.WriteHeader(403)
			return
		}
		remote, e := net.Dial("tcp", up.Listener.Addr().String())
		if e != nil {
			w.WriteHeader(502)
			return
		}
		local, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		tunnels.Add(1)
		defer tunnels.Done()
		defer local.Close()
		defer remote.Close()
		io.WriteString(local, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { io.Copy(remote, local); remote.Close(); close(done) }()
		io.Copy(local, remote)
		local.Close()
		<-done
	}))
	defer func() { proxy.Close(); tunnels.Wait() }()
	oldTLS := upstreamTLS
	upstreamTLS = &tls.Config{InsecureSkipVerify: true}
	defer func() { upstreamTLS = oldTLS }() // loopback fixture only
	s := New()
	proof := workShelfProofFixture()
	s.workShelf.resolve = func(agent string) (provider.Provider, error) {
		p := workShelfFixture(agent)
		p.Chat = "https://api.githubcopilot.com"
		p.Proxy = proxy.URL
		return p, nil
	}
	s.workShelf.identity = func(provider.Provider) (string, string, string, error) {
		return proof.AccountID, proof.AccountLabel, "fixture-generation", nil
	}
	post := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/control", strings.NewReader(body)))
		return w
	}
	probe := func() provider.WorkShelfRecipient {
		w := post(s.WorkShelfProbe, `{"model":"copilot/fixture"}`)
		if w.Code != 200 {
			t.Fatalf("probe status %d: %s", w.Code, w.Body.String())
		}
		var out struct{ Recipient provider.WorkShelfRecipient }
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Recipient.Valid() {
			t.Fatal("proof not returned")
		}
		return out.Recipient
	}
	first := probe()
	if calls.Load() != 1 {
		t.Fatal("probe repeated")
	}
	w := post(s.WorkShelfRecipient, `{"model":"copilot/fixture"}`)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal("lookup invoked model")
	}
	body := func(p provider.WorkShelfRecipient) string {
		b, _ := json.Marshal(map[string]any{"model": "copilot/fixture", "messages": []map[string]string{{"role": "user", "content": "synthetic-project"}}, "max_completion_tokens": 64, "stream": false, "recipient": p})
		return string(b)
	}
	w = post(s.WorkShelfText, body(first))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("generation status=%d calls=%d %s", w.Code, calls.Load(), w.Body.String())
	}
	second := probe()
	if first.ProofID == second.ProofID || calls.Load() != 3 {
		t.Fatal("retest reused proof")
	}
	w = post(s.WorkShelfText, body(first))
	if w.Code != 409 || calls.Load() != 3 {
		t.Fatal("old proof sent")
	}
	invalid.Store(true)
	w = post(s.WorkShelfProbe, `{"model":"copilot/fixture"}`)
	if w.Code == 200 || calls.Load() != 4 {
		t.Fatal("partial probe published")
	}
	if _, e := s.workShelf.current(first.ModelID); e == nil {
		t.Fatal("failed probe restored proof")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/control", strings.NewReader(`{"model":"copilot/fixture"}`)).WithContext(ctx)
	w = httptest.NewRecorder()
	s.WorkShelfProbe(w, r)
	if w.Code == 200 || calls.Load() != 4 {
		t.Fatal("cancelled probe sent")
	}
}
