package projectcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestTwoActualModelRequestsReuseSameProjectPacket(t *testing.T) {
	p, _, _ := packet(t)
	payload, _ := p.Bytes()
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("request decode")
		}
		text := "OK"
		if len(request.Messages) == 2 {
			if request.Messages[0].Role != "system" || request.Messages[0].Content != Instructions || request.Messages[1].Content != string(payload) {
				t.Error("model-specific context divergence")
			}
			seen = append(seen, request.Model)
			advice, _ := json.Marshal(adviceFor(p))
			text = string(advice)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": text}}}})
	}))
	defer server.Close()
	client := modelhttp.NewClient()
	defer client.Close()
	store, err := models.NewStore(filepath.Join(t.TempDir(), "model-config"), nil, client)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	revision := uint64(0)
	for i, name := range []string{"model-a", "model-b"} {
		draft, err := store.PrepareDraft(context.Background(), revision, models.ConnectionInput{ProviderName: name, Protocol: models.Chat, BaseURL: server.URL, ModelID: name})
		if err != nil {
			t.Fatal(err)
		}
		verified, err := draft.Verify(context.Background())
		if err != nil || !verified.Verified {
			t.Fatal("test model", err)
		}
		receipt, err := store.SaveVerifiedDraft(context.Background(), draft, fmt.Sprintf("save-%d", i), true)
		draft.Close()
		if err != nil {
			t.Fatal(err)
		}
		revision = receipt.Revision
		lease, err := store.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		result, err := lease.Generate(models.GenerationRequest{System: Instructions, Input: string(payload), MaxOutputTokens: 500})
		lease.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.ValidateAdvice([]byte(result.Text)); err != nil {
			t.Fatal("ungrounded response", err)
		}
	}
	if len(seen) != 2 || seen[0] != "model-a" || seen[1] != "model-b" {
		t.Fatal("model switch did not actually call both", seen)
	}
}
