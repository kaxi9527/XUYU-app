package models

import (
	"encoding/json"
	"testing"
)

func TestDebitExactDecimalsAndLegacyJSON(t *testing.T) {
	for input, want := range map[string]string{"0": "0", "0.0000": "0", "1.2300": "1.23", "1e-18": "0.000000000000000001", "123e-2": "1.23", "1E+9": "1000000000", "0.123456789012345678": "0.123456789012345678"} {
		got, ok := NormalizeDebitAmount(input)
		if !ok || got != want {
			t.Fatalf("%s: %q %v", input, got, ok)
		}
	}
	for _, input := range []string{"", "null", `"0.1"`, "-0", "-1", "NaN", "Inf", "1e-19", "1e99", "1e999999", "1000000000.1", "0.0000000000000000001", "01", "1/3", " 1"} {
		if _, ok := NormalizeDebitAmount(input); ok {
			t.Fatalf("accepted %q", input)
		}
	}
	data, err := json.Marshal(Usage{})
	if err != nil || string(data) != "{}" {
		t.Fatalf("legacy JSON drift %s %v", data, err)
	}
	u := Usage{OpenRouterDebit: &OpenRouterDebit{Amount: "0.1"}}
	if !u.ValidFor(Recipient{Protocol: Chat, BaseURL: "https://openrouter.ai/api/v1"}) {
		t.Fatal("official source rejected")
	}
	if u.ValidFor(Recipient{Protocol: Chat, BaseURL: "https://openrouter.ai.attacker.invalid/api/v1"}) {
		t.Fatal("foreign source accepted")
	}
	u.OpenRouterDebit.Amount = "0.10"
	if u.Valid() {
		t.Fatal("noncanonical receipt accepted")
	}
}
