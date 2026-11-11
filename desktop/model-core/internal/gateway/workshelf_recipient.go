package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/provider"
)

var errWorkShelfProof = errors.New("订阅发送证明已失效，请重新测试连接并预览")

const workShelfProbeText = "Reply with OK."

type workShelfProofEntry struct {
	public       provider.WorkShelfRecipient
	seal, tenant string // helper-private account generation and cloud scope
	ticket       uint64
}
type workShelfProofs struct {
	once    sync.Once
	mu      sync.Mutex
	key     [32]byte
	err     error
	serial  uint64
	entries map[string]workShelfProofEntry
	pending map[string]uint64
	uses    map[uint64]struct {
		model  string
		cancel context.CancelFunc
	}
	resolve  func(string) (provider.Provider, error)
	identity func(provider.Provider) (string, string, string, error)
}

func (s *workShelfProofs) init() error {
	s.once.Do(func() {
		_, s.err = rand.Read(s.key[:])
		s.entries = map[string]workShelfProofEntry{}
		s.pending = map[string]uint64{}
		s.uses = map[uint64]struct {
			model  string
			cancel context.CancelFunc
		}{}
		if s.resolve == nil {
			s.resolve = provider.WorkShelfSelected
		}
		if s.identity == nil {
			s.identity = func(p provider.Provider) (string, string, string, error) {
				id, label, e := provider.WorkShelfAccountIdentity(p)
				if e != nil {
					return "", "", "", e
				}
				seal, e := provider.WorkShelfAccountSeal(p, s.key[:])
				return id, label, seal, e
			}
		}
	})
	return s.err
}
func (s *workShelfProofs) selected(model string) (provider.Provider, string, string, string, error) {
	if e := s.init(); e != nil {
		return provider.Provider{}, "", "", "", e
	}
	agent, _, ok := provider.WorkShelfModel(model)
	if !ok {
		return provider.Provider{}, "", "", "", errWorkShelfProof
	}
	p, e := s.resolve(agent)
	if e != nil {
		return p, "", "", "", errWorkShelfProof
	}
	id, label, seal, e := s.identity(p)
	if e != nil || p.ID != agent || p.Account == nil || p.Account.Agent != agent || id == "" || seal == "" {
		return p, "", "", "", errWorkShelfProof
	}
	return p, id, label, seal, nil
}
func (s *workShelfProofs) begin(model string) (uint64, string, error) {
	if e := s.init(); e != nil {
		return 0, "", e
	}
	var cancel []context.CancelFunc
	s.mu.Lock()
	if _, ok := s.pending[model]; !ok {
		if _, exists := s.entries[model]; !exists && len(s.entries)+len(s.pending) >= 256 {
			s.mu.Unlock()
			return 0, "", errWorkShelfProof
		}
	}
	delete(s.entries, model)
	s.serial++
	ticket := s.serial
	s.pending[model] = ticket
	for _, u := range s.uses {
		if u.model == model {
			cancel = append(cancel, u.cancel)
		}
	}
	s.mu.Unlock()
	for _, c := range cancel {
		c()
	}
	_, _, _, seal, e := s.selected(model)
	if e != nil {
		s.abandon(model, ticket)
		return 0, "", e
	}
	return ticket, seal, nil
}
func (s *workShelfProofs) abandon(model string, ticket uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[model] == ticket {
		delete(s.pending, model)
	}
}
func (s *workShelfProofs) current(model string) (provider.WorkShelfRecipient, error) {
	_, id, _, seal, e := s.selected(model)
	if e != nil {
		return provider.WorkShelfRecipient{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.entries[model]
	if !ok || v.seal != seal || v.public.AccountID != id {
		return provider.WorkShelfRecipient{}, errWorkShelfProof
	}
	return v.public, nil
}
func (s *workShelfProofs) publish(ctx context.Context, model string, ticket uint64, seal string, entry workShelfProofEntry) error {
	_, id, _, current, e := s.selected(model)
	if e != nil || current != seal || entry.seal != seal || entry.public.AccountID != id || !entry.public.Valid() {
		return errWorkShelfProof
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.pending[model] != ticket {
		return errWorkShelfProof
	}
	delete(s.pending, model)
	s.entries[model] = entry
	return nil
}

// This is the linearization point for dispatch. Replacement revokes the old
// proof under the same mutex and cancels already-started uses after unlocking.
// No mutex is held over network I/O, response consumption, cancellation or exit.
func (s *workShelfProofs) claim(ctx context.Context, model string, expected *provider.WorkShelfRecipient, ticket uint64, seal, tenant string) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if len(s.uses) >= 256 {
		return nil, nil, errWorkShelfProof
	}
	if expected == nil {
		if s.pending[model] != ticket {
			return nil, nil, errWorkShelfProof
		}
	} else {
		v, ok := s.entries[model]
		if !ok || v.public != *expected || v.seal != seal || v.tenant != tenant {
			return nil, nil, errWorkShelfProof
		}
	}
	s.serial++
	id := s.serial
	child, cancel := context.WithCancel(ctx)
	s.uses[id] = struct {
		model  string
		cancel context.CancelFunc
	}{model, cancel}
	var once sync.Once
	done := func() { once.Do(func() { cancel(); s.mu.Lock(); delete(s.uses, id); s.mu.Unlock() }) }
	return child, done, nil
}

type workShelfControlKey struct{}
type workShelfControl struct {
	proofs   *workShelfProofs
	probe    bool
	ticket   uint64
	seal     string
	observed *workShelfProofEntry
	// A private seam for boundary tests, never set by an HTTP caller.
	permit func(context.Context, provider.Provider, *http.Request, map[string]any, string, *provider.WorkShelfRecipient) (context.Context, func(), error)
}

func (c *workShelfControl) check(r *provider.WorkShelfRecipient) error {
	if c == nil || c.proofs == nil || r == nil || !r.Valid() {
		return errWorkShelfProof
	}
	got, e := c.proofs.current(r.ModelID)
	if e != nil || got != *r {
		return errWorkShelfProof
	}
	return nil
}
func (c *workShelfControl) authorize(ctx context.Context, p provider.Provider, req *http.Request, body map[string]any, model string, expected *provider.WorkShelfRecipient) (context.Context, func(), error) {
	if c == nil {
		return nil, nil, errWorkShelfProof
	}
	if c.permit != nil {
		return c.permit(ctx, p, req, body, model, expected)
	}
	if c.proofs == nil {
		return nil, nil, errWorkShelfProof
	}
	_, id, label, seal, e := c.proofs.selected(model)
	if e != nil {
		return nil, nil, e
	}
	pid, _, pseal, e := c.proofs.identity(p)
	if e != nil || pid != id || pseal != seal {
		return nil, nil, errWorkShelfProof
	}
	wire, ok := body["model"].(string)
	if !ok {
		return nil, nil, errWorkShelfProof
	}
	tenant := ""
	if v, ok := body["project"]; ok {
		tenant, ok = v.(string)
		if !ok || tenant == "" {
			return nil, nil, errWorkShelfProof
		}
	}
	proofID := ""
	if expected != nil {
		proofID = expected.ProofID
	} else {
		var token [32]byte
		if _, e = rand.Read(token[:]); e != nil {
			return nil, nil, e
		}
		proofID = hex.EncodeToString(token[:])
	}
	actual := provider.WorkShelfRecipient{Schema: 1, ProofID: proofID, Agent: p.ID, AccountID: id, AccountLabel: label, ModelID: model, Endpoint: req.URL.String(), WireModel: wire}
	if !actual.Valid() || expected != nil && actual != *expected || c.probe && seal != c.seal {
		return nil, nil, errWorkShelfProof
	}
	child, done, e := c.proofs.claim(ctx, model, expected, c.ticket, seal, tenant)
	if e != nil {
		return nil, nil, e
	}
	c.observed = &workShelfProofEntry{public: actual, seal: seal, tenant: tenant, ticket: c.ticket}
	return child, done, nil
}

func decodeWorkShelfJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errWorkShelfProof
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				name, ok := k.(string)
				if e != nil || !ok || seen[name] {
					return errWorkShelfProof
				}
				seen[name] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		default:
			return errWorkShelfProof
		}
		_, e = d.Token()
		return e
	}
	if e := value(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errWorkShelfProof
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func workShelfModelInput(r *http.Request) (string, error) {
	if r.Method != "POST" {
		return "", errWorkShelfProof
	}
	data, e := io.ReadAll(io.LimitReader(r.Body, 4097))
	if e != nil || len(data) > 4096 {
		return "", errWorkShelfProof
	}
	var in struct {
		Model string `json:"model"`
	}
	if decodeWorkShelfJSON(data, &in) != nil {
		return "", errWorkShelfProof
	}
	a, _, ok := provider.WorkShelfModel(in.Model)
	if !ok || a == "claude" {
		return "", errWorkShelfProof
	}
	return in.Model, nil
}
func workShelfJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// A read-only lookup: no probe, credential refresh or model request occurs here.
func (s *Server) WorkShelfRecipient(w http.ResponseWriter, r *http.Request) {
	model, e := workShelfModelInput(r)
	if e != nil {
		writeError(w, provider.Chat, 400, "订阅模型无效")
		return
	}
	v, e := s.workShelf.current(model)
	if e != nil {
		writeError(w, provider.Chat, 409, errWorkShelfProof.Error())
		return
	}
	workShelfJSON(w, v)
}

type workShelfProbeWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *workShelfProbeWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *workShelfProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *workShelfProbeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(b) > 128<<10 {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(b)
}
func (w *workShelfProbeWriter) Flush() {}

// The caller supplies only a model ID. Fixed test text is constructed here,
// never taken from a project preview or caller-provided messages.
func (s *Server) WorkShelfProbe(w http.ResponseWriter, r *http.Request) {
	model, e := workShelfModelInput(r)
	if e != nil {
		writeError(w, provider.Chat, 400, "订阅模型无效")
		return
	}
	ticket, seal, e := s.workShelf.begin(model)
	if e != nil {
		writeError(w, provider.Chat, 409, errWorkShelfProof.Error())
		return
	}
	defer s.workShelf.abandon(model, ticket)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctrl := &workShelfControl{proofs: &s.workShelf, probe: true, ticket: ticket, seal: seal}
	data, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": workShelfProbeText}}, "max_completion_tokens": 64, "stream": false})
	probe := r.Clone(context.WithValue(ctx, workShelfControlKey{}, ctrl))
	probe.Body = io.NopCloser(bytes.NewReader(data))
	result := &workShelfProbeWriter{}
	s.runWorkShelfText(result, probe)
	if result.status != 200 || result.overflow || !workShelfProbeComplete(result.body.Bytes()) || ctrl.observed == nil {
		writeError(w, provider.Chat, 502, "订阅连接未通过受限测试，未保存发送证明")
		return
	}
	if e = s.workShelf.publish(ctx, model, ticket, seal, *ctrl.observed); e != nil {
		writeError(w, provider.Chat, 409, errWorkShelfProof.Error())
		return
	}
	workShelfJSON(w, map[string]any{"recipient": ctrl.observed.public, "result": json.RawMessage(result.body.Bytes())})
}

// Match the desktop text-generation acceptance contract before publishing a
// proof. A successful HTTP status or a partial/tool response is not a test pass.
func workShelfProbeComplete(data []byte) bool {
	var raw map[string]json.RawMessage
	if !utf8.Valid(data) || decodeWorkShelfJSON(data, &raw) != nil || raw == nil {
		return false
	}
	if v, ok := raw["error"]; ok && string(v) != "null" {
		return false
	}
	var r struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Role, Content string
				Refusal       *string         `json:"refusal"`
				Tools         []any           `json:"tool_calls"`
				Function      json.RawMessage `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt        *int64 `json:"prompt_tokens"`
			Completion    *int64 `json:"completion_tokens"`
			Total         *int64 `json:"total_tokens"`
			PromptDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &r) != nil || len(r.Choices) != 1 {
		return false
	}
	c := r.Choices[0]
	if c.Finish != "stop" || c.Message.Role != "assistant" || strings.TrimSpace(c.Message.Content) == "" || strings.ContainsRune(c.Message.Content, 0) || len(c.Message.Tools) != 0 || c.Message.Refusal != nil && *c.Message.Refusal != "" || len(c.Message.Function) > 0 && string(c.Message.Function) != "null" {
		return false
	}
	for _, n := range []*int64{r.Usage.Prompt, r.Usage.Completion, r.Usage.Total, r.Usage.PromptDetails.Cached, r.Usage.CompletionDetails.Reasoning} {
		if n != nil && (*n < 0 || *n > 1_000_000_000) {
			return false
		}
	}
	return true
}
