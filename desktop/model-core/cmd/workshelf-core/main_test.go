package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestPrivateGenerationNeedsOwnTokenAndNoOrigin(t *testing.T) {
	calls := 0
	c := &core{key: strings.Repeat("a", 64), generation: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })}
	for _, x := range []struct {
		token, origin string
		status        int
	}{{"", "", 401}, {"Bearer magpie", "", 401}, {"Bearer " + c.key, "https://example.test", 401}, {"Bearer " + c.key, "", 204}} {
		r := httptest.NewRequest("POST", "/control/generate-text", strings.NewReader(`{}`))
		r.Header.Set("Authorization", x.token)
		r.Header.Set("Origin", x.origin)
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		if w.Code != x.status {
			t.Errorf("status=%d want=%d", w.Code, x.status)
		}
	}
	if calls != 1 {
		t.Fatal("untrusted callers reached generation")
	}
}

func TestRecipientControlRoutesRequirePrivateToken(t *testing.T) {
	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })
	c := &core{key: strings.Repeat("b", 64), probe: handler, recipient: handler, generation: handler}
	for _, path := range []string{"/control/probe-text", "/control/generation-recipient", "/control/generate-text"} {
		for _, x := range []struct {
			method, token, origin string
			status                int
		}{{"POST", "", "", 401}, {"POST", "Bearer magpie", "", 401}, {"POST", "Bearer " + c.key, "https://example.test", 401}, {"GET", "Bearer " + c.key, "", 404}, {"POST", "Bearer " + c.key, "", 204}} {
			r := httptest.NewRequest(x.method, path, strings.NewReader(`{}`))
			r.Header.Set("Authorization", x.token)
			r.Header.Set("Origin", x.origin)
			w := httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code != x.status {
				t.Fatalf("%s %s status %d", x.method, path, w.Code)
			}
		}
	}
	if calls != 3 {
		t.Fatal("private boundary bypass")
	}
}

func TestPrivateAPI(t *testing.T) {
	t.Setenv("WORKSHELF_MODEL_HOME", t.TempDir())
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	c := &core{key: strings.Repeat("a", 64), flows: map[string]bool{}, inference: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer magpie" {
			t.Error("missing internal handler authorization")
		}
		reply(w, 200, map[string]bool{"inference": true})
	})}
	call := func(method, path string, body any, token, origin string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", token)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w
	}
	auth := "Bearer " + c.key
	for _, x := range []struct{ token, origin string }{{"", ""}, {"Bearer magpie", ""}, {auth, "https://example.com"}} {
		if w := call("GET", "/control/state", nil, x.token, x.origin); w.Code != 401 {
			t.Fatalf("unauthorized status %d", w.Code)
		}
	}
	for _, path := range []string{"/", "/api/hello", "/v1/keys", "/control/unknown"} {
		if w := call("GET", path, nil, auth, ""); w.Code != 404 {
			t.Errorf("exposed %s", path)
		}
	}
	if w := call("GET", "/v1/models", nil, auth, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini"} {
		in := providerInput{Name: "fixture " + protocol, Protocol: protocol, BaseURL: "http://127.0.0.1:12345/v1", Key: "fixture-private-key", Models: []string{"vision"}}
		w := call("POST", "/control/providers", in, auth, "")
		if w.Code != 200 {
			t.Fatalf("save %s: %s", protocol, w.Body.String())
		}
		var result map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		in.ID = result["id"]
		in.Key = ""
		if _, err := saveProvider(in); err != nil {
			t.Fatal(err)
		}
		ps, _ := provider.Stored()
		found := false
		for _, p := range ps {
			if p.ID == in.ID {
				found = true
				if p.Key != "fixture-private-key" {
					t.Fatal("blank edit dropped key")
				}
			}
		}
		if !found {
			t.Fatal("missing provider")
		}
		in.BaseURL = "http://127.0.0.1:12346/v1"
		if _, err := saveProvider(in); err != nil {
			t.Fatal(err)
		}
		p, _ := stored(in.ID)
		if p.Key != "" {
			t.Fatal("key leaked to changed endpoint")
		}
	}
	w := call("GET", "/control/state", nil, auth, "")
	if strings.Contains(w.Body.String(), "fixture-private-key") || strings.Contains(w.Body.String(), "apiKey") {
		t.Fatal("credentials exposed")
	}
	if w := call("POST", "/control/providers", providerInput{Name: "bad", Protocol: "openai", BaseURL: "https://user:secret@example.com"}, auth, ""); w.Code != 400 {
		t.Fatal("accepted credentials in URL")
	}
	if w := call("POST", "/control/login", map[string]string{"agent": "arbitrary-plugin"}, auth, ""); w.Code != 400 {
		t.Fatal("accepted plugin login")
	}
	if w := call("GET", "/control/login/unknown", nil, auth, ""); w.Code != 404 {
		t.Fatal("accepted unknown login")
	}
}
