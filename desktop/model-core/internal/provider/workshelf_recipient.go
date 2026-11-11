package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Public contract mirrored by WorkShelf internal/models.SubscriptionRecipient.
// ProofID is fresh per successful probe; no credential fingerprint is exposed.
type WorkShelfRecipient struct {
	Schema       int    `json:"schema"`
	ProofID      string `json:"proofID"`
	Agent        string `json:"agent"`
	AccountID    string `json:"accountID"`
	AccountLabel string `json:"accountLabel"`
	ModelID      string `json:"modelID"`
	Endpoint     string `json:"endpoint"`
	WireModel    string `json:"wireModel"`
}

func WorkShelfModel(model string) (string, string, bool) {
	agent, name, ok := strings.Cut(model, "/")
	return agent, name, ok && UXQASupported(agent) && name != "" && len(name) <= 256 && !strings.ContainsAny(name, "/\\:@") && !strings.ContainsFunc(name, unicode.IsSpace) && !strings.ContainsFunc(name, unicode.IsControl) && !strings.EqualFold(name, "auto")
}
func (r WorkShelfRecipient) Valid() bool {
	id := func(v string) bool {
		b, e := hex.DecodeString(v)
		return e == nil && len(b) == 32 && strings.ToLower(v) == v
	}
	a, _, ok := WorkShelfModel(r.ModelID)
	u, err := url.Parse(r.Endpoint)
	_, _, wireOK := WorkShelfModel(r.Agent + "/" + r.WireModel)
	return r.Schema == 1 && id(r.ProofID) && id(r.AccountID) && ok && a == r.Agent && wireOK && err == nil && WorkShelfDestination(r.Agent, u) && r.AccountLabel != "" && len([]rune(r.AccountLabel)) <= 120 && !strings.ContainsFunc(r.AccountLabel, unicode.IsControl)
}

// This digest stays inside the provider/helper. Credential rotation, same-name
// reauthorization, selected project/tenant, plan or account identity changes
// invalidate the probe. Unknown principal continuity requires a new test.
func workShelfSavedGeneration(l savedLogin) string {
	data, err := json.Marshal(struct {
		ID, Agent, User, Plan, SKU, Project, Seen string
		Auth, Profile                             json.RawMessage
	}{loginID(l), l.Agent, l.User, l.Plan, l.AccessSKU, l.Project, l.Seen.String(), l.Auth, l.Profile})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func workShelfSavedID(l savedLogin) string {
	sum := sha256.Sum256([]byte("workshelf-account-v1\x00" + l.Agent + "\x00" + loginID(l)))
	return hex.EncodeToString(sum[:])
}
func WorkShelfAccountSeal(p Provider, key []byte) (string, error) {
	if len(key) != 32 || p.Account == nil || p.Account.workShelfGeneration == "" || p.Account.workShelfID == "" {
		return "", errors.New("订阅账号代次无法核实")
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(p.Account.workShelfGeneration))
	return hex.EncodeToString(h.Sum(nil)), nil
}
func WorkShelfAccountIdentity(p Provider) (string, string, error) {
	if p.Account == nil || p.Account.workShelfID == "" {
		return "", "", errors.New("订阅账号身份无法核实")
	}
	name := []rune(p.Account.User)
	masked := "••••"
	if len(name) > 4 {
		masked = string(name[:2]) + "••••" + string(name[len(name)-2:])
	}
	return p.Account.workShelfID, p.Name + " · " + masked, nil
}
