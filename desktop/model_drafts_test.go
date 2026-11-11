package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

type bridgeDraftAdapter struct {
	modelTestVerifier
	list func(context.Context, models.Target, *credentials.Secret, string) (models.ModelPage, error)
}

func (a bridgeDraftAdapter) ListModels(ctx context.Context, target models.Target, key *credentials.Secret, cursor string) (models.ModelPage, error) {
	return a.list(ctx, target, key, cursor)
}
func TestModelDraftListTestSaveUsesOneTransaction(t *testing.T) {
	var calls atomic.Int32
	adapter := bridgeDraftAdapter{modelTestVerifier: func(ctx context.Context, target models.Target, key *credentials.Secret) (models.Capabilities, error) {
		calls.Add(1)
		return modelTestReady(ctx, target, key)
	}, list: func(_ context.Context, target models.Target, key *credentials.Secret, _ string) (models.ModelPage, error) {
		if target.ModelID != "" || key == nil {
			t.Fatal("discovery requires only a connection")
		}
		return models.ModelPage{Models: []models.AvailableModel{{ID: "first"}, {ID: "second"}}}, nil
	}}
	svc, vault, _ := modelTestService(t, adapter)
	input := modelTestInput()
	input.ModelID = ""
	draft, err := svc.CreateModelDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(draft)
	if strings.Contains(string(encoded), input.APIKey) {
		t.Fatal("draft leaked key")
	}
	listed := svc.ListModelDraft(draft.Token, "", "draft-list")
	if listed.Error != "" || listed.Models == nil || len(listed.Models.Models) != 2 {
		t.Fatalf("list: %+v", listed)
	}
	if _, err = svc.SelectModelDraft(draft.Token, "first", "First"); err != nil {
		t.Fatal(err)
	}
	verified := svc.VerifyModelDraft(draft.Token, "draft-probe")
	if verified.Error != "" || verified.DraftVerification == nil || !verified.DraftVerification.Verified {
		t.Fatalf("probe: %+v", verified)
	}
	if again := svc.VerifyModelDraft(draft.Token, "draft-probe"); again.Error != "" || calls.Load() != 1 {
		t.Fatal("probe replay spent twice")
	}
	state, _ := svc.ModelConnections()
	if state.State.Revision != 0 || len(state.State.Providers) != 0 || vault.creates != 0 {
		t.Fatal("unsaved draft persisted")
	}
	if _, err = svc.SelectModelDraft(draft.Token, "second", "Second"); err != nil {
		t.Fatal(err)
	}
	if old := svc.VerifyModelDraft(draft.Token, "draft-probe"); old.Error != models.ErrReused.Error() {
		t.Fatalf("stale request proof reused: %+v", old)
	}
	if old := svc.SaveModelDraft(draft.Token, "too-early"); old.Receipt != nil || old.Error == "" {
		t.Fatal("changed model reused old proof")
	}
	if fresh := svc.VerifyModelDraft(draft.Token, "draft-probe-2"); fresh.Error != "" {
		t.Fatalf("retest: %+v", fresh)
	}
	saved := svc.SaveModelDraft(draft.Token, "draft-save")
	if saved.Error != "" || saved.Receipt == nil || saved.Receipt.Revision != 1 {
		t.Fatalf("save: %+v", saved)
	}
	if again := svc.SaveModelDraft(draft.Token, "draft-save"); again.Receipt == nil || again.Receipt.Revision != 1 {
		t.Fatalf("save replay: %+v", again)
	}
	state, _ = svc.ModelConnections()
	if state.State.Settings.Mode != "agent" || state.State.Settings.ActiveModelID != saved.Receipt.ModelID || state.State.Models[0].Verification == nil || vault.creates != 1 {
		t.Fatal("save did not atomically activate verified model")
	}
	if !svc.DiscardModelDraft(draft.Token) {
		t.Fatal("completed entry not released")
	}
	operation, err := svc.ModelOperation("draft-save")
	if err != nil || !operation.Found {
		t.Fatal("receipt unavailable after draft closed", err)
	}
}

func TestModelDraftReleaseCancelsAndDestroysTemporaryKey(t *testing.T) {
	for _, reason := range []string{"discard", "expiry", "close", "cancel"} {
		t.Run(reason, func(t *testing.T) {
			entered := make(chan *credentials.Secret, 1)
			svc, vault, _ := modelTestService(t, modelTestVerifier(func(ctx context.Context, _ models.Target, key *credentials.Secret) (models.Capabilities, error) {
				entered <- key
				<-ctx.Done()
				return models.Capabilities{}, ctx.Err()
			}))
			if reason == "expiry" {
				svc.connections.ttl = 100 * time.Millisecond
			}
			draft, err := svc.CreateModelDraft(modelTestInput())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan ModelActionResult, 1)
			go func() { done <- svc.VerifyModelDraft(draft.Token, "blocked-probe") }()
			var key *credentials.Secret
			select {
			case key = <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("probe did not start")
			}
			switch reason {
			case "discard":
				svc.DiscardModelDraft(draft.Token)
			case "close":
				svc.closeModels()
			case "cancel":
				if !svc.CancelModelDraftRequest("blocked-probe") {
					t.Fatal("cancel failed")
				}
			}
			select {
			case result := <-done:
				if result.Error == "" || result.Receipt != nil {
					t.Fatalf("cancel result: %+v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("probe did not stop")
			}
			if reason == "cancel" {
				svc.DiscardModelDraft(draft.Token)
			}
			if bytes, err := key.Bytes(); err == nil {
				credentials.Wipe(bytes)
				t.Fatal("released draft retained secret")
			}
			if len(vault.items) != 0 {
				t.Fatal("cancel wrote a credential")
			}
		})
	}
}

func TestModelDraftFailedProbePreservesCurrentModel(t *testing.T) {
	var fail atomic.Bool
	svc, vault, _ := modelTestService(t, modelTestVerifier(func(ctx context.Context, target models.Target, key *credentials.Secret) (models.Capabilities, error) {
		if fail.Load() {
			return models.Capabilities{}, &models.ConnectionProblem{Code: "authentication"}
		}
		return modelTestReady(ctx, target, key)
	}))
	saved := modelTestSave(t, svc)
	verified := svc.VerifyModelConnection(saved.ModelID, saved.Revision, "ready")
	active := svc.SetWorkShelfMode("agent", saved.ModelID, verified.Receipt.Revision, "use")
	before, _ := svc.ModelConnections()
	beforeJSON, _ := json.Marshal(before.State)
	fail.Store(true)
	input := modelTestInput()
	input.Revision = active.Receipt.Revision
	draft, err := svc.CreateModelDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	if result := svc.VerifyModelDraft(draft.Token, "failed"); result.Error == "" {
		t.Fatal("failed probe succeeded")
	}
	if result := svc.SaveModelDraft(draft.Token, "failed-save"); result.Error == "" || result.Receipt != nil {
		t.Fatal("failed draft saved")
	}
	svc.DiscardModelDraft(draft.Token)
	after, _ := svc.ModelConnections()
	afterJSON, _ := json.Marshal(after.State)
	if string(beforeJSON) != string(afterJSON) || vault.creates != 1 {
		t.Fatal("failed draft changed active state")
	}
}
