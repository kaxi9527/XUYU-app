package agentdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/agents"
)

type EnrollmentGrant struct {
	Agent   string `json:"agent"`
	RuleSHA string `json:"ruleSHA"`
}
type EnrollmentPolicy struct {
	Version uint64            `json:"version"`
	Enabled bool              `json:"enabled"`
	Grants  []EnrollmentGrant `json:"grants"`
	Ignored []string          `json:"ignored"`
}
type EnrollmentEvent struct {
	Intent agents.EnrollmentIntent `json:"intent"`
	Status string                  `json:"status"`
	Reason string                  `json:"reason,omitempty"`
	At     time.Time               `json:"at"`
}
type EnrollmentReview struct {
	Revision uint64                   `json:"revision"`
	Preview  agents.EnrollmentPreview `json:"preview"`
}

func knownEnrollmentAgent(agent string) bool {
	for _, a := range agents.AutomaticAgents() {
		if a == agent {
			return true
		}
	}
	return false
}
func (s *Store) EnrollmentPreview(ctx context.Context, agent string) (EnrollmentReview, error) {
	r, err := s.Read(ctx)
	if err != nil {
		return EnrollmentReview{}, err
	}
	if r.Revision == 0 {
		return EnrollmentReview{}, errors.New("先执行一次 Agent 检查")
	}
	p, err := agents.PrepareEnrollment(ctx, r.Scan.Home, agent)
	if err != nil {
		return EnrollmentReview{}, err
	}
	return EnrollmentReview{r.Revision, p.Preview()}, nil
}

func normalizedPolicy(enabled bool, allowed, ignored []string) (EnrollmentPolicy, error) {
	if enabled && !agents.AutomaticEnrollmentAvailable() {
		return EnrollmentPolicy{}, errors.New("当前构建未支持安全自动接入")
	}
	p := EnrollmentPolicy{Enabled: enabled, Grants: []EnrollmentGrant{}, Ignored: []string{}}
	seen := map[string]bool{}
	for _, a := range allowed {
		if !knownEnrollmentAgent(a) {
			return p, errors.New("该工具没有已核实的自动接入入口")
		}
		if !seen[a] {
			p.Grants = append(p.Grants, EnrollmentGrant{a, agents.EnrollmentRuleSHA(a)})
			seen[a] = true
		}
	}
	seen = map[string]bool{}
	for _, a := range ignored {
		if !knownEnrollmentAgent(a) {
			return p, errors.New("忽略列表中有未知自动接入工具")
		}
		if !seen[a] {
			p.Ignored = append(p.Ignored, a)
			seen[a] = true
		}
	}
	if enabled && len(p.Grants) == 0 {
		return p, errors.New("开启自动接入时需要指定工具范围")
	}
	sort.Slice(p.Grants, func(i, j int) bool { return p.Grants[i].Agent < p.Grants[j].Agent })
	sort.Strings(p.Ignored)
	return p, nil
}

// Grants bind exact rule content; later template updates cannot expand them.
func (s *Store) SetEnrollmentPolicy(ctx context.Context, revision uint64, enabled bool, allowed, ignored []string) (Receipt, error) {
	p, err := normalizedPolicy(enabled, allowed, ignored)
	if err != nil {
		return Receipt{}, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer release()
	r, _, err := s.latest(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if r.Revision == 0 {
		return Receipt{}, errors.New("先执行一次 Agent 检查")
	}
	if r.Policy != nil {
		p.Version = r.Policy.Version
		if reflect.DeepEqual(*r.Policy, p) {
			return r, nil
		}
	}
	if r.Revision != revision {
		return Receipt{}, errors.New("Agent 接入设置已变化，请重新读取")
	}
	p.Version++
	r.Policy = &p
	return s.recordEnrollment(ctx, r, "enrollment_policy", nil)
}

func (s *Store) recordEnrollment(ctx context.Context, r Receipt, kind string, event *EnrollmentEvent) (Receipt, error) {
	previous, b, err := s.latest(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if previous.Revision != r.Revision {
		return Receipt{}, errors.New("Agent 接入记录已变化")
	}
	r.Revision++
	if r.Revision == 0 {
		return Receipt{}, ErrCorrupt
	}
	r.PreviousSHA = hash(b)
	r.Event = kind
	r.RecordedAt = time.Now().UTC()
	r.Changes = []Change{}
	if event != nil {
		event.At = r.RecordedAt
		found := false
		for i, e := range r.Enrollments {
			if e.Intent.Agent == event.Intent.Agent {
				r.Enrollments[i] = *event
				found = true
				break
			}
		}
		if !found {
			r.Enrollments = append(r.Enrollments, *event)
		}
		sort.Slice(r.Enrollments, func(i, j int) bool { return r.Enrollments[i].Intent.Agent < r.Enrollments[j].Intent.Agent })
	}
	if err := validateEnrollmentReceipt(r); err != nil {
		return Receipt{}, err
	}
	if err := s.append(ctx, r); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

func previousEnrollment(r Receipt, agent string) (EnrollmentEvent, bool) {
	for _, e := range r.Enrollments {
		if e.Intent.Agent == agent {
			return e, true
		}
	}
	return EnrollmentEvent{}, false
}

func (s *Store) Enroll(ctx context.Context, revision uint64, agent, digest string) (Receipt, error) {
	if !knownEnrollmentAgent(agent) {
		return Receipt{}, errors.New("该工具需手动接入，未写入规则")
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer release()
	r, _, err := s.latest(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if r.Revision == 0 || r.Revision != revision {
		return Receipt{}, errors.New("接入预览已过期")
	}
	if last, ok := previousEnrollment(r, agent); ok && last.Status == "writing" {
		return Receipt{}, errors.New("上次接入结果待核对，请先恢复检查")
	}
	p, err := agents.PrepareEnrollment(ctx, r.Scan.Home, agent)
	if err != nil {
		return Receipt{}, err
	}
	if p.Preview().Digest != digest {
		return Receipt{}, agents.ErrEnrollmentChanged
	}
	return s.applyEnrollment(ctx, r, p)
}

func (s *Store) applyEnrollment(ctx context.Context, r Receipt, p *agents.EnrollmentPlan) (Receipt, error) {
	v := p.Preview()
	e := EnrollmentEvent{Intent: agents.EnrollmentIntent{Agent: v.Agent, Path: v.Path, BeforeSHA: v.BeforeSHA, AfterSHA: v.AfterSHA}, Status: "needs_review", Reason: v.Reason}
	if v.Status == "blocked" {
		return s.recordIfChanged(ctx, r, e)
	}
	if v.Status == "already_present" {
		e.Status = "rule_present_pending_load"
		return s.recordIfChanged(ctx, r, e)
	}
	backup := filepath.Join(s.directory, "rule-backups")
	if err := os.Mkdir(backup, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return Receipt{}, err
	}
	out, writeErr := agents.ApplyEnrollment(ctx, p, backup, func(i agents.EnrollmentIntent) error {
		e.Intent = i
		e.Status = "writing"
		e.Reason = ""
		var err error
		r, err = s.recordEnrollment(ctx, r, "enrollment_intent", &e)
		return err
	})
	if r.Revision == 0 {
		return Receipt{}, writeErr
	}
	if out.Path != "" {
		e.Intent = out.EnrollmentIntent
	}
	if writeErr != nil {
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		} // Keep durable intent for later read-only recovery.
		e.Status = "needs_review"
		e.Reason = "write_failed_or_changed"
		if e.Intent.AfterSHA != "" {
			if observed, err := agents.ObserveEnrollment(ctx, e.Intent); err == nil {
				e.Status = observed
			}
		}
	} else {
		e.Status = out.Status
		e.Reason = ""
	}
	return s.recordIfChanged(ctx, r, e)
}

func (s *Store) recordIfChanged(ctx context.Context, r Receipt, e EnrollmentEvent) (Receipt, error) {
	if old, ok := previousEnrollment(r, e.Intent.Agent); ok {
		old.At = time.Time{}
		e.At = time.Time{}
		if reflect.DeepEqual(old, e) {
			return r, nil
		}
	}
	return s.recordEnrollment(ctx, r, "enrollment_result", &e)
}

// Local rule delivery only: no messages, other-agent startup, or model calls.
func (s *Store) ReconcileEnrollment(ctx context.Context) (Receipt, error) {
	return s.reconcileEnrollment(ctx, true)
}

// RecoverEnrollment only reconciles unfinished intents; it cannot start a write.
func (s *Store) RecoverEnrollment(ctx context.Context) (Receipt, error) {
	return s.reconcileEnrollment(ctx, false)
}

func (s *Store) reconcileEnrollment(ctx context.Context, allowNew bool) (Receipt, error) {
	r, err := s.Read(ctx)
	if err != nil || r.Revision == 0 {
		return r, err
	}
	if !agents.AutomaticEnrollmentAvailable() {
		pending := r.Policy != nil && r.Policy.Enabled
		for _, e := range r.Enrollments {
			pending = pending || e.Status == "writing"
		}
		if pending {
			return r, errors.New("当前构建不能安全接入或核对未完成写入；扫描和原接入记录已保留")
		}
		return r, nil
	}
	// Release the registry lock after each file. A policy change can stop the
	// next item without waiting for an entire batch of client writes.
	for _, agent := range agents.AutomaticAgents() {
		if _, err = s.reconcileEnrollmentAgent(ctx, agent, allowNew); err != nil {
			return Receipt{}, err
		}
	}
	return s.Read(ctx)
}

func (s *Store) reconcileEnrollmentAgent(ctx context.Context, agent string, allowNew bool) (Receipt, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer release()
	r, _, err := s.latest(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if r.Revision == 0 {
		return r, nil
	}
	for _, e := range append([]EnrollmentEvent(nil), r.Enrollments...) {
		if e.Status != "writing" || e.Intent.Agent != agent {
			continue
		}
		state, err := agents.ObserveEnrollment(ctx, e.Intent)
		if err != nil {
			if ctx.Err() != nil {
				return Receipt{}, ctx.Err()
			}
			state = "needs_review"
		}
		e.Status = state
		e.Reason = "interrupted_write_reconciled"
		r, err = s.recordEnrollment(ctx, r, "enrollment_recovery", &e)
		if err != nil {
			return Receipt{}, err
		}
	}
	if !allowNew || r.Policy == nil || !r.Policy.Enabled {
		return r, nil
	}
	ignored := map[string]bool{}
	for _, a := range r.Policy.Ignored {
		ignored[a] = true
	}
	for _, grant := range r.Policy.Grants {
		if grant.Agent != agent {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		if ignored[grant.Agent] {
			continue
		}
		seen := false
		for _, c := range r.Scan.Candidates {
			seen = seen || c.ID == grant.Agent
		}
		if !seen {
			continue
		}
		last, hadLast := previousEnrollment(r, grant.Agent)
		if grant.RuleSHA != agents.EnrollmentRuleSHA(grant.Agent) {
			e := EnrollmentEvent{Intent: agents.EnrollmentIntent{Agent: grant.Agent}, Status: "needs_review", Reason: "policy_rule_changed"}
			r, err = s.recordIfChanged(ctx, r, e)
			if err != nil {
				return Receipt{}, err
			}
			continue
		}
		p, err := agents.PrepareEnrollment(ctx, r.Scan.Home, grant.Agent)
		if err != nil {
			return Receipt{}, err
		}
		v := p.Preview()
		if hadLast && (last.Status == "rule_written_pending_load" || last.Status == "rule_present_pending_load" || last.Status == "rule_changed_needs_review" || last.Status == "needs_review" && last.Intent.Backup != "") {
			if v.Status != "already_present" && last.Status != "rule_changed_needs_review" {
				last.Status = "rule_changed_needs_review"
				last.Reason = "previous_rule_removed_or_changed"
				r, err = s.recordIfChanged(ctx, r, last)
				if err != nil {
					return Receipt{}, err
				}
			}
			continue
		}
		r, err = s.applyEnrollment(ctx, r, p)
		if err != nil {
			return Receipt{}, err
		}
	}
	return r, nil
}

// Legacy schema-1 scan receipts remain readable and grant no write permissions.
func validateEnrollmentReceipt(r Receipt) error {
	if r.Policy != nil {
		if r.Policy.Version == 0 || len(r.Policy.Grants) > 3 || len(r.Policy.Ignored) > 3 {
			return ErrCorrupt
		}
		seen := map[string]bool{}
		for _, g := range r.Policy.Grants {
			if !knownEnrollmentAgent(g.Agent) || len(g.RuleSHA) != 64 || seen[g.Agent] {
				return ErrCorrupt
			}
			seen[g.Agent] = true
		}
		for _, a := range r.Policy.Ignored {
			if !knownEnrollmentAgent(a) {
				return ErrCorrupt
			}
		}
	}
	if len(r.Enrollments) > 3 {
		return ErrCorrupt
	}
	seen := map[string]bool{}
	valid := map[string]bool{"writing": true, "needs_review": true, "not_written": true, "rule_written_pending_load": true, "rule_present_pending_load": true, "rule_changed_needs_review": true}
	for _, e := range r.Enrollments {
		if !knownEnrollmentAgent(e.Intent.Agent) || seen[e.Intent.Agent] || e.At.IsZero() || !valid[e.Status] {
			return ErrCorrupt
		}
		seen[e.Intent.Agent] = true
		if e.Intent.Path != "" && !agents.EnrollmentPathAllowed(r.Scan.Home, e.Intent.Agent, e.Intent.Path) {
			return ErrCorrupt
		}
		if e.Status == "writing" && (e.Intent.Path == "" || len(e.Intent.AfterSHA) != 64 || e.Intent.Backup == "") {
			return ErrCorrupt
		}
	}
	return nil
}

func PolicyPreview(enabled bool, allowed, ignored []string) (json.RawMessage, error) {
	p, err := normalizedPolicy(enabled, allowed, ignored)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	return b, err
}
