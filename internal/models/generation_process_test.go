package models

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

func TestGenerationStopsAfterSeparateProcessChangesMode(t *testing.T) {
	if directory := os.Getenv("WORKSHELF_GENERATION_STOP_TEST"); directory != "" {
		s, err := NewStore(directory, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		state, err := s.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetMode(context.Background(), state.Revision, "process-stop", "tool", ""); err != nil {
			t.Fatal(err)
		}
		return
	}
	entered := make(chan struct{})
	s, _, _ := activeGenerator(t, generatorFixture{func(ctx context.Context, _ Target, _ *credentials.Secret, _ GenerationRequest) (GenerationResult, error) {
		close(entered)
		<-ctx.Done()
		return GenerationResult{Text: "late result"}, nil
	}})
	lease, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	done := make(chan error, 1)
	go func() {
		result, err := lease.Generate(GenerationRequest{Input: "synthetic", MaxOutputTokens: 32})
		if result.Text != "" {
			t.Error("stale output published")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGenerationStopsAfterSeparateProcessChangesMode$")
	cmd.Env = append(os.Environ(), "WORKSHELF_GENERATION_STOP_TEST="+s.directory)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stopped request returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child mode change did not cancel request")
	}
}
