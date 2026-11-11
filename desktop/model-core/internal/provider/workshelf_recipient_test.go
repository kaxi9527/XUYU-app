package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkShelfAccountGenerationAndPublicIdentity(t *testing.T) {
	l := savedLogin{ID: "stable-fixture", Agent: "copilot", User: "fixture@example.test", Plan: "individual", Seen: time.Unix(1, 0), Auth: json.RawMessage(`{"token":"synthetic-private-token"}`), Project: "tenant-one"}
	base := workShelfSavedGeneration(l)
	id := workShelfSavedID(l)
	if base == "" || id == "" {
		t.Fatal("missing private generation")
	}
	for _, mutate := range []func(*savedLogin){func(x *savedLogin) { x.Auth = json.RawMessage(`{"token":"new-private-token"}`) }, func(x *savedLogin) { x.Seen = x.Seen.Add(time.Second) }, func(x *savedLogin) { x.Project = "tenant-two" }, func(x *savedLogin) { x.Profile = json.RawMessage(`{"account":"new"}`) }, func(x *savedLogin) { x.Plan = "business" }} {
		changed := l
		mutate(&changed)
		if workShelfSavedGeneration(changed) == base || workShelfSavedID(changed) != id {
			t.Fatal("same-id credential/tenant change not detected")
		}
	}
	p := Provider{Name: "Copilot", ID: "copilot", Account: &Account{Agent: l.Agent, User: l.User, workShelfGeneration: base, workShelfID: id}}
	publicID, label, e := WorkShelfAccountIdentity(p)
	if e != nil || publicID != id || strings.Contains(label, l.User) {
		t.Fatal("unmasked identity")
	}
	one, e := WorkShelfAccountSeal(p, []byte(strings.Repeat("a", 32)))
	if e != nil {
		t.Fatal(e)
	}
	two, e := WorkShelfAccountSeal(p, []byte(strings.Repeat("b", 32)))
	if e != nil || one == two || one == base {
		t.Fatal("seal not helper-private")
	}
	data, _ := json.Marshal(p.Account)
	for _, secret := range []string{base, one, "synthetic-private-token"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private generation serialized")
		}
	}
	l.Auth = json.RawMessage(`{invalid`)
	if workShelfSavedGeneration(l) != "" {
		t.Fatal("invalid account created usable generation")
	}
}
