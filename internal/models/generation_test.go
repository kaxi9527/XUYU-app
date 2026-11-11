package models

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

type generatorFixture struct {
	generate func(context.Context, Target, *credentials.Secret, GenerationRequest) (GenerationResult, error)
}

func (g generatorFixture) Verify(ctx context.Context, target Target, key *credentials.Secret) (Capabilities, error) {
	return passingVerifier(ctx, target, key)
}
func (g generatorFixture) Generate(ctx context.Context, target Target, key *credentials.Secret, in GenerationRequest) (GenerationResult, error) {
	return g.generate(ctx, target, key, in)
}
func activeGenerator(t *testing.T, g generatorFixture) (*Store, *memoryVault, Receipt) {
	t.Helper()
	s, v := fixture(t)
	s.verifier = g
	p := prepare(t, s, 0, "", "", "synthetic-key")
	saved := save(t, s, p, "saved")
	verified, err := s.VerifyModel(context.Background(), saved.Revision, "verified", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.SetMode(context.Background(), verified.Receipt.Revision, "active", "agent", saved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	return s, v, active
}
func TestLeaseGeneratesOnceAndDestroysBorrowedKey(t *testing.T) {
	var calls atomic.Int32
	var borrowed *credentials.Secret
	s, _, _ := activeGenerator(t, generatorFixture{func(ctx context.Context, target Target, key *credentials.Secret, in GenerationRequest) (GenerationResult, error) {
		calls.Add(1)
		borrowed = key
		if in.Input != "context" || target.ModelID != "test-model" {
			t.Error("wrong request")
		}
		return GenerationResult{Text: "suggestion"}, nil
	}})
	l, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	in := GenerationRequest{Input: "context", MaxOutputTokens: 32}
	result, err := l.Generate(in)
	if err != nil || result.Text != "suggestion" {
		t.Fatal(err)
	}
	if _, err = l.Generate(in); !errors.Is(err, ErrRequestConsumed) || calls.Load() != 1 {
		t.Fatal("duplicate request sent", err)
	}
	if bytes, err := borrowed.Bytes(); err == nil {
		credentials.Wipe(bytes)
		t.Fatal("key retained")
	}
}
func TestGenerationStopsForModeCloseCallerAndOtherStore(t *testing.T) {
	for _, kind := range []string{"mode", "close", "caller", "other_store"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			s, v, active := activeGenerator(t, generatorFixture{func(ctx context.Context, _ Target, _ *credentials.Secret, _ GenerationRequest) (GenerationResult, error) {
				close(entered)
				<-ctx.Done()
				return GenerationResult{Text: "late success must be discarded"}, nil
			}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l, err := s.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			done := make(chan error, 1)
			go func() {
				r, e := l.Generate(GenerationRequest{Input: "context", MaxOutputTokens: 32})
				if r.Text != "" {
					t.Error("late result published")
				}
				done <- e
			}()
			<-entered
			switch kind {
			case "mode":
				_, err = s.SetMode(context.Background(), active.Revision, "stop", "tool", "")
			case "close":
				s.Close()
			case "caller":
				cancel()
			case "other_store":
				other, e := NewStore(s.directory, v, verifierFunc(passingVerifier))
				if e != nil {
					t.Fatal(e)
				}
				defer other.Close()
				_, err = other.SetMode(context.Background(), active.Revision, "remote-stop", "tool", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if err == nil {
					t.Fatal("stop ignored")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("in-flight request not cancelled")
			}
		})
	}
}
