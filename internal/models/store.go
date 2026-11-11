package models

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

type Store struct {
	directory           string
	vault               credentials.Vault
	verifier            Verifier
	replace             func(string, string) error
	gate                chan struct{}
	leaseMu             sync.Mutex
	leases              map[*Lease]context.CancelFunc
	closed              atomic.Bool
	armed               atomic.Bool
	subscriptionUse     sync.RWMutex
	subscriptionChanges atomic.Int32
	subscriptionOwnerMu sync.Mutex
	subscriptionOwner   *os.File
}

func NewStore(directory string, vault credentials.Vault, verifier Verifier) (*Store, error) {
	canonical, err := canonicalDirectory(directory)
	if err != nil {
		return nil, err
	}
	return &Store{directory: canonical, vault: vault, verifier: verifier, replace: os.Rename, gate: make(chan struct{}, 1), leases: map[*Lease]context.CancelFunc{}}, nil
}

// NamespaceFor keeps different local configuration stores out of each other's
// keychain entries. No filesystem path or user name is sent to Keychain.
func NamespaceFor(directory string) (string, error) {
	canonical, err := canonicalDirectory(directory)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}
func (s *Store) locked(ctx context.Context, fn func() error) error {
	if s.closed.Load() {
		return ErrStopped
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.gate }()
	if s.closed.Load() {
		return ErrStopped
	}
	if _, err := s.directoryReady(true); err != nil {
		return err
	}
	path := filepath.Join(s.directory, ".connections.lock")
	lock, err := lockFile(ctx, path)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	opened, e := lock.Stat()
	current, e2 := os.Lstat(path)
	if e != nil || e2 != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return ErrConflict
	}
	if _, err = s.directoryReady(false); err != nil {
		return err
	}
	return fn()
}
func (s *Store) Read(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	c, _, err := s.readCatalog()
	if err != nil {
		return State{}, err
	}
	out := State{Revision: c.Revision, Settings: c.Settings, Models: c.Models, Providers: []PublicProvider{}, SessionReady: c.Settings.Mode == "agent" && s.armed.Load() && !s.closed.Load()}
	for _, p := range c.Providers {
		options, _ := NormalizeOptions(p.Protocol, p.Options)
		out.Providers = append(out.Providers, PublicProvider{ID: p.ID, Name: p.Name, Protocol: p.Protocol, BaseURL: p.BaseURL, HasCredential: p.SecretRef != "", Options: options})
	}
	j, _, err := s.pendingSecrets()
	if err != nil {
		return State{}, err
	}
	active := map[string]bool{}
	for _, p := range c.Providers {
		active[p.SecretRef] = true
	}
	for _, ref := range j.References {
		if !active[ref] {
			out.PendingCredentialCleanup++
		}
	}
	return out, nil
}
func (s *Store) Operation(ctx context.Context, id string) (*Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, _, err := s.readCatalog()
	if err != nil {
		return nil, err
	}
	for _, r := range c.Operations {
		if r.OperationID == id {
			copy := r
			return &copy, nil
		}
	}
	return nil, nil
}
func (s *Store) Prepare(ctx context.Context, revision uint64, in ConnectionInput) (*Prepared, error) {
	return s.prepare(ctx, revision, in, false)
}

func (s *Store) prepare(ctx context.Context, revision uint64, in ConnectionInput, draft bool) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !label(in.ProviderName, 120) || (!label(in.ModelID, 256) && !(draft && in.ModelID == "")) || !validProtocol(in.Protocol) || (in.Credential != nil && in.RemoveCredential) {
		return nil, errors.New("请填写有效的提供商、协议和模型；凭据替换与移除不能同时选择")
	}
	if in.ModelName == "" {
		name := []rune(in.ModelID)
		if len(name) > 120 {
			name = name[:120]
		}
		in.ModelName = string(name)
	}
	if !label(in.ModelName, 120) && !(draft && in.ModelID == "" && in.ModelName == "") {
		return nil, errors.New("模型显示名称无效")
	}
	endpoint, err := normalizeEndpoint(in.BaseURL)
	if err != nil {
		return nil, err
	}
	if in.Protocol == Subscription && (SubscriptionAgent(endpoint) == "" || in.Credential != nil) {
		return nil, errors.New("订阅模型只能使用内置授权连接")
	}
	c, _, err := s.readCatalog()
	if err != nil {
		return nil, err
	}
	if c.Revision != revision {
		return nil, ErrConflict
	}
	p := &Prepared{revision: revision, providerID: in.ProviderID, modelID: in.ModelRecordID, name: in.ProviderName, baseURL: endpoint, upstreamModel: in.ModelID, modelName: in.ModelName, protocol: in.Protocol, removeSecret: in.RemoveCredential}
	p.owner = s.directory
	p.options, err = NormalizeOptions(in.Protocol, in.Options)
	if err != nil {
		return nil, err
	}
	if old := findProvider(&c, p.providerID); old != nil && old.SecretRef != "" && in.Credential == nil && !in.RemoveCredential {
		options, e := NormalizeOptions(old.Protocol, old.Options)
		if e != nil || old.Protocol != p.protocol || old.BaseURL != p.baseURL || options.AuthHeader != p.options.AuthHeader {
			return nil, ErrCredentialScope
		}
	}
	if p.providerID == "" {
		p.providerID, err = identifier("provider_")
	} else if findProvider(&c, p.providerID) == nil {
		return nil, errors.New("提供商不存在")
	}
	if err != nil {
		return nil, err
	}
	if p.modelID == "" {
		for _, existing := range c.Models {
			if existing.ProviderID == p.providerID && existing.ModelID == p.upstreamModel {
				p.modelID = existing.ID
				break
			}
		}
		if p.modelID == "" {
			p.modelID, err = identifier("model_")
		}
	} else {
		m := findModel(&c, p.modelID)
		if m == nil || m.ProviderID != p.providerID {
			return nil, errors.New("模型不属于所选提供商")
		}
	}
	if err != nil {
		return nil, err
	}
	if in.Credential != nil {
		value, e := in.Credential.Bytes()
		if e != nil {
			return nil, e
		}
		defer credentials.Wipe(value)
		p.secret, e = credentials.NewSecret(value)
		if e != nil {
			return nil, e
		}
		p.secretRef, e = credentials.NewReference()
		if e != nil {
			p.Close()
			return nil, e
		}
	}
	return p, nil
}
func findProvider(c *catalog, id string) *Provider {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}
func findModel(c *catalog, id string) *Model {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}
func digest(action string, value any) string {
	b, _ := json.Marshal(struct {
		Action string
		Value  any
	}{action, value})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func replay(c catalog, id, hash string) (*Receipt, error) {
	if !label(id, 128) {
		return nil, errors.New("操作标识无效")
	}
	for _, r := range c.Operations {
		if r.OperationID == id {
			if r.Digest != hash {
				return nil, ErrReused
			}
			copy := r
			return &copy, nil
		}
	}
	return nil, nil
}
func (s *Store) commit(ctx context.Context, c *catalog, original []byte, receipt Receipt, invalidate bool) (Receipt, error) {
	c.Revision++
	if invalidate {
		c.Settings.Epoch++
	}
	receipt.Revision = c.Revision
	c.Operations = append(c.Operations, receipt)
	if err := validate(*c); err != nil {
		return Receipt{}, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return Receipt{}, err
	}
	committed, err := s.writeAtomic(ctx, "connections.json", data, original)
	if committed {
		if invalidate {
			s.stopLeases()
			if c.Settings.Mode != "agent" {
				s.armed.Store(false)
			}
		}
		return receipt, err
	}
	return Receipt{}, err
}

type cleanupJournal struct {
	Schema     int      `json:"schema"`
	References []string `json:"references"`
}

func (s *Store) pendingSecrets() (cleanupJournal, []byte, error) {
	j := cleanupJournal{Schema: 1, References: []string{}}
	data, err := s.readBytes("credential-cleanup.json")
	if err != nil || data == nil {
		return j, data, err
	}
	if decode(data, &j) != nil || j.Schema != 1 || len(j.References) > 512 {
		return j, data, ErrCorrupt
	}
	seen := map[string]bool{}
	for _, ref := range j.References {
		if !credentials.ValidReference(ref) || seen[ref] {
			return j, data, ErrCorrupt
		}
		seen[ref] = true
	}
	return j, data, nil
}
func (s *Store) reserveSecrets(ctx context.Context, refs ...string) error {
	j, original, err := s.pendingSecrets()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, ref := range j.References {
		seen[ref] = true
	}
	for _, ref := range refs {
		if ref != "" && !seen[ref] {
			j.References = append(j.References, ref)
			seen[ref] = true
		}
	}
	if len(j.References) > 512 {
		return errors.New("有过多凭据等待安全清理，请先重试清理")
	}
	if len(j.References) == 0 && original == nil {
		return nil
	}
	data, _ := json.Marshal(j)
	_, err = s.writeAtomic(ctx, "credential-cleanup.json", data, original)
	return err
}
func (s *Store) cleanup(ctx context.Context, c catalog) error {
	j, original, err := s.pendingSecrets()
	if err != nil || original == nil {
		return err
	}
	active := map[string]bool{}
	for _, p := range c.Providers {
		if p.SecretRef != "" {
			active[p.SecretRef] = true
		}
	}
	remaining := []string{}
	var first error
	for _, ref := range j.References {
		if active[ref] {
			continue
		}
		if s.vault == nil {
			remaining = append(remaining, ref)
			first = credentials.ErrUnavailable
			continue
		}
		e := s.vault.Delete(ctx, ref)
		if e != nil && !errors.Is(e, credentials.ErrNotFound) {
			remaining = append(remaining, ref)
			first = e
		}
	}
	j.References = remaining
	data, _ := json.Marshal(j)
	_, err = s.writeAtomic(ctx, "credential-cleanup.json", data, original)
	if err != nil {
		return err
	}
	return first
}

// Cleanup only considers references reserved by this store, never enumerating
// the OS keychain. A currently referenced credential is always preserved.
func (s *Store) Cleanup(ctx context.Context) error {
	return s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		return s.cleanup(ctx, c)
	})
}

func (s *Store) Save(ctx context.Context, p *Prepared, operationID string) (Receipt, error) {
	return s.save(ctx, p, operationID, nil, false)
}

func (s *Store) save(ctx context.Context, p *Prepared, operationID string, proof *draftProof, activate bool) (Receipt, error) {
	if p == nil || p.owner != s.directory {
		return Receipt{}, errors.New("连接预览不存在")
	}
	hash := digest("save", []any{p.revision, p.providerID, p.modelID, p.name, p.protocol, p.baseURL, p.upstreamModel, p.modelName, p.secretRef, p.removeSecret, p.options})
	if proof != nil {
		hash = digest("save-verified", []any{hash, proof.id, activate})
	}
	var result Receipt
	err := s.locked(ctx, func() error {
		c, original, err := s.readCatalog()
		if err != nil {
			return err
		}
		old, err := replay(c, operationID, hash)
		if err != nil {
			return err
		}
		if old != nil {
			result = *old
			return nil
		}
		if c.Revision != p.revision {
			return ErrConflict
		}
		if proof != nil && time.Now().After(proof.expires) {
			return ErrDraftExpired
		}
		provider := findProvider(&c, p.providerID)
		if provider == nil {
			c.Providers = append(c.Providers, Provider{ID: p.providerID})
			provider = &c.Providers[len(c.Providers)-1]
		}
		oldSecret := provider.SecretRef
		previousOptions, _ := NormalizeOptions(provider.Protocol, provider.Options)
		providerChanged := provider.Version == 0 || provider.Protocol != p.protocol || provider.BaseURL != p.baseURL || previousOptions != p.options || p.secretRef != "" || (p.removeSecret && oldSecret != "")
		if p.secretRef != "" || p.removeSecret {
			if err = s.reserveSecrets(ctx, p.secretRef, oldSecret); err != nil {
				return err
			}
		}
		if p.secretRef != "" {
			if s.vault == nil {
				return credentials.ErrUnavailable
			}
			if err = s.vault.Create(ctx, p.secretRef, p.secret); errors.Is(err, credentials.ErrExists) {
				stored, e := s.vault.Read(ctx, p.secretRef)
				if e != nil {
					return e
				}
				defer stored.Destroy()
				a, e := stored.Bytes()
				if e != nil {
					return e
				}
				defer credentials.Wipe(a)
				b, e := p.secret.Bytes()
				if e != nil {
					return e
				}
				defer credentials.Wipe(b)
				if subtle.ConstantTimeCompare(a, b) != 1 {
					return credentials.ErrExists
				}
				err = nil
			}
			if err != nil {
				return err
			}
			provider.SecretRef = p.secretRef
		} else if p.removeSecret {
			provider.SecretRef = ""
		}
		provider.Name = p.name
		provider.Protocol = p.protocol
		provider.BaseURL = p.baseURL
		provider.Options = p.options
		if providerChanged {
			provider.Version++
		}
		for i := range c.Models {
			if providerChanged && c.Models[i].ProviderID == provider.ID {
				c.Models[i].Verification = nil
				if c.Settings.ActiveModelID == c.Models[i].ID {
					c.Settings.Mode = "tool"
				}
			}
		}
		model := findModel(&c, p.modelID)
		if model == nil {
			c.Models = append(c.Models, Model{ID: p.modelID, ProviderID: provider.ID})
			model = &c.Models[len(c.Models)-1]
		}
		if model.ModelID != p.upstreamModel {
			model.Verification = nil
			if c.Settings.ActiveModelID == model.ID {
				c.Settings.Mode = "tool"
			}
		}
		model.ModelID = p.upstreamModel
		model.Name = p.modelName
		receipt := Receipt{OperationID: operationID, Digest: hash, ProviderID: provider.ID, ModelID: model.ID}
		if proof != nil {
			if time.Now().After(proof.expires) {
				return ErrDraftExpired
			}
			passed := true
			receipt.Verified = &passed
			model.Verification = &Verification{ProviderVersion: provider.Version, At: proof.at.UTC().Format(time.RFC3339Nano), Capabilities: proof.capabilities}
			if activate {
				c.Settings.Mode = "agent"
				c.Settings.ActiveModelID = model.ID
			}
		}
		result, err = s.commit(ctx, &c, original, receipt, true)
		if proof != nil && activate && result.Revision > 0 {
			s.armed.Store(true)
		}
		if err == nil {
			_ = s.cleanup(ctx, c)
		} // The durable cleanup journal retains failures for explicit retry.
		return err
	})
	return result, err
}

func (s *Store) SetMode(ctx context.Context, revision uint64, operationID, mode, modelID string) (Receipt, error) {
	if mode != "tool" && mode != "agent" {
		return Receipt{}, errors.New("请选择工具或 Agent 模式")
	}
	hash := digest("mode", []any{revision, mode, modelID})
	var result Receipt
	err := s.locked(ctx, func() error {
		c, original, err := s.readCatalog()
		if err != nil {
			return err
		}
		old, err := replay(c, operationID, hash)
		if err != nil {
			return err
		}
		if old != nil {
			result = *old
			return nil
		}
		if c.Revision != revision {
			return ErrConflict
		}
		if mode == "agent" {
			m := findModel(&c, modelID)
			if m == nil || m.Verification == nil {
				return ErrUnverified
			}
			p := findProvider(&c, m.ProviderID)
			if p != nil && p.Protocol == Subscription && s.subscriptionChanges.Load() > 0 {
				return ErrAccountChanging
			}
			if p != nil && p.Protocol == Subscription {
				if err := s.ClaimSubscriptionOwnership(); err != nil {
					return err
				}
			}
			if p == nil || m.Verification.ProviderVersion != p.Version {
				return ErrUnverified
			}
			if p.SecretRef != "" {
				if s.vault == nil {
					return credentials.ErrUnavailable
				}
				secret, e := s.vault.Read(ctx, p.SecretRef)
				if e != nil {
					return e
				}
				secret.Destroy()
			}
			c.Settings.ActiveModelID = modelID
		}
		c.Settings.Mode = mode
		result, err = s.commit(ctx, &c, original, Receipt{OperationID: operationID, Digest: hash, ModelID: modelID}, true)
		if result.Revision > 0 {
			s.armed.Store(mode == "agent")
		}
		return err
	})
	return result, err
}

type VerificationResult struct {
	Receipt  Receipt
	Verified bool
}

func (s *Store) VerifyModel(ctx context.Context, revision uint64, operationID, modelID string) (VerificationResult, error) {
	if s.closed.Load() {
		return VerificationResult{}, ErrStopped
	}
	if s.verifier == nil {
		return VerificationResult{}, errors.New("此版本尚未配置模型协议适配器")
	}
	hash := digest("verify", []any{revision, modelID})
	c, _, err := s.readCatalog()
	if err != nil {
		return VerificationResult{}, err
	}
	old, err := replay(c, operationID, hash)
	if err != nil {
		return VerificationResult{}, err
	}
	if old != nil {
		return VerificationResult{Receipt: *old, Verified: old.Verified != nil && *old.Verified}, nil
	}
	if c.Revision != revision {
		return VerificationResult{}, ErrConflict
	}
	m := findModel(&c, modelID)
	if m == nil {
		return VerificationResult{}, errors.New("模型不存在")
	}
	p := findProvider(&c, m.ProviderID)
	if p.Protocol == Subscription {
		if err := s.ClaimSubscriptionOwnership(); err != nil {
			return VerificationResult{}, err
		}
		s.subscriptionUse.RLock()
		defer s.subscriptionUse.RUnlock()
		if s.subscriptionChanges.Load() > 0 {
			return VerificationResult{}, ErrAccountChanging
		}
	}
	var secret *credentials.Secret
	if p.SecretRef != "" {
		if s.vault == nil {
			return VerificationResult{}, credentials.ErrUnavailable
		}
		secret, err = s.vault.Read(ctx, p.SecretRef)
		if err != nil {
			return VerificationResult{}, err
		}
		defer secret.Destroy()
	}
	caps, probeErr := s.verifier.Verify(ctx, Target{Protocol: p.Protocol, BaseURL: p.BaseURL, ModelID: m.ModelID, Options: p.Options}, secret)
	if err = ctx.Err(); err != nil {
		return VerificationResult{}, err
	}
	passed := probeErr == nil && validCapabilities(caps) && caps.Text == "supported"
	var problem *ConnectionProblem
	if !passed {
		problem = &ConnectionProblem{Code: "verification_failed"}
		var classified *ConnectionProblem
		if errors.As(probeErr, &classified) && classified != nil && classified.Valid() {
			copy := *classified
			problem = &copy
		}
	}
	var result VerificationResult
	err = s.locked(ctx, func() error {
		latest, original, e := s.readCatalog()
		if e != nil {
			return e
		}
		old, e := replay(latest, operationID, hash)
		if e != nil {
			return e
		}
		if old != nil {
			result.Receipt = *old
			result.Verified = old.Verified != nil && *old.Verified
			return nil
		}
		if latest.Revision != revision {
			return ErrConflict
		}
		if p.Protocol == Subscription && s.subscriptionChanges.Load() > 0 {
			return ErrAccountChanging
		}
		if p.Protocol == Subscription {
			if err := s.ClaimSubscriptionOwnership(); err != nil {
				return err
			}
		}
		model := findModel(&latest, modelID)
		model.Verification = nil
		if passed {
			model.Verification = &Verification{ProviderVersion: p.Version, At: time.Now().UTC().Format(time.RFC3339Nano), Capabilities: caps}
		} else if latest.Settings.ActiveModelID == modelID {
			latest.Settings.Mode = "tool"
		}
		result.Receipt, e = s.commit(ctx, &latest, original, Receipt{OperationID: operationID, Digest: hash, ProviderID: p.ID, ModelID: modelID, Verified: &passed, Problem: problem}, !passed)
		result.Verified = passed
		return e
	})
	return result, err
}

type Lease struct {
	Context   context.Context
	Epoch     uint64
	ModelID   string
	store     *Store
	generated atomic.Bool
}

func (l *Lease) Close() {
	if l == nil {
		return
	}
	l.store.leaseMu.Lock()
	cancel := l.store.leases[l]
	delete(l.store.leases, l)
	l.store.leaseMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (l *Lease) Check() error {
	if err := l.Context.Err(); err != nil {
		return ErrStopped
	}
	state, err := l.store.Read(l.Context)
	if err != nil {
		return err
	}
	if state.Settings.Mode != "agent" || state.Settings.Epoch != l.Epoch || state.Settings.ActiveModelID != l.ModelID {
		return ErrStopped
	}
	return nil
}
func (s *Store) Begin(ctx context.Context) (*Lease, error) {
	var lease *Lease
	err := s.locked(ctx, func() error {
		c, _, err := s.readCatalog()
		if err != nil {
			return err
		}
		if c.Settings.Mode != "agent" || !s.armed.Load() {
			return ErrStopped
		}
		m := findModel(&c, c.Settings.ActiveModelID)
		if m == nil {
			return ErrUnverified
		}
		p := findProvider(&c, m.ProviderID)
		if p == nil {
			return ErrUnverified
		}
		if p.Protocol == Subscription && s.subscriptionChanges.Load() > 0 {
			return ErrAccountChanging
		}
		child, cancel := context.WithCancel(ctx)
		if p.Protocol == Subscription {
			if err := s.ClaimSubscriptionOwnership(); err != nil {
				cancel()
				return err
			}
		}
		lease = &Lease{Context: child, Epoch: c.Settings.Epoch, ModelID: c.Settings.ActiveModelID, store: s}
		s.leaseMu.Lock()
		s.leases[lease] = cancel
		s.leaseMu.Unlock()
		return nil
	})
	return lease, err
}
func (s *Store) stopLeases() {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	for lease, cancel := range s.leases {
		cancel()
		delete(s.leases, lease)
	}
}
func (s *Store) Close() {
	s.closed.Store(true)
	s.armed.Store(false)
	s.stopLeases()
	s.subscriptionUse.Lock()
	defer s.subscriptionUse.Unlock()
	s.subscriptionOwnerMu.Lock()
	defer s.subscriptionOwnerMu.Unlock()
	// Unfinished account mutation must not let another process take ownership.
	// The desktop joins its helper and finishes gates before calling Close.
	if s.subscriptionOwner != nil && s.subscriptionChanges.Load() == 0 {
		unlockFile(s.subscriptionOwner)
		s.subscriptionOwner = nil
	}
}
