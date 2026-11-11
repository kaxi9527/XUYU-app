package models

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

func TestConfirmedRecipientLeaseAfterRestartDoesNotArmOrRewrite(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	g := generatorFixture{func(context.Context, Target, *credentials.Secret, GenerationRequest) (GenerationResult, error) {
		calls.Add(1)
		return GenerationResult{Text: "fixture"}, nil
	}}
	s, v, _ := activeGenerator(t, g)
	lease, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := lease.Recipient()
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	s.Close()
	restarted, err := NewStore(s.directory, v, g)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	_, before, err := restarted.readCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Begin(ctx); !errors.Is(err, ErrStopped) {
		t.Fatal("restart armed ordinary calls", err)
	}
	for _, change := range []func(*Recipient){func(r *Recipient) { r.Epoch++ }, func(r *Recipient) { r.ProviderVersion++ }, func(r *Recipient) { r.BaseURL = "https://different.invalid/v1" }, func(r *Recipient) { r.ModelName += " changed" }, func(r *Recipient) { r.Protocol = Subscription }} {
		wrong := recipient
		change(&wrong)
		if l, e := restarted.BeginForConfirmedRecipient(ctx, wrong); e == nil {
			l.Close()
			t.Fatal("changed recipient authorized")
		}
	}
	resumed, err := restarted.BeginForConfirmedRecipient(ctx, recipient)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	in := GenerationRequest{Input: "confirmed batch fixture", MaxOutputTokens: 32}
	if _, err = resumed.Generate(in); err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.Generate(in); !errors.Is(err, ErrRequestConsumed) {
		t.Fatal("repeated resumed call", err)
	}
	if calls.Load() != 1 {
		t.Fatal("wrong calls", calls.Load())
	}
	if _, err = restarted.Begin(ctx); !errors.Is(err, ErrStopped) {
		t.Fatal("scoped lease armed ordinary calls", err)
	}
	c, after, err := restarted.readCatalog()
	if err != nil || !bytes.Equal(before, after) || restarted.armed.Load() {
		t.Fatal("resume changed catalog or session")
	}
	if _, err = restarted.SetMode(ctx, c.Revision, "switch-tool", "tool", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.BeginForConfirmedRecipient(ctx, recipient); err == nil {
		t.Fatal("resumed after mode change")
	}
}
