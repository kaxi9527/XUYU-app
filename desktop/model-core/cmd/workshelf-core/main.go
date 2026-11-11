// UXQA private model runtime. Source-integrated protocols and authorizations;
// deliberately no upstream application, web manager, CLI or plugin host.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

type core struct {
	inference  http.Handler
	generation http.Handler
	probe      http.Handler
	recipient  http.Handler
	key        string
	mu         sync.Mutex
	flows      map[string]bool
}
type providerInput struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Protocol string   `json:"protocol"`
	BaseURL  string   `json:"baseUrl"`
	Key      string   `json:"apiKey"`
	ClearKey bool     `json:"clearKey"`
	Models   []string `json:"models"`
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		fail(w, 400, "请求格式不正确")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求包含多余内容")
		return false
	}
	return true
}

func projection(p provider.Provider) map[string]any {
	protocol, base := "openai", p.Chat
	if p.BaseAPI == "responses" {
		protocol, base = "responses", p.Responses
	}
	if p.BaseAPI == "anthropic" {
		protocol, base = "anthropic", p.Anthropic
	}
	if p.BaseAPI == "gemini" {
		protocol, base = "gemini", p.Gemini
	}
	return map[string]any{"id": p.ID, "name": p.Name, "protocol": protocol, "baseUrl": base, "hasKey": p.Key != "", "models": p.Models}
}

func stored(id string) (*provider.Provider, error) {
	ps, err := provider.Stored()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id && !provider.UXQASupported(id) {
			return &p, nil
		}
	}
	return nil, errors.New("服务商不存在")
}

func saveProvider(in providerInput) (string, error) {
	in.Name, in.BaseURL = strings.TrimSpace(in.Name), strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	u, err := url.Parse(in.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("请填写不带凭据或查询参数的 HTTP(S) 模型地址")
	}
	if in.Name == "" {
		return "", errors.New("请填写服务商名称")
	}
	// Compare the same canonical endpoint that the migrated provider store keeps.
	if in.Protocol == "anthropic" {
		for _, suffix := range []string{"/v1/messages", "/v1"} {
			if base, ok := strings.CutSuffix(in.BaseURL, suffix); ok {
				in.BaseURL = base
				break
			}
		}
	}
	if in.Protocol == "gemini" {
		in.BaseURL = provider.GeminiBase(in.BaseURL)
	}
	p := provider.Provider{ID: in.ID, Name: in.Name, Key: strings.TrimSpace(in.Key), Models: in.Models}
	switch in.Protocol {
	case "openai":
		p.Chat, p.BaseAPI = in.BaseURL, "chat"
	case "responses":
		p.Responses, p.BaseAPI = in.BaseURL, "responses"
	case "anthropic":
		p.Anthropic, p.BaseAPI = in.BaseURL, "anthropic"
	case "gemini":
		p.Gemini, p.BaseAPI = in.BaseURL, "gemini"
	default:
		return "", errors.New("不支持的模型协议")
	}
	if in.ID != "" {
		old, err := stored(in.ID)
		if err != nil {
			return "", err
		}
		if p.Key == "" && !in.ClearKey && projection(*old)["baseUrl"] == in.BaseURL && projection(*old)["protocol"] == in.Protocol {
			p.Key = old.Key
		}
		return p.ID, provider.Save(p)
	}
	// Explicit namespace prevents a custom provider shadowing a subscription.
	p.ID = fmt.Sprintf("api-%d", time.Now().UnixNano())
	return provider.Add(p)
}

func (c *core) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only the owning UXQA process knows this per-launch key. A browser cannot
	// reach the core directly; it calls the already-authenticated console API.
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.key)) != 1 {
		fail(w, 401, "需要 UXQA 会话认证")
		return
	}
	path := r.URL.Path
	if r.Method == "POST" && (path == "/control/generate-text" || path == "/control/probe-text" || path == "/control/generation-recipient") {
		handler := c.generation
		if path == "/control/probe-text" {
			handler = c.probe
		} else if path == "/control/generation-recipient" {
			handler = c.recipient
		}
		if handler == nil {
			fail(w, 503, "模型生成入口尚未就绪")
			return
		}
		handler.ServeHTTP(w, r)
		return
	}
	if path == "/control/accounts" || path == "/control/state" || path == "/control/account" || strings.HasPrefix(path, "/control/login") {
		if err := provider.WorkShelfAccountStorageError(); err != nil {
			fail(w, 409, "账号存储无法安全读取，原文件已保留")
			return
		}
	}
	if r.Method == "GET" && path == "/control/accounts" {
		reply(w, 200, map[string]any{"accounts": provider.UXQALogins(), "capabilities": provider.UXQACapabilities()})
		return
	}
	if r.Method == "GET" && path == "/control/models" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var mu sync.Mutex
		var wg sync.WaitGroup
		limit := make(chan struct{}, 2)
		ids, warnings := []string{}, []string{}
		for _, p := range provider.All() {
			if agent := r.URL.Query().Get("agent"); agent != "" && p.ID != agent {
				continue
			}
			wg.Go(func() {
				select {
				case limit <- struct{}{}:
				case <-ctx.Done():
					mu.Lock()
					warnings = append(warnings, p.Name+"：读取模型列表超时")
					mu.Unlock()
					return
				}
				defer func() { <-limit }()
				_, err := p.Fetch(ctx)
				models := p.Exposed()
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					warnings = append(warnings, p.Name+"：未能刷新模型列表，请检查地址、凭据或填写自定义模型")
				}
				for _, model := range models {
					ids = append(ids, p.ID+"/"+model.ID)
				}
			})
		}
		wg.Wait()
		slices.Sort(ids)
		ids = slices.Compact(ids)
		slices.Sort(warnings)
		reply(w, 200, map[string]any{"models": ids, "warnings": warnings})
		return
	}
	if (r.Method == "GET" && path == "/v1/models") || (r.Method == "POST" && (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages")) {
		r.Header.Set("Authorization", "Bearer magpie") // internal migrated handler only
		c.inference.ServeHTTP(w, r)
		return
	}
	if r.Method == "GET" && path == "/control/state" {
		ps, err := provider.Stored()
		if err != nil {
			fail(w, 500, "无法读取模型配置")
			return
		}
		safe := []map[string]any{}
		for _, p := range ps {
			safe = append(safe, projection(p))
		}
		reply(w, 200, map[string]any{"providers": safe, "accounts": provider.UXQALogins(), "capabilities": provider.UXQACapabilities()})
		return
	}
	if r.Method == "POST" && path == "/control/providers" {
		var in providerInput
		if !decode(w, r, &in) {
			return
		}
		id, err := saveProvider(in)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, 200, map[string]string{"id": id})
		return
	}
	if r.Method == "POST" && path == "/control/provider-delete" {
		var in struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if _, err := stored(in.ID); err != nil {
			fail(w, 404, "服务商不存在")
			return
		}
		if err := provider.Delete(in.ID); err != nil {
			fail(w, 500, "移除服务商失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method == "POST" && path == "/control/account" {
		var in struct {
			Agent  string `json:"agent"`
			User   string `json:"user"`
			On     *bool  `json:"on"`
			Remove bool   `json:"remove"`
			Select bool   `json:"select"`
		}
		if !decode(w, r, &in) {
			return
		}
		var err error
		if in.Select && !in.Remove {
			err = provider.WorkShelfSelectLogin(in.Agent, in.User)
		} else {
			err = provider.UXQAEditLogin(in.Agent, in.User, in.On, in.Remove)
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method == "POST" && path == "/control/login" {
		var in struct {
			Agent string `json:"agent"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !provider.UXQASupported(in.Agent) {
			fail(w, 400, "不支持的订阅类型")
			return
		}
		st, err := provider.StartSignIn(in.Agent)
		if err != nil {
			fail(w, 400, "无法开始登录："+err.Error())
			return
		}
		c.mu.Lock()
		c.flows[st.ID] = true
		c.mu.Unlock()
		reply(w, 200, st)
		return
	}
	if strings.HasPrefix(path, "/control/login/") {
		id := strings.TrimPrefix(path, "/control/login/")
		c.mu.Lock()
		known := c.flows[id]
		c.mu.Unlock()
		if !known {
			fail(w, 404, "登录流程不存在或已过期")
			return
		}
		if r.Method == "POST" {
			var in struct {
				Cancel   bool   `json:"cancel"`
				Callback string `json:"callback"`
			}
			if !decode(w, r, &in) {
				return
			}
			if in.Cancel {
				provider.CancelSignIn(id)
			} else if err := provider.SubmitSignInCallback(id, in.Callback); err != nil {
				fail(w, 400, "授权回调无效或已过期，请检查后重试")
				return
			}
		} else if r.Method != "GET" {
			fail(w, 405, "不支持的请求方法")
			return
		}
		st, ok := provider.SignInStatus(id)
		if !ok {
			fail(w, 404, "登录流程已过期")
			return
		}
		if st.State == "done" {
			provider.ForgetAccounts()
		}
		reply(w, 200, st)
		return
	}
	fail(w, 404, "接口不存在")
}

func main() {
	// Official Claude CLI opens authorization via this process as BROWSER.
	if provider.TookOpenedURL(os.Args[1:]) {
		return
	}
	root, key := appdir.UXQARoot(), os.Getenv("WORKSHELF_MODEL_TOKEN")
	if root == "" || len(key) < 32 {
		fmt.Fprintln(os.Stderr, "UXQA model runtime requires its owning application")
		os.Exit(1)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		os.Exit(1)
	}
	// Settled defaults in our exclusive store: no history, archives or updates.
	config := []byte(`{"noAutoUpdate":true,"noAccountDiscovery":false,"sessionReads":{"asked":true,"all":false,"agents":{}},"gatewayConversations":false,"requestArchive":false,"requestArchiveLocal":false}`)
	if err := appdir.WorkShelfWriteFile(filepath.Join(root, "settings.json"), config); err != nil {
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	gatewayServer := gateway.New()
	c := &core{key: key, inference: gatewayServer.Handler(), generation: http.HandlerFunc(gatewayServer.WorkShelfText), probe: http.HandlerFunc(gatewayServer.WorkShelfProbe), recipient: http.HandlerFunc(gatewayServer.WorkShelfRecipient), flows: map[string]bool{}}
	server := &http.Server{Handler: c, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop() }()
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		for id := range c.flows {
			provider.CancelSignIn(id)
		}
		c.mu.Unlock()
		deadline, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = server.Shutdown(deadline)
	}()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"url": "http://" + listener.Addr().String()})
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(1)
	}
}
