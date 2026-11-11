package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

type modelTestVault struct {
	mu           sync.Mutex
	items        map[string][]byte
	creates      int
	failDelete   bool
	beforeCreate func(context.Context)
}

func (v *modelTestVault) Create(ctx context.Context, ref string, secret *credentials.Secret) error {
	if v.beforeCreate != nil {
		v.beforeCreate(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := secret.Bytes()
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, found := v.items[ref]; found {
		credentials.Wipe(value)
		return credentials.ErrExists
	}
	v.items[ref] = value
	v.creates++
	return nil
}
func (v *modelTestVault) Read(ctx context.Context, ref string) (*credentials.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	value, found := v.items[ref]
	if !found {
		return nil, credentials.ErrNotFound
	}
	return credentials.NewSecret(value)
}
func (v *modelTestVault) Delete(ctx context.Context, ref string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failDelete {
		return credentials.ErrLocked
	}
	if value, found := v.items[ref]; found {
		credentials.Wipe(value)
		delete(v.items, ref)
		return nil
	}
	return credentials.ErrNotFound
}

type modelTestVerifier func(context.Context, models.Target, *credentials.Secret) (models.Capabilities, error)

func (v modelTestVerifier) Verify(ctx context.Context, target models.Target, secret *credentials.Secret) (models.Capabilities, error) {
	return v(ctx, target, secret)
}

func modelTestService(t *testing.T, verifier models.Verifier) (*ShelfService, *modelTestVault, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "models")
	vault := &modelTestVault{items: map[string][]byte{}}
	store, err := models.NewStore(directory, vault, verifier)
	if err != nil {
		t.Fatal(err)
	}
	svc := &ShelfService{}
	svc.connections.store = store
	svc.connections.credentialStorage = true
	svc.connections.protocolReady = verifier != nil
	t.Cleanup(svc.closeModels)
	return svc, vault, directory
}
func modelTestInput() ModelConnectionInput {
	return ModelConnectionInput{ProviderName: "Fixture", Protocol: models.Chat, BaseURL: "https://example.test/v1/", ModelID: "fixture-model", APIKey: "synthetic-do-not-persist"}
}
func modelTestSave(t *testing.T, svc *ShelfService) models.Receipt {
	t.Helper()
	preview, err := svc.PreviewModelConnection(modelTestInput())
	if err != nil {
		t.Fatal(err)
	}
	result := svc.SaveModelConnection(preview.Token, "save-fixture")
	if result.Error != "" || result.Receipt == nil {
		t.Fatalf("save: %+v", result)
	}
	return *result.Receipt
}
func modelTestReady(context.Context, models.Target, *credentials.Secret) (models.Capabilities, error) {
	return models.Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
}

func TestModelBridgeInputAndPreviewAreRedacted(t *testing.T) {
	svc, vault, directory := modelTestService(t, nil)
	state, err := svc.ModelConnections()
	if err != nil || state.State.Settings.Mode != "tool" || state.ProtocolReady {
		t.Fatalf("state: %+v %v", state, err)
	}
	in := modelTestInput()
	for _, value := range []any{in, &in} {
		if strings.Contains(fmt.Sprintf("%+v %#v %s", value, value, value), in.APIKey) {
			t.Fatal("format leaked key")
		}
		if _, err := json.Marshal(value); !errors.Is(err, credentials.ErrSerialization) {
			t.Fatalf("input serialized: %v", err)
		}
	}
	var decoded ModelConnectionInput
	if err := json.Unmarshal([]byte(`{"apiKey":"synthetic-inbound","providerName":"Fixture"}`), &decoded); err != nil || decoded.APIKey != "synthetic-inbound" {
		t.Fatal("inbound bridge cannot decode")
	}
	preview, err := svc.PreviewModelConnection(in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Connection.BaseURL != "https://example.test/v1" || preview.Connection.CredentialAction != "replace" {
		t.Fatalf("normalized preview: %+v", preview)
	}
	encoded, _ := json.Marshal(preview)
	if strings.Contains(string(encoded), in.APIKey) || strings.Contains(string(encoded), "secretRef") {
		t.Fatal("preview leaked credential")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("read or prepare created configuration")
	}
	if !svc.DiscardModelConnection(preview.Token) || vault.creates != 0 {
		t.Fatal("cancel must not persist")
	}
	if r := svc.SaveModelConnection(preview.Token, "late"); r.Receipt != nil || r.Error == "" {
		t.Fatalf("discarded preview saved: %+v", r)
	}
	if strings.Contains(modelPublicError(errors.New(in.APIKey)).Error(), in.APIKey) {
		t.Fatal("runtime error leaked input")
	}
}

func TestModelPreviewsAreBoundedExpireAndClose(t *testing.T) {
	svc, vault, _ := modelTestService(t, nil)
	svc.connections.ttl = time.Minute
	for i := 0; i < modelPreviewLimit; i++ {
		if _, err := svc.PreviewModelConnection(modelTestInput()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.PreviewModelConnection(modelTestInput()); err == nil {
		t.Fatal("unbounded previews")
	}
	svc.closeModels()
	if len(svc.connections.entries) != 0 || vault.creates != 0 {
		t.Fatal("capacity previews survived close")
	}
	svc, vault, _ = modelTestService(t, nil)
	svc.connections.ttl = 40 * time.Millisecond
	if _, err := svc.PreviewModelConnection(modelTestInput()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.connections.mu.Lock()
		count := len(svc.connections.entries)
		svc.connections.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired secrets still retained")
		}
		time.Sleep(time.Millisecond)
	}
	svc.connections.ttl = time.Minute
	if _, err := svc.PreviewModelConnection(modelTestInput()); err != nil {
		t.Fatal(err)
	}
	svc.closeModels()
	if len(svc.connections.entries) != 0 || vault.creates != 0 {
		t.Fatal("shutdown retained previews or wrote credentials")
	}
	if _, err := svc.PreviewModelConnection(modelTestInput()); !errors.Is(err, models.ErrStopped) {
		t.Fatalf("closed broker: %v", err)
	}
}

func TestModelCredentialCleanupIsRetryable(t *testing.T) {
	svc, vault, _ := modelTestService(t, nil)
	first := modelTestSave(t, svc)
	in := modelTestInput()
	in.ProviderID, in.ModelRecordID, in.Revision, in.APIKey = first.ProviderID, first.ModelID, first.Revision, "synthetic-replacement"
	preview, err := svc.PreviewModelConnection(in)
	if err != nil {
		t.Fatal(err)
	}
	vault.failDelete = true
	saved := svc.SaveModelConnection(preview.Token, "replace")
	if saved.Error != "" || saved.Receipt == nil {
		t.Fatalf("replacement: %+v", saved)
	}
	state, err := svc.ModelConnections()
	if err != nil || state.State.PendingCredentialCleanup == 0 {
		t.Fatalf("pending not visible: %+v %v", state, err)
	}
	failed := svc.RetryModelCredentialCleanup()
	if failed.Pending == nil || *failed.Pending == 0 || failed.Error == "" {
		t.Fatalf("failed cleanup hidden: %+v", failed)
	}
	vault.failDelete = false
	cleaned := svc.RetryModelCredentialCleanup()
	if cleaned.Error != "" || cleaned.Pending == nil || *cleaned.Pending != 0 || len(vault.items) != 1 {
		t.Fatalf("cleanup removed active credential or retained inactive: %+v count=%d", cleaned, len(vault.items))
	}
}

func TestModelSaveSingleFlightAndRestartReceipt(t *testing.T) {
	svc, vault, directory := modelTestService(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	vault.beforeCreate = func(ctx context.Context) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	p, err := svc.PreviewModelConnection(modelTestInput())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan ModelActionResult, 2)
	go func() { results <- svc.SaveModelConnection(p.Token, "save-once") }()
	<-entered
	if svc.DiscardModelConnection(p.Token) {
		t.Fatal("in-progress save incorrectly reported cancelled")
	}
	if r := svc.SaveModelConnection(p.Token, "different-operation"); r.Error != models.ErrReused.Error() {
		t.Fatalf("token rebound: %+v", r)
	}
	go func() { results <- svc.SaveModelConnection(p.Token, "save-once") }()
	close(release)
	first, second := <-results, <-results
	if first.Receipt == nil || second.Receipt == nil || *first.Receipt != *second.Receipt || vault.creates != 1 {
		t.Fatalf("duplicate save: %+v %+v creates=%d", first, second, vault.creates)
	}
	svc.connections.mu.Lock()
	retained := svc.connections.entries[p.Token].prepared
	svc.connections.mu.Unlock()
	if retained != nil {
		t.Fatal("completed save retained secret")
	}
	state, err := svc.ModelConnections()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), modelTestInput().APIKey) || strings.Contains(string(encoded), "secretRef") {
		t.Fatal("public state leaked secret")
	}
	_ = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), modelTestInput().APIKey) {
				t.Fatal("key stored in ordinary file")
			}
		}
		return err
	})
	svc.closeModels()
	store, err := models.NewStore(directory, vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &ShelfService{}
	restarted.connections.store = store
	t.Cleanup(restarted.closeModels)
	recovered, err := restarted.ModelOperation("save-once")
	if err != nil || !recovered.Found || *recovered.Receipt != *first.Receipt {
		t.Fatalf("lost acknowledgement: %+v %v", recovered, err)
	}
	if result := restarted.SaveModelConnection(p.Token, "save-once"); result.Receipt != nil || result.Error == "" {
		t.Fatal("restart invented lost preview")
	}
}

func TestModelStalePreviewAndAbsentAdapter(t *testing.T) {
	svc, vault, _ := modelTestService(t, nil)
	stale, err := svc.PreviewModelConnection(modelTestInput())
	if err != nil {
		t.Fatal(err)
	}
	saved := modelTestSave(t, svc)
	failed := svc.SaveModelConnection(stale.Token, "stale-save")
	if failed.Error != models.ErrConflict.Error() || failed.Uncertain || failed.Receipt != nil || vault.creates != 1 {
		t.Fatalf("stale save: %+v", failed)
	}
	if result := svc.VerifyModelConnection(saved.ModelID, saved.Revision, "verify-no-adapter"); result.Error != "此版本尚未配置模型协议适配器" || result.Receipt != nil {
		t.Fatalf("fake production verification: %+v", result)
	}
	if result := svc.SetWorkShelfMode("agent", saved.ModelID, saved.Revision, "start-unverified"); result.Error != models.ErrUnverified.Error() {
		t.Fatalf("unverified start: %+v", result)
	}
	if result := svc.SetWorkShelfMode("tool", "", saved.Revision, "save-fixture"); !result.Uncertain || result.Receipt != nil {
		t.Fatalf("reused ID acknowledged as a different action: %+v", result)
	}
}

func TestModelVerificationCancellationAndToolMode(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	verifier := modelTestVerifier(func(ctx context.Context, _ models.Target, _ *credentials.Secret) (models.Capabilities, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return models.Capabilities{}, ctx.Err()
	})
	svc, _, _ := modelTestService(t, verifier)
	saved := modelTestSave(t, svc)
	done := make(chan ModelActionResult, 1)
	go func() { done <- svc.VerifyModelConnection(saved.ModelID, saved.Revision, "probe") }()
	<-entered
	if !svc.CancelModelVerification("probe") {
		t.Fatal("cannot cancel probe")
	}
	first := <-done
	second := svc.VerifyModelConnection(saved.ModelID, saved.Revision, "probe")
	if first.Error != "操作已取消" || second.Error != first.Error || calls.Load() != 1 {
		t.Fatalf("cancel/replay: %+v %+v calls=%d", first, second, calls.Load())
	}
	go func() { done <- svc.VerifyModelConnection(saved.ModelID, saved.Revision, "probe-2") }()
	<-entered
	stopped := svc.SetWorkShelfMode("tool", "", saved.Revision, "stop")
	if stopped.Error != "" || stopped.Receipt == nil {
		t.Fatalf("stop: %+v", stopped)
	}
	if result := <-done; result.Error != "操作已取消" || result.Receipt != nil {
		t.Fatalf("late probe: %+v", result)
	}
	state, err := svc.ModelConnections()
	if err != nil || state.State.Settings.Mode != "tool" || state.State.Models[0].Verification != nil {
		t.Fatalf("cancelled probe changed mode or verification: %+v %v", state, err)
	}
}

func TestModelVerifiedModeAndDeletion(t *testing.T) {
	svc, vault, _ := modelTestService(t, modelTestVerifier(modelTestReady))
	saved := modelTestSave(t, svc)
	verified := svc.VerifyModelConnection(saved.ModelID, saved.Revision, "verify")
	if verified.Receipt == nil || verified.Receipt.Verified == nil || !*verified.Receipt.Verified {
		t.Fatalf("verification: %+v", verified)
	}
	started := svc.SetWorkShelfMode("agent", saved.ModelID, verified.Receipt.Revision, "start")
	if started.Error != "" || started.Receipt == nil {
		t.Fatalf("start: %+v", started)
	}
	lease, err := svc.connections.store.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	deleted := svc.DeleteModelProvider(saved.ProviderID, started.Receipt.Revision, "delete")
	if deleted.Error != "" || deleted.Receipt == nil {
		t.Fatalf("delete: %+v", deleted)
	}
	select {
	case <-lease.Context.Done():
	default:
		t.Fatal("delete did not stop current lease")
	}
	state, err := svc.ModelConnections()
	if err != nil || state.State.Settings.Mode != "tool" || len(state.State.Providers) != 0 || len(vault.items) != 0 {
		t.Fatalf("delete left active connection: %+v %v", state, err)
	}
}

func TestModelStopAvailableAtProbeCapacity(t *testing.T) {
	entered := make(chan struct{}, modelJobLimit)
	verifier := modelTestVerifier(func(ctx context.Context, _ models.Target, _ *credentials.Secret) (models.Capabilities, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return models.Capabilities{}, ctx.Err()
	})
	svc, _, _ := modelTestService(t, verifier)
	saved := modelTestSave(t, svc)
	done := make(chan ModelActionResult, modelJobLimit)
	for i := 0; i < modelJobLimit; i++ {
		go func(i int) {
			done <- svc.VerifyModelConnection(saved.ModelID, saved.Revision, fmt.Sprintf("probe-%d", i))
		}(i)
	}
	for i := 0; i < modelJobLimit; i++ {
		<-entered
	}
	if result := svc.VerifyModelConnection(saved.ModelID, saved.Revision, "overflow"); result.Error == "" {
		t.Fatal("unbounded model jobs")
	}
	if result := svc.SetWorkShelfMode("tool", "", saved.Revision, "stop-all"); result.Error != "" || result.Receipt == nil {
		t.Fatalf("stop blocked by probes: %+v", result)
	}
	for i := 0; i < modelJobLimit; i++ {
		if result := <-done; result.Error != "操作已取消" {
			t.Fatalf("probe not cancelled: %+v", result)
		}
	}
}

func TestModelCloseCancelsSaveAndVerification(t *testing.T) {
	svc, vault, _ := modelTestService(t, nil)
	entered := make(chan struct{})
	vault.beforeCreate = func(ctx context.Context) { close(entered); <-ctx.Done() }
	p, err := svc.PreviewModelConnection(modelTestInput())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan ModelActionResult, 1)
	go func() { done <- svc.SaveModelConnection(p.Token, "save-closing") }()
	<-entered
	svc.closeModels()
	result := <-done
	if result.Receipt != nil || result.Error == "" || vault.creates != 0 || len(svc.connections.entries) != 0 {
		t.Fatalf("close left save/secret: %+v", result)
	}

	probeStarted := make(chan struct{})
	probing, _, _ := modelTestService(t, modelTestVerifier(func(ctx context.Context, _ models.Target, _ *credentials.Secret) (models.Capabilities, error) {
		close(probeStarted)
		<-ctx.Done()
		return models.Capabilities{}, ctx.Err()
	}))
	saved := modelTestSave(t, probing)
	go func() { done <- probing.VerifyModelConnection(saved.ModelID, saved.Revision, "probe-closing") }()
	<-probeStarted
	probing.closeModels()
	if result = <-done; result.Receipt != nil || result.Error != "操作已取消" {
		t.Fatalf("close left verification: %+v", result)
	}
}
