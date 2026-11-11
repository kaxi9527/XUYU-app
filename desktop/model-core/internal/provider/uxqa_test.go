package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
)

func TestUXQAAccountIsolationAndAuthorization(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSHELF_MODEL_HOME", root)
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	if err := downloadClaudeCode(context.Background()); err == nil || !strings.Contains(err.Error(), "不自动下载") {
		t.Fatal("automatic runtime install was not blocked")
	}
	foreign := filepath.Join(t.TempDir(), "credential.json")
	_ = os.WriteFile(foreign, []byte("foreign credential unchanged"), 0600)
	if _, err := readAccountFile(foreign); err == nil {
		t.Fatal("read foreign credential")
	}
	alias := filepath.Join(root, "symlink.json")
	_ = os.Symlink(foreign, alias)
	if _, err := readAccountFile(alias); err == nil {
		t.Fatal("read foreign credential through symlink")
	}
	for _, p := range []string{appdir.CodexHomeIn("/foreign"), appdir.ClaudeDirIn("/foreign"), geminiDir(), copilotCLIHome(), copilotConfigDir()} {
		if !appdir.UXQAOwn(p) {
			t.Fatal("foreign client path")
		}
	}
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.test", "https://api.openai.com/auth": map[string]string{"chatgpt_plan_type": "plus", "chatgpt_account_id": "fixture-account"}})
	jwt := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	var challenge string
	exchanges := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
			t.Error("PKCE verifier mismatch")
		}
		if r.Form.Get("code") != "fixture-code" {
			t.Error("wrong authorization code")
		}
		exchanges++
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": jwt, "access_token": "fixture-access-token", "refresh_token": "fixture-refresh-token"})
	}))
	defer tokenServer.Close()
	oldToken, oldCallback := codexTokenURL, codexCallbackAddr
	codexTokenURL, codexCallbackAddr = tokenServer.URL, "127.0.0.1:0"
	defer func() { codexTokenURL, codexCallbackAddr = oldToken, oldCallback }()
	flow, err := StartSignIn("codex")
	if err != nil {
		t.Fatal(err)
	}
	defer CancelSignIn(flow.ID)
	u, _ := url.Parse(flow.URL)
	challenge = u.Query().Get("code_challenge")
	if challenge == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("missing PKCE")
	}
	callback := u.Query().Get("redirect_uri") + "?code=fixture-code&state="
	_ = SubmitSignInCallback(flow.ID, callback+"incorrect-state")
	if exchanges != 0 {
		t.Fatal("exchanged with invalid state")
	}
	if err := SubmitSignInCallback(flow.ID, callback+url.QueryEscape(u.Query().Get("state"))); err != nil {
		t.Fatal(err)
	}
	state, _ := SignInStatus(flow.ID)
	if state.State != "done" {
		t.Fatalf("state %s", state.State)
	}
	if exchanges != 1 {
		t.Fatal("missing token exchange")
	}
	stateJSON, _ := json.Marshal(state)
	if strings.Contains(string(stateJSON), "fixture-access-token") {
		t.Fatal("token in login state")
	}
	ls := UXQALogins()
	if len(ls) != 1 || !ls[0].On {
		t.Fatal("saved account missing")
	}
	ps := Accounts()
	if len(ps) != 1 || ps[0].ID != "codex" {
		t.Fatal("saved account cannot serve without an external Codex login")
	}
	off := false
	if err := UXQAEditLogin("codex", ls[0].User, &off, false); err != nil {
		t.Fatal(err)
	}
	if len(Accounts()) != 0 {
		t.Fatal("disabled account still serves")
	}
	if err := UXQAEditLogin("codex", ls[0].User, nil, true); err != nil {
		t.Fatal(err)
	}
	if len(UXQALogins()) != 0 {
		t.Fatal("removed account retained")
	}
	if b, _ := os.ReadFile(foreign); string(b) != "foreign credential unchanged" {
		t.Fatal("foreign account changed")
	}
	if _, err := os.Stat(filepath.Join(appdir.CodexHome(), "auth.json")); !os.IsNotExist(err) {
		t.Fatal("wrote agent login")
	}
	canceled, err := StartSignIn("codex")
	if err != nil {
		t.Fatal(err)
	}
	CancelSignIn(canceled.ID)
	state, _ = SignInStatus(canceled.ID)
	if state.State != "canceled" {
		t.Fatal("cancel did not finish")
	}
	if info, err := os.Stat(loginsPath()); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("credentials are not private")
	}
}

func TestUXQACodexModelsBecomeSelectableAfterLogin(t *testing.T) {
	t.Setenv("WORKSHELF_MODEL_HOME", t.TempDir())
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	jwt := func(v any) string {
		b, _ := json.Marshal(v)
		return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".fixture"
	}
	id := jwt(map[string]any{"email": "models@example.test", "https://api.openai.com/auth": map[string]string{"chatgpt_plan_type": "pro", "chatgpt_account_id": "fixture-account"}})
	token := jwt(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	l, err := codexLogin(id, token, "fixture-refresh", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = addLogin(l); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("unexpected model request path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("saved UXQA account not used")
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"fixture-vision","display_name":"Fixture Vision","visibility":"list","supported_in_api":true,"input_modalities":["text","image"]}]}`))
	}))
	defer server.Close()
	previous := CodexBase
	CodexBase = server.URL
	defer func() { CodexBase = previous }()
	ps := Accounts()
	if len(ps) != 1 {
		t.Fatal("missing saved account provider")
	}
	listed, err := ps[0].Fetch(context.Background())
	if err != nil || len(listed) == 0 {
		t.Fatalf("upstream list failed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("model list requests = %d", requests)
	}
	contains := func(ms []catalog.Model) bool {
		for _, m := range ms {
			if m.ID == "fixture-vision" {
				return true
			}
		}
		return false
	}
	if !contains(ps[0].Exposed()) {
		t.Fatal("login fetched models but UXQA model picker remained empty")
	}
	if ms, _, ok := catalog.Live(accountModels("codex", l.User)); !ok || !contains(ms) {
		t.Fatal("account model catalog not persisted")
	}
	if ms, _, ok := catalog.Live("codex"); !ok || !contains(ms) {
		t.Fatal("provider model catalog not persisted")
	}
	ForgetAccounts()
	if _, _, ok := Resolve("codex/fixture-vision"); !ok {
		t.Fatal("refreshed model cannot be routed after account reload")
	}
	if _, err := os.Stat(filepath.Join(appdir.CodexHome(), "auth.json")); !os.IsNotExist(err) {
		t.Fatal("model refresh wrote external agent login")
	}
}
