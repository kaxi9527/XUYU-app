package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/organize"
)

func executionFixture(t *testing.T) (*organizePlans, config.Config, *organize.Plan, string) {
	t.Helper()
	h := t.TempDir()
	cfg := config.Default(h)
	src := filepath.Join(h, "brief.md")
	if err := os.WriteFile(src, []byte("requirements"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &organizePlans{}
	plan, err := store.preview(cfg, h, organize.Request{Project: "设计项目", Sources: []string{src}, WorkTypes: []string{"设计·UI原型"}})
	if err != nil || len(plan.Rows) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}
	return store, cfg, plan, src
}

func TestOrganizeExecutionReplaysReceiptWithoutMovingRestoredFiles(t *testing.T) {
	store, cfg, plan, src := executionFixture(t)
	ids := []string{plan.Rows[0].ID}
	calls := 0
	execute := func(_ context.Context, p *organize.Plan, selected []string) (*home.Summary, error) {
		calls++
		return p.Apply(cfg, selected)
	}
	first, err := store.apply(cfg, plan.Token, ids, execute)
	if err != nil || first.Moved != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := store.apply(cfg, plan.Token, append(ids, ids...), execute)
	if err != nil || second.UndoPath != first.UndoPath || calls != 1 {
		t.Fatal("retry did not replay receipt", err, calls)
	}
	undo, err := home.Undo(cfg, first.UndoPath)
	if err != nil || undo.Restored != 1 {
		t.Fatalf("undo=%+v err=%v", undo, err)
	}
	if _, err = store.apply(cfg, plan.Token, ids, execute); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src)
	if err != nil || string(b) != "requirements" || calls != 1 {
		t.Fatal("late retry changed restored source", err, calls)
	}
}

func TestOrganizeExecutionRejectsChangedSelectionAndConfiguration(t *testing.T) {
	store, cfg, plan, _ := executionFixture(t)
	selected := []string{plan.Rows[0].ID}
	execute := func(_ context.Context, p *organize.Plan, ids []string) (*home.Summary, error) {
		return p.Apply(cfg, ids)
	}
	changed := cfg
	changed.Root = filepath.Join(filepath.Dir(cfg.Root), "Other")
	if _, err := store.apply(changed, plan.Token, selected, execute); err == nil {
		t.Fatal("unconfirmed configuration accepted")
	}
	if _, err := store.apply(cfg, plan.Token, selected, execute); err != nil {
		t.Fatal(err)
	}
	if _, err := store.apply(cfg, plan.Token, []string{"another-file"}, execute); err == nil {
		t.Fatal("changed selection accepted")
	}
	if _, err := store.apply(changed, plan.Token, selected, execute); err == nil {
		t.Fatal("changed config accepted on replay")
	}
}

func TestOrganizeExecutionConcurrentRetryRunsOnce(t *testing.T) {
	store, cfg, plan, _ := executionFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	execute := func(_ context.Context, p *organize.Plan, ids []string) (*home.Summary, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return p.Apply(cfg, ids)
	}
	var wg sync.WaitGroup
	results := make(chan *home.Summary, 2)
	errs := make(chan error, 2)
	request := func() {
		defer wg.Done()
		r, e := store.apply(cfg, plan.Token, []string{plan.Rows[0].ID}, execute)
		results <- r
		errs <- e
	}
	wg.Add(1)
	go request()
	<-started
	wg.Add(1)
	go request()
	close(release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	path := ""
	for r := range results {
		if r == nil || r.Moved != 1 {
			t.Fatalf("%+v", r)
		}
		if path != "" && path != r.UndoPath {
			t.Fatal("different receipts")
		}
		path = r.UndoPath
	}
	if calls.Load() != 1 {
		t.Fatal("executed twice")
	}
}

func TestOrganizeExecutionStopReturnsReceiptAndKeepsUnstartedFiles(t *testing.T) {
	store, cfg, plan, src := executionFixture(t)
	if store.stop("unknown") {
		t.Fatal("unknown run stopped")
	}
	started := make(chan struct{})
	result := make(chan *home.Summary, 1)
	errs := make(chan error, 1)
	go func() {
		r, e := store.apply(cfg, plan.Token, []string{plan.Rows[0].ID}, func(ctx context.Context, p *organize.Plan, ids []string) (*home.Summary, error) {
			close(started)
			<-ctx.Done()
			return p.ApplyContext(ctx, cfg, ids)
		})
		result <- r
		errs <- e
	}()
	<-started
	if !store.stop(plan.Token) {
		t.Fatal("active run did not accept stop")
	}
	r, err := <-result, <-errs
	if err != nil || r.Stopped != 1 || r.Moved != 0 || r.Failed != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = os.Stat(src); err != nil {
		t.Fatal("stopped before first move changed source", err)
	}
	if store.stop(plan.Token) {
		t.Fatal("completed run reported still running")
	}
}

func TestOrganizeExecutionRetainsFailureAndUnblocksAfterPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "panic"}[panics], func(t *testing.T) {
			store, cfg, plan, src := executionFixture(t)
			calls := 0
			execute := func(context.Context, *organize.Plan, []string) (*home.Summary, error) {
				calls++
				if panics {
					panic("fixture panic")
				}
				return nil, errors.New("fixture unavailable")
			}
			func() {
				defer func() {
					if panics && recover() == nil {
						t.Error("expected fixture panic")
					}
				}()
				_, _ = store.apply(cfg, plan.Token, []string{plan.Rows[0].ID}, execute)
			}()
			if _, err := store.apply(cfg, plan.Token, []string{plan.Rows[0].ID}, execute); err == nil {
				t.Fatal("failed operation silently retried")
			}
			if calls != 1 {
				t.Fatal("failed callback re-executed")
			}
			if _, err := os.Stat(src); err != nil {
				t.Fatal("failure changed fixture")
			}
		})
	}
}
