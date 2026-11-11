package autoorganize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

type ModelConsent struct {
	Recipient   models.Recipient `json:"recipient"`
	ScopeSHA    string           `json:"scopeSHA"`
	GrantedAt   time.Time        `json:"grantedAt"`
	GrantDigest string           `json:"grantDigest,omitempty"`
}
type ModelAttempt struct {
	InputSHA string         `json:"inputSHA"`
	Status   string         `json:"status"`
	Decision *ModelDecision `json:"decision,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Model    string         `json:"model,omitempty"`
	Problem  string         `json:"problem,omitempty"`
	At       time.Time      `json:"at"`
}
type ModelState struct {
	Consent          *ModelConsent           `json:"consent,omitempty"`
	ObjectionConsent *ModelConsent           `json:"objectionConsent,omitempty"`
	Attempts         map[string]ModelAttempt `json:"attempts,omitempty"`
}

func SetObjectionModelConsent(cfg config.Config, consent *ModelConsent) error {
	_, err := locked(cfg, func() (Operation, error) {
		state, err := ReadModelState(cfg)
		if err != nil {
			return Operation{}, err
		}
		p, err := ReadPolicy(cfg)
		if err != nil {
			return Operation{}, err
		}
		if consent != nil && (p.ObjectionEnabledAt == "" || PolicyScopeSHA(p) != consent.ScopeSHA || consent.Recipient.RequireLiveProof() != nil) {
			return Operation{}, errors.New("会话授权或模型已变化，请重新确认")
		}
		state.ObjectionConsent = consent
		return Operation{}, writeModelState(cfg, state)
	})
	return err
}

func PolicyScopeSHA(p Policy) string { return sessionPolicyID(p) }

func ReadModelState(cfg config.Config) (ModelState, error) {
	var state ModelState
	b, err := os.ReadFile(filepath.Join(dir(cfg), "模型判断.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(b, &state); err != nil {
		return state, errors.New("模型判断记录无法读取")
	}
	return state, nil
}

func RefreshContinuousRecipient(cfg config.Config, old, fresh models.Recipient) error {
	if old.Subscription == nil || fresh.Subscription == nil {
		return errors.New("模型接收方无法恢复")
	}
	previous := old
	proof := *old.Subscription
	proof.ProofID = fresh.Subscription.ProofID
	previous.Subscription = &proof
	if previous.Digest() != fresh.Digest() || fresh.RequireLiveProof() != nil {
		return errors.New("模型或账号已变化，请重新确认")
	}
	_, err := locked(cfg, func() (Operation, error) {
		state, err := ReadModelState(cfg)
		if err != nil {
			return Operation{}, err
		}
		policy, err := ReadPolicy(cfg)
		if err != nil {
			return Operation{}, err
		}
		authorized := false
		for _, consent := range []*ModelConsent{state.Consent, state.ObjectionConsent} {
			if consent != nil && consent.Recipient.Digest() == old.Digest() && consent.ScopeSHA == PolicyScopeSHA(policy) {
				authorized = true
			}
		}
		if !authorized {
			return Operation{}, errors.New("模型授权已变化，请重新确认")
		}
		updated := false
		for _, consent := range []*ModelConsent{state.Consent, state.ObjectionConsent} {
			if consent == nil || consent.Recipient.Subscription == nil || consent.ScopeSHA != PolicyScopeSHA(policy) {
				continue
			}
			same := consent.Recipient
			currentProof := *same.Subscription
			currentProof.ProofID = fresh.Subscription.ProofID
			same.Subscription = &currentProof
			if same.Digest() != fresh.Digest() {
				continue
			}
			if consent.GrantDigest == "" {
				consent.GrantDigest = consent.Recipient.Digest()
			}
			consent.Recipient = fresh
			updated = true
		}
		if !updated {
			return Operation{}, errors.New("模型授权已变化，请重新确认")
		}
		return Operation{}, writeModelState(cfg, state)
	})
	return err
}
func writeModelFile(cfg config.Config, name string, b []byte) error {
	f, err := os.CreateTemp(dir(cfg), ".settings-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir(cfg), name))
	}
	return err
}
func writeModelState(cfg config.Config, state ModelState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeModelFile(cfg, "模型判断.json", b)
}
func SetModelConsent(cfg config.Config, consent *ModelConsent) error {
	_, err := locked(cfg, func() (Operation, error) {
		state, e := ReadModelState(cfg)
		if e != nil {
			return Operation{}, e
		}
		p, e := ReadPolicy(cfg)
		if e != nil {
			return Operation{}, e
		}
		if consent != nil && (!p.Enabled || PolicyScopeSHA(p) != consent.ScopeSHA || consent.Recipient.RequireLiveProof() != nil) {
			return Operation{}, errors.New("模型或整理范围已变化，请重新确认")
		}
		state.Consent = consent
		state.Attempts = nil
		return Operation{}, writeModelState(cfg, state)
	})
	return err
}

// BeginModelBatch persists each request before the network call, so a restart
// does not silently send the same uncertain request again.
func BeginModelBatch(cfg config.Config, consent ModelConsent, tasks []ModelTask, hashes map[string]string) ([]ModelTask, error) {
	var eligible []ModelTask
	_, err := locked(cfg, func() (Operation, error) {
		state, e := ReadModelState(cfg)
		if e != nil {
			return Operation{}, e
		}
		p, e := ReadPolicy(cfg)
		if e != nil {
			return Operation{}, e
		}
		if state.Consent == nil || state.Consent.Recipient.Digest() != consent.Recipient.Digest() || state.Consent.ScopeSHA != consent.ScopeSHA || !p.Enabled || PolicyScopeSHA(p) != consent.ScopeSHA {
			return Operation{}, errors.New("模型判断已关闭或授权范围已变化")
		}
		if state.Attempts == nil {
			state.Attempts = map[string]ModelAttempt{}
		}
		for _, task := range tasks {
			if old, ok := state.Attempts[task.ID]; ok && old.InputSHA == hashes[task.ID] {
				continue
			}
			eligible = append(eligible, task)
			state.Attempts[task.ID] = ModelAttempt{InputSHA: hashes[task.ID], Status: "pending", Provider: consent.Recipient.ProviderName, Model: consent.Recipient.ModelName, At: time.Now().UTC()}
			if len(eligible) == 20 {
				break
			}
		}
		if len(eligible) == 0 {
			return Operation{}, nil
		}
		return Operation{}, writeModelState(cfg, state)
	})
	return eligible, err
}

// FinishModelBatch stores the proposed decision and applies only still-current
// groups; a pre-existing user choice is never overwritten.
func FinishModelBatch(cfg config.Config, consent ModelConsent, tasks []ModelTask, hashes map[string]string, decisions []ModelDecision, problem string) error {
	_, err := locked(cfg, func() (Operation, error) {
		state, e := ReadModelState(cfg)
		if e != nil {
			return Operation{}, e
		}
		p, e := ReadPolicy(cfg)
		if e != nil {
			return Operation{}, e
		}
		if state.Consent == nil || state.Consent.Recipient.Digest() != consent.Recipient.Digest() || state.Consent.ScopeSHA != consent.ScopeSHA || !p.Enabled || PolicyScopeSHA(p) != consent.ScopeSHA {
			return Operation{}, errors.New("模型判断结果已过期")
		}
		byID := map[string]ModelDecision{}
		for _, d := range decisions {
			byID[d.ID] = d
		}
		if p.SessionProjects == nil {
			p.SessionProjects = map[string]string{}
		}
		if p.ProjectTrades == nil {
			p.ProjectTrades = map[string]string{}
		}
		for _, task := range tasks {
			a, ok := state.Attempts[task.ID]
			if !ok || a.InputSHA != hashes[task.ID] || a.Status != "pending" {
				continue
			}
			a.Status = "uncertain"
			a.Problem = problem
			if decision, ok := byID[task.ID]; ok && problem == "" {
				if applyModelDecision(&p, task, decision) {
					a.Status = "applied"
					copy := decision
					a.Decision = &copy
				} else {
					a.Status = "superseded"
				}
			}
			state.Attempts[task.ID] = a
		}
		// Persist the model's evidence before making its decision executable.
		// A later pass can replay it if the policy write is interrupted.
		if e = writeModelState(cfg, state); e != nil {
			return Operation{}, e
		}
		return Operation{}, writePolicy(cfg, p)
	})
	return err
}

func applyModelDecision(p *Policy, task ModelTask, decision ModelDecision) bool {
	if p.SessionProjects == nil {
		p.SessionProjects = map[string]string{}
	}
	if p.ProjectTrades == nil {
		p.ProjectTrades = map[string]string{}
	}
	if task.Kind == "session" && p.SessionProjects[task.ID] != "" {
		return false
	}
	if existing := p.ProjectTrades[decision.Project]; existing != "" && existing != decision.WorkType {
		return false
	}
	if task.Kind == "session" {
		p.SessionProjects[task.ID] = decision.Project
	}
	p.ProjectTrades[decision.Project] = decision.WorkType
	return true
}

func RecoverModelDecisions(cfg config.Config, consent ModelConsent, tasks []ModelTask, hashes map[string]string) error {
	_, err := locked(cfg, func() (Operation, error) {
		state, e := ReadModelState(cfg)
		if e != nil {
			return Operation{}, e
		}
		p, e := ReadPolicy(cfg)
		if e != nil {
			return Operation{}, e
		}
		if state.Consent == nil || state.Consent.Recipient.Digest() != consent.Recipient.Digest() || state.Consent.ScopeSHA != consent.ScopeSHA || !p.Enabled || PolicyScopeSHA(p) != consent.ScopeSHA {
			return Operation{}, errors.New("模型判断结果已过期")
		}
		changed := false
		for _, task := range tasks {
			a, ok := state.Attempts[task.ID]
			if ok && a.InputSHA == hashes[task.ID] && a.Status == "applied" && a.Decision != nil && applyModelDecision(&p, task, *a.Decision) {
				changed = true
			}
		}
		if !changed {
			return Operation{}, nil
		}
		return Operation{}, writePolicy(cfg, p)
	})
	return err
}
func RetryModelAttempts(cfg config.Config) error {
	_, err := locked(cfg, func() (Operation, error) {
		state, e := ReadModelState(cfg)
		if e != nil {
			return Operation{}, e
		}
		if state.Consent == nil {
			return Operation{}, nil
		}
		changed := false
		for id, attempt := range state.Attempts {
			if attempt.Status != "applied" && (attempt.Status != "pending" || time.Since(attempt.At) > time.Minute) {
				delete(state.Attempts, id)
				changed = true
			}
		}
		if !changed {
			return Operation{}, nil
		}
		return Operation{}, writeModelState(cfg, state)
	})
	return err
}
