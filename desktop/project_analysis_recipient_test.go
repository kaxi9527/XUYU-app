package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/models"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
)

// Local HTTP boundary, using the real catalog, adapter, project controller and
// durable ledgers. It never uses a real account, credential or model service.
func subscriptionAnalysisFixture(t *testing.T, rotateAfterFirst bool) (*ShelfService, string, *atomic.Int32, func()) {
	t.Helper()
	s, project, _ := analysisServiceFixture(t, nil)
	s.connections.store.Close()
	proof := subscriptionRecipientFixture()
	var mu sync.Mutex
	var calls atomic.Int32
	rotate := func() { mu.Lock(); proof.ProofID = strings.Repeat("c", 64); mu.Unlock() }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, e := io.ReadAll(io.LimitReader(r.Body, 128<<10))
		if e != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		current := proof
		mu.Unlock()
		if r.URL.Path == "/control/probe-text" || r.URL.Path == "/control/generation-recipient" {
			if string(data) != `{"model":"copilot/fixture"}` {
				t.Error("probe/query sent project content")
				w.WriteHeader(400)
				return
			}
			if r.URL.Path == "/control/probe-text" {
				json.NewEncoder(w).Encode(map[string]any{"recipient": current, "result": json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)})
			} else {
				json.NewEncoder(w).Encode(current)
			}
			return
		}
		if r.URL.Path != "/control/generate-text" {
			w.WriteHeader(404)
			return
		}
		var input struct {
			Recipient models.SubscriptionRecipient
			Messages  []struct{ Content string }
		}
		if json.Unmarshal(data, &input) != nil || input.Recipient != current || len(input.Messages) < 1 {
			w.WriteHeader(409)
			return
		}
		if strings.Contains(string(data), "DO_NOT_SEND") {
			t.Error("file body leaked")
		}
		packet, e := projectcontext.Restore([]byte(input.Messages[len(input.Messages)-1].Content))
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		writeAnalysisFixture(w, packet)
		if rotateAfterFirst && n == 1 {
			rotate()
		}
	}))
	t.Cleanup(server.Close)
	adapter := &accountModelAdapter{request: func(ctx context.Context, method, path string, in, out any) error {
		data, e := json.Marshal(in)
		if e != nil {
			return e
		}
		r, e := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(data))
		if e != nil {
			return e
		}
		response, e := server.Client().Do(r)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("synthetic recipient changed")
		}
		return json.NewDecoder(response.Body).Decode(out)
	}}
	store, e := models.NewStore(filepath.Join(t.TempDir(), "subscription-models"), nil, adapter)
	if e != nil {
		t.Fatal(e)
	}
	s.connections.store = store
	p, e := store.Prepare(context.Background(), 0, models.ConnectionInput{ProviderName: "Copilot", Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/copilot", ModelID: proof.ModelID})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	saved, e := store.Save(context.Background(), p, "save-proof")
	if e != nil {
		t.Fatal(e)
	}
	v, e := store.VerifyModel(context.Background(), saved.Revision, "verify-proof", saved.ModelID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.SetMode(context.Background(), v.Receipt.Revision, "use-proof", "agent", saved.ModelID); e != nil {
		t.Fatal(e)
	}
	return s, project, &calls, rotate
}

func TestProjectAnalysisSubscriptionProofSingleHTTP(t *testing.T) {
	s, _, calls, rotate := subscriptionAnalysisFixture(t, false)
	p, e := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if e != nil || p.Recipient.Subscription == nil || calls.Load() != 0 {
		t.Fatal("preview", e)
	}
	if _, e = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "subscription-one", true, false); e != nil {
		t.Fatal(e)
	}
	r := waitAnalysis(t, s, "subscription-one", true)
	if r.Status != "succeeded" || r.Spec.Recipient.Digest() != p.Recipient.Digest() || calls.Load() != 1 {
		t.Fatal("receipt mismatch", r.Status, calls.Load())
	}
	if _, e = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "subscription-one", true, false); e != nil || calls.Load() != 1 {
		t.Fatal("idempotent confirmation resent", e)
	}
	next, e := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if e != nil {
		t.Fatal(e)
	}
	rotate()
	if _, e = s.ConfirmProjectAnalysis("root:品牌项目", next.Token, "subscription-stale", true, false); e == nil || calls.Load() != 1 {
		t.Fatal("stale confirmation sent")
	}
	h, e := s.ProjectAnalysisHistory("root:品牌项目", 0)
	if e != nil || h.Total != 1 {
		t.Fatal("failed confirmation reserved", e, h.Total)
	}
}
func TestProjectAnalysisSubscriptionBatchHTTP(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "retest-between-groups"}[rotate], func(t *testing.T) {
			s, project, calls, _ := subscriptionAnalysisFixture(t, rotate)
			addBatchFiles(t, s, project, 81)
			p := previewWholeBatch(t, s)
			if calls.Load() != 0 || p.Spec.Requests[0].Recipient.Subscription == nil {
				t.Fatal("batch preview")
			}
			if _, e := s.ConfirmProjectAnalysisBatch("root:品牌项目", p.Token, "subscription-batch", true, false, false); e != nil {
				t.Fatal(e)
			}
			want := "completed"
			if rotate {
				want = "stopped"
			}
			item := waitBatch(t, s, "subscription-batch", func(x AnalysisBatchItem) bool { return x.Status == want && x.NoteSaved })
			if rotate {
				if calls.Load() != 1 || item.Completed != 1 || item.AnalyzedFiles >= item.TotalFiles {
					t.Fatal("retest reused old recipient", calls.Load(), item)
				}
			} else if int(calls.Load()) != item.Total || item.Completed != item.Total {
				t.Fatal("batch incomplete", item)
			}
			before := calls.Load()
			if _, e := s.ConfirmProjectAnalysisBatch("root:品牌项目", p.Token, "subscription-batch", true, false, false); e != nil || calls.Load() != before {
				t.Fatal("batch replay sent", e)
			}
		})
	}
}
