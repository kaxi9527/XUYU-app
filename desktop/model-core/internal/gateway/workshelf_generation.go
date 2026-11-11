package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/provider"
)

// WorkShelfText bypasses the general router: work data cannot use provider
// aliases, model groups, stand-ins, fallback providers or account rotation.
// Only the authenticated private core handler exposes this method.
func (s *Server) WorkShelfText(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(context.WithValue(r.Context(), workShelfControlKey{}, &workShelfControl{proofs: &s.workShelf}))
	s.runWorkShelfText(w, r)
}
func (s *Server) runWorkShelfText(w http.ResponseWriter, r *http.Request) {
	if err := s.workShelf.init(); err != nil {
		writeError(w, provider.Chat, 503, "订阅证明存储暂不可用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	workShelfText(w, r, s.workShelf.resolve, func(p provider.Provider, model string, body []byte) {
		call := Call{Model: p.ID + "/" + model}
		s.attempt(w, r, provider.Chat, p, model, body, &call)
	})
}
func workShelfText(w http.ResponseWriter, r *http.Request, resolve func(string) (provider.Provider, error), send func(provider.Provider, string, []byte)) {
	reject := func(code int) {
		writeError(w, provider.Chat, code, "WorkShelf subscription request cannot be sent to the selected account")
	}
	if r.Method != "POST" {
		reject(405)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (128<<10)+1))
	if err != nil || len(data) > 128<<10 {
		reject(400)
		return
	}
	var in struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Max       int                          `json:"max_completion_tokens"`
		Stream    bool                         `json:"stream"`
		Recipient *provider.WorkShelfRecipient `json:"recipient,omitempty"`
	}
	if decodeWorkShelfJSON(data, &in) != nil {
		reject(400)
		return
	}
	agent, model, ok := strings.Cut(in.Model, "/")
	if !ok || !provider.UXQASupported(agent) || model == "" || len(model) > 256 || strings.ContainsAny(model, "/\\:@") || strings.ContainsFunc(model, unicode.IsSpace) || strings.ContainsFunc(model, unicode.IsControl) || strings.EqualFold(model, "auto") || in.Stream || in.Max < 1 || in.Max > 8192 || len(in.Messages) < 1 || len(in.Messages) > 2 {
		reject(400)
		return
	}
	bytes := 0
	for i, m := range in.Messages {
		bytes += len(m.Content)
		if !utf8.ValidString(m.Content) || strings.ContainsRune(m.Content, 0) || strings.TrimSpace(m.Content) == "" || (i == len(in.Messages)-1 && m.Role != "user") || (i < len(in.Messages)-1 && m.Role != "system") {
			reject(400)
			return
		}
	}
	if bytes > 64<<10 {
		reject(400)
		return
	}
	p, err := resolve(agent)
	if err != nil || p.ID != agent || p.Account == nil || p.Account.Agent != agent || p.Account.User == "" || len(p.Fallback) != 0 {
		reject(409)
		return
	}
	if agent == "claude" {
		reject(422)
		return
	}
	control, _ := r.Context().Value(workShelfControlKey{}).(*workShelfControl)
	if control == nil {
		reject(409)
		return
	}
	if control.probe {
		if in.Recipient != nil || in.Max != 64 || len(in.Messages) != 1 || in.Messages[0].Role != "user" || in.Messages[0].Content != workShelfProbeText {
			reject(400)
			return
		}
	} else if in.Recipient == nil || in.Recipient.ModelID != in.Model || control.check(in.Recipient) != nil {
		reject(409)
		return
	}
	expected := in.Recipient
	in.Recipient = nil
	// Re-encode only permitted fields. Headers cannot supply client routing,
	// pinned accounts, resume IDs, plugin tools or an alternative upstream.
	canonical, err := json.Marshal(in)
	if err != nil {
		reject(400)
		return
	}
	r.Header = make(http.Header)
	*r = *r.WithContext(context.WithValue(r.Context(), workShelfTextKey{}, &workShelfTextLimit{max: in.Max, agent: agent, user: p.Account.User, model: in.Model, expected: expected, control: control}))
	send(p, model, canonical)
}

type workShelfTextKey struct{}
type workShelfTextLimit struct {
	max         int
	agent, user string
	model       string
	expected    *provider.WorkShelfRecipient
	control     *workShelfControl
	sent        atomic.Bool
}

func workShelfLimit(ctx context.Context) *workShelfTextLimit {
	v, _ := ctx.Value(workShelfTextKey{}).(*workShelfTextLimit)
	return v
}

// Inspect the final signed body, after every protocol transformation. A lost
// or raised limit fails before project content leaves the process. This path
// performs one non-redirecting POST and bypasses all account custom transports.
func (s *Server) workShelfForward(p provider.Provider, req *http.Request, limit *workShelfTextLimit) (*http.Response, error) {
	// The general gateway transport has replay and websocket recovery wrappers.
	// Use a plain one-shot transport; no reused connection can replay this POST.
	transport := vendorTransport(30 * time.Second)
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	return workShelfSend(p, req, limit, &http.Client{Transport: transport})
}

func workShelfSend(p provider.Provider, req *http.Request, limit *workShelfTextLimit, upstream *http.Client) (*http.Response, error) {
	errBlocked := errors.New("订阅尚不支持此受限文本请求")
	if p.ID != limit.agent || p.Account == nil || p.Account.Agent != limit.agent || p.Account.User != limit.user || !provider.WorkShelfDestination(limit.agent, req.URL) || req.Method != "POST" || req.GetBody == nil || !limit.sent.CompareAndSwap(false, true) {
		return nil, errBlocked
	}
	b, err := req.GetBody()
	if err != nil {
		return nil, errBlocked
	}
	data, err := io.ReadAll(io.LimitReader(b, (1<<20)+1))
	b.Close()
	if err != nil || len(data) > 1<<20 {
		return nil, errBlocked
	}
	var body map[string]any
	if decodeWorkShelfJSON(data, &body) != nil {
		return nil, errBlocked
	}
	if v, ok := body["tools"]; ok {
		if list, ok := v.([]any); !ok || len(list) != 0 {
			return nil, errBlocked
		}
	}
	bounded := false
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if v, ok := body[key]; ok {
			n, valid := v.(float64)
			if !valid || n < 1 || n > float64(limit.max) || n != float64(int(n)) {
				return nil, errBlocked
			}
			bounded = true
		}
	}
	if limit.agent == "gemini" || limit.agent == "antigravity" {
		bounded = false
	}
	if nested, ok := body["request"].(map[string]any); ok {
		if v, ok := nested["tools"]; ok {
			if list, ok := v.([]any); !ok || len(list) != 0 {
				return nil, errBlocked
			}
		}
		if gen, ok := nested["generationConfig"].(map[string]any); ok {
			n, ok := gen["maxOutputTokens"].(float64)
			if !ok || n < 1 || n > float64(limit.max) || n != float64(int(n)) {
				return nil, errBlocked
			}
			bounded = true
		}
	}
	if !bounded && limit.agent != "codex" {
		return nil, errBlocked
	}
	child, done, err := limit.control.authorize(req.Context(), p, req, body, limit.model, limit.expected)
	if err != nil {
		return nil, errBlocked
	}
	req = req.WithContext(child)
	client := *upstream
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req.Header.Del("Idempotency-Key")
	req.Header.Del("X-Idempotency-Key")
	// net/http HTTP/2 can replay even a non-idempotent POST on REFUSED_STREAM
	// when GetBody is available. Validation is finished; do not offer a replay.
	req.GetBody = nil
	if err = req.Context().Err(); err != nil {
		done()
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		done()
		return nil, err
	}
	res.Body = &workShelfAuthorizedBody{ReadCloser: res.Body, done: done}
	return res, nil
}

type workShelfAuthorizedBody struct {
	io.ReadCloser
	done func()
}

func (b *workShelfAuthorizedBody) Close() error { err := b.ReadCloser.Close(); b.done(); return err }
