package models

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

var ErrCredentialScope = errors.New("连接地址、协议或鉴权头已改变，请重新输入 API Key，或明确移除凭据")
var ErrDraftExpired = errors.New("连接草稿已失效，请重新填写并测试")

// Draft never persists secrets or configuration. Its only mutable selection is
// the model; changing the destination requires a new draft and scope check.
type Draft struct {
	mu            sync.Mutex
	owner         *Store
	prepared      *Prepared
	explicitModel bool
	expires       time.Time
	proof         *draftProof
}
type draftProof struct {
	id           string
	at           time.Time
	expires      time.Time
	capabilities Capabilities
}
type DraftVerification struct {
	Verified bool               `json:"verified"`
	Problem  *ConnectionProblem `json:"problem,omitempty"`
}

func (s *Store) PrepareDraft(ctx context.Context, revision uint64, in ConnectionInput) (*Draft, error) {
	if in.Protocol == Subscription {
		return nil, errors.New("订阅连接请通过账号授权选择模型")
	}
	p, err := s.prepare(ctx, revision, in, true)
	if err != nil {
		return nil, err
	}
	return &Draft{owner: s, prepared: p, explicitModel: in.ModelRecordID != "", expires: time.Now().Add(5 * time.Minute)}, nil
}
func (d *Draft) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.prepared != nil {
		d.prepared.Close()
		d.prepared = nil
	}
	d.proof = nil
}
func (d *Draft) Preview() (ConnectionPreview, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.catalog(context.Background()); err != nil {
		return ConnectionPreview{}, false, err
	}
	return d.prepared.Preview(), d.proof != nil, nil
}
func (d *Draft) valid() error {
	if d == nil || d.prepared == nil || time.Now().After(d.expires) {
		return ErrDraftExpired
	}
	if d.owner.closed.Load() {
		return ErrStopped
	}
	return nil
}
func (d *Draft) catalog(ctx context.Context) (catalog, error) {
	if err := ctx.Err(); err != nil {
		return catalog{}, err
	}
	if err := d.valid(); err != nil {
		return catalog{}, err
	}
	c, _, err := d.owner.readCatalog()
	if err != nil {
		return c, err
	}
	if c.Revision != d.prepared.revision {
		return c, ErrConflict
	}
	return c, nil
}
func (d *Draft) SelectModel(ctx context.Context, id, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.catalog(ctx)
	if err != nil {
		return err
	}
	if !label(id, 256) {
		return errors.New("请填写有效的模型 ID")
	}
	if name == "" {
		runes := []rune(id)
		if len(runes) > 120 {
			runes = runes[:120]
		}
		name = string(runes)
	}
	if !label(name, 120) {
		return errors.New("模型显示名称无效")
	}
	p := d.prepared
	if id == p.upstreamModel && name == p.modelName {
		return nil
	}
	if !d.explicitModel {
		record := ""
		for _, m := range c.Models {
			if m.ProviderID == p.providerID && m.ModelID == id {
				record = m.ID
				break
			}
		}
		if record == "" {
			record, err = identifier("model_")
			if err != nil {
				return err
			}
		}
		p.modelID = record
	}
	p.upstreamModel, p.modelName = id, name
	d.proof = nil
	return nil
}

// Called with d.mu held. A saved key is read only for the identical receiver;
// prepare already enforces this, and this check defends every outbound request.
func (d *Draft) secret(ctx context.Context, c catalog) (*credentials.Secret, func(), error) {
	p := d.prepared
	if p.secret != nil {
		return p.secret, func() {}, nil
	}
	old := findProvider(&c, p.providerID)
	if p.removeSecret || old == nil || old.SecretRef == "" {
		return nil, func() {}, nil
	}
	options, err := NormalizeOptions(old.Protocol, old.Options)
	if err != nil || old.Protocol != p.protocol || old.BaseURL != p.baseURL || options.AuthHeader != p.options.AuthHeader {
		return nil, nil, ErrCredentialScope
	}
	if d.owner.vault == nil {
		return nil, nil, credentials.ErrUnavailable
	}
	secret, err := d.owner.vault.Read(ctx, old.SecretRef)
	if err != nil {
		return nil, nil, err
	}
	return secret, secret.Destroy, nil
}
func (d *Draft) Discover(ctx context.Context, cursor string) (ModelPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.catalog(ctx)
	if err != nil {
		return ModelPage{}, err
	}
	if len(cursor) > 2048 {
		return ModelPage{}, &ConnectionProblem{Code: "invalid_request"}
	}
	discoverer, ok := d.owner.verifier.(Discoverer)
	if !ok {
		return ModelPage{}, &ConnectionProblem{Code: "list_unsupported"}
	}
	secret, release, err := d.secret(ctx, c)
	if err != nil {
		return ModelPage{}, err
	}
	defer release()
	p := d.prepared
	page, err := discoverer.ListModels(ctx, Target{Protocol: p.protocol, BaseURL: p.baseURL, Options: p.options}, secret, cursor)
	if err != nil {
		return ModelPage{}, err
	}
	if _, err = d.catalog(ctx); err != nil {
		return ModelPage{}, err
	}
	return page, nil
}
func (d *Draft) Verify(ctx context.Context) (DraftVerification, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.proof = nil
	c, err := d.catalog(ctx)
	if err != nil {
		return DraftVerification{}, err
	}
	p := d.prepared
	if !label(p.upstreamModel, 256) {
		return DraftVerification{}, errors.New("请先选择或填写模型 ID")
	}
	if d.owner.verifier == nil {
		return DraftVerification{}, errors.New("此版本尚未配置模型协议适配器")
	}
	secret, release, err := d.secret(ctx, c)
	if err != nil {
		return DraftVerification{}, err
	}
	defer release()
	caps, probeErr := d.owner.verifier.Verify(ctx, Target{Protocol: p.protocol, BaseURL: p.baseURL, ModelID: p.upstreamModel, Options: p.options}, secret)
	if _, err = d.catalog(ctx); err != nil {
		return DraftVerification{}, err
	}
	if probeErr != nil || !validCapabilities(caps) || caps.Text != "supported" {
		problem := &ConnectionProblem{Code: "verification_failed"}
		var classified *ConnectionProblem
		if errors.As(probeErr, &classified) && classified != nil && classified.Valid() {
			v := *classified
			problem = &v
		}
		return DraftVerification{Problem: problem}, nil
	}
	id, err := identifier("proof_")
	if err != nil {
		return DraftVerification{}, err
	}
	d.proof = &draftProof{id: id, at: time.Now(), expires: d.expires, capabilities: caps}
	return DraftVerification{Verified: true}, nil
}
func (s *Store) SaveVerifiedDraft(ctx context.Context, d *Draft, operationID string, activate bool) (Receipt, error) {
	if d == nil {
		return Receipt{}, ErrDraftExpired
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.valid(); err != nil {
		return Receipt{}, err
	}
	if d.owner != s {
		return Receipt{}, ErrConflict
	}
	if d.proof == nil {
		return Receipt{}, ErrUnverified
	}
	return s.save(ctx, d.prepared, operationID, d.proof, activate)
}
