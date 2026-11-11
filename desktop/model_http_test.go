package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestModelDesktopFourProtocolWorkflow(t *testing.T) {
	replies := map[models.Protocol]string{
		models.Chat:      `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`,
		models.Responses: `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}]}`,
		models.Anthropic: `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`,
		models.Gemini:    `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"OK"}]}}]}`,
	}
	for protocol, reply := range replies {
		t.Run(string(protocol), func(t *testing.T) {
			var requests atomic.Int32
			var limited atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == "GET" {
					if protocol == models.Gemini {
						_, _ = io.WriteString(w, `{"models":[{"name":"models/test-model","displayName":"Test"}]}`)
					} else {
						_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
					}
					return
				}
				if limited.Load() {
					w.Header().Set("Retry-After", "12")
					w.WriteHeader(429)
					_, _ = io.WriteString(w, `{"error":{"message":"do-not-show-this-private-message"}}`)
					return
				}
				_, _ = io.WriteString(w, reply)
			}))
			t.Cleanup(server.Close)
			client := modelhttp.NewClient()
			t.Cleanup(client.Close)
			svc, _, _ := modelTestService(t, client)
			in := modelTestInput()
			in.Protocol = protocol
			in.BaseURL = server.URL + "/v1"
			in.ModelID = "test-model"
			preview, err := svc.PreviewModelConnection(in)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 {
				t.Fatal("preview made a model request")
			}
			saved := svc.SaveModelConnection(preview.Token, "save")
			if saved.Error != "" || saved.Receipt == nil {
				t.Fatalf("save %+v", saved)
			}
			if requests.Load() != 0 {
				t.Fatal("save made a model request")
			}
			listed := svc.ListConnectionModels(saved.Receipt.ProviderID, saved.Receipt.Revision, "", "list")
			if listed.Error != "" || listed.Models == nil || len(listed.Models.Models) != 1 || listed.Revision != saved.Receipt.Revision {
				t.Fatalf("list %+v", listed)
			}
			state, err := svc.ModelConnections()
			if err != nil || state.State.Models[0].Verification != nil || state.State.Settings.Mode != "tool" {
				t.Fatal("list was mistaken for verification")
			}
			verified := svc.VerifyModelConnection(saved.Receipt.ModelID, saved.Receipt.Revision, "verify")
			if verified.Error != "" || verified.Receipt == nil || verified.Receipt.Verified == nil || !*verified.Receipt.Verified {
				t.Fatalf("verify %+v", verified)
			}
			if svc.CancelModelVerification("verify") {
				t.Fatal("completed verification falsely cancelled")
			}
			stale := svc.ListConnectionModels(saved.Receipt.ProviderID, saved.Receipt.Revision, "", "list")
			if stale.Models != nil || stale.Error != models.ErrConflict.Error() {
				t.Fatalf("stale cached discovery %+v", stale)
			}
			started := svc.SetWorkShelfMode("agent", saved.Receipt.ModelID, verified.Receipt.Revision, "start")
			if started.Error != "" || started.Receipt == nil {
				t.Fatalf("start %+v", started)
			}
			lease, err := svc.connections.store.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			limited.Store(true)
			failed := svc.VerifyModelConnection(saved.Receipt.ModelID, started.Receipt.Revision, "limited")
			if failed.Error != "" || failed.Receipt == nil || failed.Receipt.Problem == nil || failed.Receipt.Problem.Code != "rate_limited" || failed.Receipt.Problem.RetryAfterSeconds != 12 {
				t.Fatalf("HTTP failure %+v", failed)
			}
			if strings.Contains(fmt.Sprint(failed), "do-not-show") {
				t.Fatal("private provider error leaked")
			}
			select {
			case <-lease.Context.Done():
			default:
				t.Fatal("failed active connection did not stop lease")
			}
			state, err = svc.ModelConnections()
			if err != nil || state.State.Models[0].Verification != nil || state.State.Settings.Mode != "tool" {
				t.Fatal("failed connection remains active")
			}
			recovered, err := svc.ModelOperation("limited")
			if err != nil || !recovered.Found || recovered.Receipt.Problem == nil || recovered.Receipt.Problem.Code != "rate_limited" {
				t.Fatalf("diagnostic lost on recovery %+v %v", recovered, err)
			}
			if requests.Load() != 3 {
				t.Fatalf("unexpected retries or requests: %d", requests.Load())
			}
		})
	}
}

func TestModelDesktopDiscoveryCancellation(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(server.Close)
	client := modelhttp.NewClient()
	t.Cleanup(client.Close)
	svc, _, _ := modelTestService(t, client)
	in := modelTestInput()
	in.BaseURL = server.URL
	preview, err := svc.PreviewModelConnection(in)
	if err != nil {
		t.Fatal(err)
	}
	saved := svc.SaveModelConnection(preview.Token, "save")
	if saved.Receipt == nil {
		t.Fatal(saved.Error)
	}
	done := make(chan ModelActionResult, 1)
	go func() {
		done <- svc.ListConnectionModels(saved.Receipt.ProviderID, saved.Receipt.Revision, "", "loading")
	}()
	<-entered
	if !svc.CancelModelDiscovery("loading") {
		t.Fatal("cannot cancel discovery")
	}
	result := <-done
	if result.Models != nil || result.Error != "操作已取消" {
		t.Fatalf("cancel result %+v", result)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("HTTP list continued after cancellation")
	}
	if svc.CancelModelDiscovery("loading") {
		t.Fatal("completed discovery falsely cancelled")
	}
}
