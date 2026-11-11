package models

import (
	"math/big"
	"regexp"
	"strings"
)

// OpenRouterDebit is the account debit reported in one provider response, in
// OpenRouter credits. It is not a quote, invoice, USD total or spending cap.
// BYOK nil means the response did not identify the upstream billing mode.
type OpenRouterDebit struct {
	Amount string `json:"amount"`
	BYOK   *bool  `json:"byok,omitempty"`
}

var debitNumber = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})(\.[0-9]{1,18})?([eE][+-]?[0-9]{1,2})?$`)

// NormalizeDebitAmount never rounds or passes money through floating point.
func NormalizeDebitAmount(raw string) (string, bool) {
	if len(raw) > 64 || !debitNumber.MatchString(raw) {
		return "", false
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok || r.Sign() < 0 || r.Cmp(big.NewRat(1_000_000_000, 1)) > 0 {
		return "", false
	}
	s := r.FloatString(18)
	check, _ := new(big.Rat).SetString(s)
	if check.Cmp(r) != 0 {
		return "", false
	}
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s, true
}

func SupportsOpenRouterDebit(protocol Protocol, baseURL string) bool {
	return (protocol == Chat || protocol == Responses) && strings.TrimSuffix(baseURL, "/") == "https://openrouter.ai/api/v1"
}

func (u Usage) ValidFor(recipient Recipient) bool {
	return u.Valid() && (u.OpenRouterDebit == nil || SupportsOpenRouterDebit(recipient.Protocol, recipient.BaseURL))
}

func (d *OpenRouterDebit) valid() bool {
	if d == nil {
		return true
	}
	normalized, ok := NormalizeDebitAmount(d.Amount)
	return ok && normalized == d.Amount
}
