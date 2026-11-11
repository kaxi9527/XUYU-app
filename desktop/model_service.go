package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

const modelPreviewLimit = 8
const modelJobLimit = 8

// This is an inbound-only bridge value. Never log or persist the bridge input;
// its string copy cannot be reliably erased by Go or the browser runtime.
type ModelConnectionInput struct {
	Revision         uint64          `json:"revision"`
	ProviderID       string          `json:"providerID"`
	ModelRecordID    string          `json:"modelRecordID"`
	ProviderName     string          `json:"providerName"`
	Protocol         models.Protocol `json:"protocol"`
	BaseURL          string          `json:"baseURL"`
	ModelID          string          `json:"modelID"`
	ModelName        string          `json:"modelName"`
	APIKey           string          `json:"apiKey"`
	RemoveCredential bool            `json:"removeCredential"`
	Options          models.Options  `json:"options"`
}

func (ModelConnectionInput) Format(s fmt.State, verb rune) {
	_, _ = io.WriteString(s, "[model connection input redacted]")
}
func (ModelConnectionInput) MarshalJSON() ([]byte, error) { return nil, credentials.ErrSerialization }

type ModelConnectionPreview struct {
	Token      string                   `json:"token"`
	ExpiresAt  string                   `json:"expiresAt"`
	Connection models.ConnectionPreview `json:"connection"`
}
type ModelConnectionState struct {
	State             models.State `json:"state"`
	CredentialStorage bool         `json:"credentialStorage"`
	ProtocolReady     bool         `json:"protocolReady"`
}
type ModelActionResult struct {
	DraftVerification *models.DraftVerification `json:"draftVerification,omitempty"`
	Revision          uint64                    `json:"revision,omitempty"`
	Receipt           *models.Receipt           `json:"receipt,omitempty"`
	Error             string                    `json:"error,omitempty"`
	Uncertain         bool                      `json:"uncertain"`
	Models            *models.ModelPage         `json:"models,omitempty"`
	Problem           *models.ConnectionProblem `json:"problem,omitempty"`
}
type ModelOperationStatus struct {
	Found   bool            `json:"found"`
	Receipt *models.Receipt `json:"receipt,omitempty"`
}
type modelRun struct {
	operationID string
	kind        string
	identity    string
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	result      ModelActionResult
	completed   bool
	expires     time.Time
}
type modelPreviewEntry struct {
	prepared *models.Prepared
	expires  time.Time
	timer    *time.Timer
	run      *modelRun
	expired  bool
}
type modelConnections struct {
	accounts          modelAccountRuntime
	accountMu         sync.Mutex
	loginID           string
	loginRelease      func() error
	loginResult       ModelLoginState
	mu                sync.Mutex
	store             *models.Store
	client            *modelhttp.Client
	credentialStorage bool
	protocolReady     bool
	entries           map[string]*modelPreviewEntry
	drafts            map[string]*modelDraftEntry
	jobs              map[string]*modelRun
	closed            bool
	active            sync.WaitGroup
	cleanupCancel     context.CancelFunc
	ttl               time.Duration // tests may shorten; production is five minutes
}

// Called with b.mu held. The store is bound once, independent of later config
// environment changes. Reading defaults does not create any files or secrets.
func (b *modelConnections) getStore() (*models.Store, error) {
	if b.closed {
		return nil, models.ErrStopped
	}
	if b.store != nil {
		return b.store, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(filepath.Dir(config.DefaultPath(home)), "models")
	namespace, err := models.NamespaceFor(directory)
	if err != nil {
		return nil, err
	}
	vault, err := credentials.NewVault(namespace)
	if err != nil && !errors.Is(err, credentials.ErrUnavailable) {
		return nil, err
	}
	b.credentialStorage = vault != nil
	b.client = modelhttp.NewClient()
	b.store, err = models.NewStore(directory, vault, &accountModelAdapter{direct: b.client, request: b.accounts.requestContext})
	if err != nil {
		b.client.Close()
		b.client = nil
		return nil, err
	}
	if err = b.store.ClaimSubscriptionOwnership(); err != nil {
		b.store.Close()
		b.store = nil
		b.client.Close()
		b.client = nil
		return nil, err
	}
	b.protocolReady = true
	return b.store, err
}

func (s *ShelfService) ModelConnectionPresets() []modelhttp.Preset { return modelhttp.Presets() }

func (s *ShelfService) ModelConnections() (ModelConnectionState, error) {
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	state := ModelConnectionState{CredentialStorage: b.credentialStorage, ProtocolReady: b.protocolReady}
	b.mu.Unlock()
	if err != nil {
		return state, modelPublicError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state.State, err = store.Read(ctx)
	return state, modelPublicError(err)
}

func (s *ShelfService) PreviewModelConnection(in ModelConnectionInput) (ModelConnectionPreview, error) {
	if len(in.APIKey) > credentials.MaxSecretBytes {
		return ModelConnectionPreview{}, credentials.ErrInvalid
	}
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.getStore()
	if err != nil {
		return ModelConnectionPreview{}, modelPublicError(err)
	}
	if b.entries == nil {
		b.entries = map[string]*modelPreviewEntry{}
	}
	// Finished previews hold no secret; their durable receipts remain queryable.
	for token, entry := range b.entries {
		if entry.run != nil && entry.run.completed || time.Now().After(entry.expires) && entry.run == nil {
			b.releasePreview(token, entry)
		}
	}
	if len(b.entries)+len(b.drafts) >= modelPreviewLimit {
		return ModelConnectionPreview{}, errors.New("已有多个连接正在编辑，请关闭一个后再试")
	}
	var secret *credentials.Secret
	if in.APIKey != "" {
		bytes := []byte(in.APIKey)
		secret, err = credentials.NewSecret(bytes)
		credentials.Wipe(bytes)
		in.APIKey = ""
		if err != nil {
			return ModelConnectionPreview{}, modelPublicError(err)
		}
		defer secret.Destroy()
		if !b.credentialStorage {
			return ModelConnectionPreview{}, credentials.ErrUnavailable
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared, err := store.Prepare(ctx, in.Revision, models.ConnectionInput{
		ProviderID: in.ProviderID, ModelRecordID: in.ModelRecordID, ProviderName: in.ProviderName,
		Protocol: in.Protocol, BaseURL: in.BaseURL, ModelID: in.ModelID, ModelName: in.ModelName,
		Credential: secret, RemoveCredential: in.RemoveCredential, Options: in.Options,
	})
	if err != nil {
		return ModelConnectionPreview{}, modelPublicError(err)
	}
	token, err := versionToken()
	if err != nil {
		prepared.Close()
		return ModelConnectionPreview{}, modelPublicError(err)
	}
	ttl := b.ttl
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	entry := &modelPreviewEntry{prepared: prepared, expires: time.Now().Add(ttl)}
	b.entries[token] = entry
	entry.timer = time.AfterFunc(ttl, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.entries[token] != entry {
			return
		}
		entry.expired = true
		if entry.run == nil || entry.run.completed {
			b.releasePreview(token, entry)
		}
	})
	return ModelConnectionPreview{Token: token, ExpiresAt: entry.expires.UTC().Format(time.RFC3339Nano), Connection: prepared.Preview()}, nil
}

// b.mu must be held; never release a Prepared while Save is using it.
func (b *modelConnections) releasePreview(token string, entry *modelPreviewEntry) {
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.prepared.Close()
	entry.prepared = nil
	delete(b.entries, token)
}

func (s *ShelfService) DiscardModelConnection(token string) bool {
	b := &s.connections
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.entries[token]
	if entry == nil {
		return true
	}
	if entry.run != nil && !entry.run.completed {
		return false
	}
	b.releasePreview(token, entry)
	return true
}

func (s *ShelfService) SaveModelConnection(token, operationID string) ModelActionResult {
	if !modelOperationID(operationID) {
		return ModelActionResult{Error: "操作标识无效"}
	}
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	if err != nil {
		b.mu.Unlock()
		return ModelActionResult{Error: modelPublicError(err).Error()}
	}
	entry := b.entries[token]
	if entry == nil {
		b.mu.Unlock()
		return ModelActionResult{Error: "连接预览已失效，请重新编辑；已保存的操作可查询回执"}
	}
	if entry.run != nil {
		run := entry.run
		b.mu.Unlock()
		if run.operationID != operationID {
			return ModelActionResult{Error: models.ErrReused.Error()}
		}
		<-run.done
		return run.result
	}
	if time.Now().After(entry.expires) {
		b.releasePreview(token, entry)
		b.mu.Unlock()
		return ModelActionResult{Error: "连接预览已过期，请重新编辑"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	run := &modelRun{operationID: operationID, kind: "save", ctx: ctx, cancel: cancel, done: make(chan struct{})}
	entry.run = run
	b.active.Add(1)
	b.mu.Unlock()
	defer b.active.Done()
	defer cancel()
	receipt, err := store.Save(ctx, entry.prepared, operationID)
	result := modelActionOutcome(store, operationID, receipt, err)
	b.mu.Lock()
	entry.prepared.Close()
	entry.prepared = nil
	run.result, run.completed = result, true
	close(run.done)
	if entry.expired || b.closed {
		b.releasePreview(token, entry)
	}
	b.mu.Unlock()
	return result
}

func (s *ShelfService) ModelOperation(operationID string) (ModelOperationStatus, error) {
	if !modelOperationID(operationID) {
		return ModelOperationStatus{}, errors.New("操作标识无效")
	}
	b := &s.connections
	b.mu.Lock()
	store, err := b.getStore()
	b.mu.Unlock()
	if err != nil {
		return ModelOperationStatus{}, modelPublicError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, err := store.Operation(ctx, operationID)
	return ModelOperationStatus{Found: receipt != nil, Receipt: receipt}, modelPublicError(err)
}

// Unexported: Wails must not expose lifecycle shutdown as an ordinary UI action.
func (s *ShelfService) closeModels() {
	s.closeAnalyses()
	b := &s.connections
	b.accounts.close()
	b.accountMu.Lock()
	if b.loginRelease != nil {
		_ = b.loginRelease()
		b.loginRelease = nil
	}
	b.accountMu.Unlock()
	b.mu.Lock()
	b.closed = true
	for token := range b.drafts {
		b.releaseDraftLocked(token)
	}
	for token, entry := range b.entries {
		if entry.run != nil && !entry.run.completed {
			entry.run.cancel()
			entry.timer.Stop()
		} else {
			b.releasePreview(token, entry)
		}
	}
	for _, run := range b.jobs {
		run.cancel()
	}
	if b.cleanupCancel != nil {
		b.cleanupCancel()
	}
	if b.store != nil {
		b.store.Close()
	}
	b.mu.Unlock()
	b.active.Wait()
	if b.client != nil {
		b.client.Close()
	}
}

func modelOperationID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}

func modelPublicError(err error) error {
	if err == nil {
		return nil
	}
	var classified *models.ConnectionProblem
	if errors.As(err, &classified) && classified != nil && classified.Valid() {
		return classified
	}
	for _, safe := range []error{models.ErrConflict, models.ErrCorrupt, models.ErrReused, models.ErrUnverified, models.ErrStopped, models.ErrAccountChanging, models.ErrAccountOwned, models.ErrCredentialScope, models.ErrDraftExpired,
		credentials.ErrUnavailable, credentials.ErrLocked, credentials.ErrNotFound, credentials.ErrInvalid} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("操作已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("操作超时，请查询保存结果或重试")
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return errors.New("模型配置无法读写，原配置已保留；请检查磁盘与权限")
	}
	// Only constant domain validation text may cross the bridge. Arbitrary
	// runtime/upstream errors can include URLs, headers or sensitive payloads.
	switch err.Error() {
	case "请填写有效的提供商、协议和模型；凭据替换与移除不能同时选择", "模型显示名称无效", "提供商不存在", "模型不属于所选提供商", "请填写有效的模型 ID", "请先选择或填写模型 ID", "订阅连接请通过账号授权选择模型",
		"请输入不含凭据、查询参数或片段的模型服务地址", "模型地址必须使用 HTTPS 或本机 HTTP", "模型不存在", "此版本尚未配置模型协议适配器",
		"不支持所选模型协议", "鉴权请求头无效", "这个请求头不能用作模型鉴权", "Claude API 版本应为有效日期", "只有 Claude 协议使用 API 日期版本", "Chat 输出上限字段无效", "只有 Chat 协议使用此输出上限字段", "连接测试输出上限应为 16 到 4096 token":
		return err
	}
	return errors.New("模型连接操作未完成，请检查配置后重试")
}

func modelActionOutcome(store *models.Store, id string, receipt models.Receipt, err error) ModelActionResult {
	if err == nil {
		return ModelActionResult{Receipt: &receipt}
	}
	result := ModelActionResult{Error: modelPublicError(err).Error()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stored, checkErr := store.Operation(ctx, id)
	if checkErr != nil {
		result.Uncertain = true
		return result
	}
	if stored != nil {
		if receipt.OperationID == id && receipt.Digest == stored.Digest {
			return ModelActionResult{Receipt: stored}
		}
		// Never interpret an older operation with different input as this save.
		result.Uncertain = true
	}
	return result
}
