package models

import (
	"context"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// SubscriptionRecipient is non-secret evidence from an explicit fixed-content
// test in the current helper. It is not permission to send project content.
// AccountID identifies the account, never a credential hash. ProofID binds its
// helper-private credential generation to this particular successful test.
type SubscriptionRecipient struct {
	Schema       int    `json:"schema"`
	ProofID      string `json:"proofID"`
	Agent        string `json:"agent"`
	AccountID    string `json:"accountID"`
	AccountLabel string `json:"accountLabel"`
	ModelID      string `json:"modelID"`
	Endpoint     string `json:"endpoint"`
	WireModel    string `json:"wireModel"`
}

func (r SubscriptionRecipient) Valid() bool {
	hexID := func(v string) bool {
		b, err := hex.DecodeString(v)
		return err == nil && len(b) == 32 && strings.ToLower(v) == v
	}
	u, err := url.Parse(r.Endpoint)
	if r.Schema != 1 || !hexID(r.ProofID) || !hexID(r.AccountID) || !label(r.AccountLabel, 120) || !label(r.WireModel, 256) || strings.ContainsAny(r.WireModel, "/\\:@") || strings.ContainsFunc(r.WireModel, unicode.IsSpace) || strings.EqualFold(r.WireModel, "auto") || err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.ForceQuery || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	agent, model, ok := strings.Cut(r.ModelID, "/")
	if !ok || agent != r.Agent || !label(model, 256) || strings.ContainsAny(model, "/\\:@") || strings.ContainsFunc(model, unicode.IsSpace) || strings.EqualFold(model, "auto") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch r.Agent {
	case "codex":
		return host == "chatgpt.com" && u.Path == "/backend-api/codex/responses" && u.RawQuery == ""
	case "copilot":
		return u.RawQuery == "" && slices.Contains([]string{"api.githubcopilot.com", "api.individual.githubcopilot.com", "api.business.githubcopilot.com", "api.enterprise.githubcopilot.com"}, host) && slices.Contains([]string{"/chat/completions", "/responses", "/v1/messages"}, u.Path)
	case "gemini", "antigravity":
		return slices.Contains([]string{"cloudcode-pa.googleapis.com", "daily-cloudcode-pa.googleapis.com"}, host) && (u.Path == "/v1internal:generateContent" && u.RawQuery == "" || u.Path == "/v1internal:streamGenerateContent" && u.RawQuery == "alt=sse")
	}
	return false
}

func (r Recipient) RequireLiveProof() error {
	if !r.Valid() {
		return ErrUnverified
	}
	if r.Protocol == Subscription && (r.Subscription == nil || !r.Subscription.Valid()) {
		return &ConnectionProblem{Code: "recipient_unverified"}
	}
	return nil
}

// SubscriptionResolver only reads an already-tested helper receipt. Implementors
// must not probe a model or refresh credentials while resolving a preview.
type SubscriptionResolver interface {
	SubscriptionRecipient(context.Context, Target) (SubscriptionRecipient, error)
}
