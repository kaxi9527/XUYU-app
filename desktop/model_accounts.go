package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

// A dedicated, source-integrated runtime owns subscription credentials. Its
// private loopback address and bearer token never cross the frontend bridge.
type modelAccountRuntime struct {
	mu                sync.Mutex
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	done              chan struct{}
	url, token        string
	closed            bool
	binary, directory string // Only tests may override these fields.
}
type ModelAccount struct {
	Agent  string `json:"agent"`
	User   string `json:"user"`
	Plan   string `json:"plan"`
	On     bool   `json:"on"`
	Lapsed string `json:"lapsed"`
}
type AccountCapability struct {
	ID          string `json:"id"`
	Available   bool   `json:"available"`
	Requirement string `json:"requirement,omitempty"`
}
type ModelAccountState struct {
	Accounts     []ModelAccount      `json:"accounts"`
	Capabilities []AccountCapability `json:"capabilities"`
}
type ModelLoginState struct {
	ID            string `json:"id"`
	Agent         string `json:"agent"`
	URL           string `json:"url"`
	Code          string `json:"code,omitempty"`
	State         string `json:"state"`
	PasteCallback bool   `json:"pasteCallback,omitempty"`
	Opened        bool   `json:"opened,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (b *modelAccountRuntime) start() error {
	if b.closed {
		return errors.New("模型接入中心已关闭")
	}
	if b.cmd != nil {
		select {
		case <-b.done:
			b.cmd = nil
			b.url = ""
			b.token = ""
		default:
			return nil
		}
	}
	binary := b.binary
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		helper := "workshelf-model-core"
		if runtime.GOOS == "windows" {
			helper += ".exe"
		}
		for _, p := range []string{filepath.Join(filepath.Dir(exe), helper), exe + "-model-core", filepath.Join(filepath.Dir(exe), "..", "Resources", helper)} {
			if fi, e := os.Lstat(p); e == nil && fi.Mode().IsRegular() {
				binary = p
				break
			}
		}
	}
	if binary == "" {
		return errors.New("此构建缺少订阅授权模块，请使用包含模型接入中心的完整构建")
	}
	directory := b.directory
	if directory == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		directory = filepath.Join(filepath.Dir(config.DefaultPath(h)), "model-accounts")
	}
	if fi, e := os.Lstat(directory); e == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir()) {
		return errors.New("账号存储位置无效")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("无法建立账号存储目录")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return errors.New("无法保护账号存储目录")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	token := hex.EncodeToString(secret)
	cmd := exec.Command(binary)
	// Do not inherit another application's model-runtime credentials.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "WORKSHELF_MODEL_") && !strings.HasPrefix(entry, "UXQA_MODEL_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "WORKSHELF_MODEL_HOME="+directory, "WORKSHELF_MODEL_TOKEN="+token)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return errors.New("订阅授权模块无法启动")
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	ready := make(chan string, 1)
	go func() {
		var v struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(io.LimitReader(stdout, 4096)).Decode(&v)
		ready <- v.URL
	}()
	var endpoint string
	select {
	case endpoint = <-ready:
	case <-time.After(8 * time.Second):
	case <-done:
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Path != "" {
		stdin.Close()
		_ = cmd.Process.Kill()
		<-done
		return errors.New("订阅授权模块启动失败")
	}
	b.cmd, b.stdin, b.done, b.url, b.token = cmd, stdin, done, endpoint, token
	return nil
}

func (b *modelAccountRuntime) request(method, path string, input any, out any) error {
	return b.requestContext(context.Background(), method, path, input, out)
}

func (b *modelAccountRuntime) requestContext(parent context.Context, method, path string, input any, out any) error {
	b.mu.Lock()
	err := b.start()
	endpoint, token := b.url, b.token
	b.mu.Unlock()
	if err != nil {
		return err
	}
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("订阅授权模块暂时无法连接，请重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not forward upstream response text or callback/token details.
		return errors.New("账号操作未完成，请检查账号状态后重试")
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return errors.New("账号返回内容无法读取")
	}
	return nil
}

func (b *modelAccountRuntime) close() {
	b.stop(true)
}

// Join the old process before allowing another one to start. OAuth callbacks
// can outlive a cancelled status, so terminal login flows quiesce the helper.
func (b *modelAccountRuntime) stop(permanent bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = b.closed || permanent
	cmd, stdin, done := b.cmd, b.stdin, b.done
	if cmd == nil {
		return
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd.Process != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	b.cmd, b.stdin, b.done, b.url, b.token = nil, nil, nil, "", ""
}

func (s *ShelfService) ModelAccounts() (ModelAccountState, error) {
	state := ModelAccountState{Accounts: []ModelAccount{}, Capabilities: []AccountCapability{}}
	if err := s.claimAccountOwnership(); err != nil {
		return state, err
	}
	err := s.connections.accounts.request("GET", "/control/accounts", nil, &state)
	return state, err
}

func (s *ShelfService) claimAccountOwnership() error {
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	b.mu.Unlock()
	if err != nil {
		return modelPublicError(err)
	}
	return modelPublicError(store.ClaimSubscriptionOwnership())
}
func validAccountAgent(agent string) bool {
	return agent == "codex" || agent == "claude" || agent == "copilot" || agent == "gemini" || agent == "antigravity"
}
func (s *ShelfService) StartModelAccountLogin(agent string) (ModelLoginState, error) {
	var state ModelLoginState
	if !validAccountAgent(agent) {
		return state, errors.New("暂不支持这个订阅类型")
	}
	b := &s.connections
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if b.loginRelease != nil {
		return state, errors.New("请先完成或取消当前订阅授权")
	}
	release, err := s.beginAccountChange(agent)
	if err != nil {
		return state, err
	}
	b.loginRelease = release
	err = b.accounts.request("POST", "/control/login", map[string]string{"agent": agent}, &state)
	if err != nil || !validModelFlow(state.ID) {
		b.accounts.stop(false)
		if cleanup := release(); cleanup == nil {
			b.loginRelease = nil
		} else {
			return state, modelPublicError(cleanup)
		}
		if err == nil {
			err = errors.New("授权返回标识无效，请重试")
		}
		return ModelLoginState{}, err
	}
	b.loginID = state.ID
	if err = s.finishAccountChange(state); err != nil {
		return ModelLoginState{}, err
	}
	return safeModelLogin(state), err
}
func validModelFlow(id string) bool {
	return len(id) > 0 && len(id) <= 100 && !strings.ContainsAny(id, "/?#\\\x00")
}
func safeModelLogin(state ModelLoginState) ModelLoginState {
	if state.Error != "" {
		state.Error = "授权未完成，可重新登录或取消。"
	}
	u, err := url.Parse(state.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		state.URL = ""
	}
	return state
}
func (s *ShelfService) ModelAccountLoginStatus(id string) (ModelLoginState, error) {
	var state ModelLoginState
	if !validModelFlow(id) {
		return state, errors.New("授权标识无效")
	}
	b := &s.connections
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if b.loginRelease == nil && b.loginResult.ID == id {
		return safeModelLogin(b.loginResult), nil
	}
	if b.loginID != id {
		return state, errors.New("授权流程已结束，请重新开始")
	}
	err := b.accounts.request("GET", "/control/login/"+url.PathEscape(id), nil, &state)
	if err == nil {
		err = s.finishAccountChange(state)
	}
	return safeModelLogin(state), err
}
func (s *ShelfService) FinishModelAccountLogin(id, callback string, cancel bool) (ModelLoginState, error) {
	var state ModelLoginState
	if !validModelFlow(id) || len(callback) > 8192 {
		return state, errors.New("授权信息无效")
	}
	b := &s.connections
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if b.loginRelease == nil && b.loginResult.ID == id {
		return safeModelLogin(b.loginResult), nil
	}
	if b.loginID != id {
		return state, errors.New("授权流程已结束，请重新开始")
	}
	err := b.accounts.request("POST", "/control/login/"+url.PathEscape(id), map[string]any{"cancel": cancel, "callback": callback}, &state)
	if err != nil && cancel {
		state = ModelLoginState{ID: id, State: "canceled"}
		err = nil
	}
	if err == nil {
		err = s.finishAccountChange(state)
	}
	return safeModelLogin(state), err
}
func (s *ShelfService) SetModelAccount(agent, user string, on, remove bool) error {
	if !validAccountAgent(agent) || user == "" || len(user) > 512 {
		return errors.New("账号信息无效")
	}
	b := &s.connections
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if b.loginRelease != nil {
		return errors.New("请先完成或取消当前订阅授权")
	}
	release, err := s.beginAccountChange(agent)
	if err != nil {
		return err
	}
	err = b.accounts.request("POST", "/control/account", map[string]any{"agent": agent, "user": user, "on": on, "remove": remove, "select": on && !remove}, nil)
	if err != nil {
		b.accounts.stop(false)
	}
	if cleanup := release(); cleanup != nil {
		return modelPublicError(cleanup)
	}
	return err
}

// accountMu is held, and the helper has reported a terminal state. Stop and
// join it before releasing the gate, including cancellation during a callback.
func (s *ShelfService) finishAccountChange(state ModelLoginState) error {
	if state.State != "done" && state.State != "canceled" && state.State != "failed" {
		return nil
	}
	b := &s.connections
	b.accounts.stop(false)
	if b.loginRelease != nil {
		if err := b.loginRelease(); err != nil {
			return modelPublicError(err)
		}
	}
	b.loginRelease = nil
	b.loginResult = state
	return nil
}

func (s *ShelfService) beginAccountChange(agent string) (func() error, error) {
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	b.mu.Unlock()
	if err != nil {
		return nil, modelPublicError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := store.BeginSubscriptionChange(ctx, agent)
	return release, modelPublicError(err)
}

// Only opens the URL belonging to a live authorization initiated in this app.
func (s *ShelfService) OpenModelAccountLoginPage(id string) error {
	state, err := s.ModelAccountLoginStatus(id)
	if err != nil {
		return err
	}
	if state.State != "waiting" || state.URL == "" {
		return errors.New("授权页面已过期，请重新开始")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", state.URL)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", state.URL)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", state.URL)
	}
	if err := cmd.Run(); err != nil {
		return errors.New("无法打开系统浏览器")
	}
	return nil
}
