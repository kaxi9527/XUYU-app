package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

func analysisSpecFixture(t *testing.T, s *Store) AnalysisSpec {
	t.Helper()
	v := readMemory(t, s)
	sha := strings.Repeat("a", 64)
	input, _ := json.Marshal(map[string]any{"sha256": sha, "body": map[string]any{"projectID": s.project.ID, "memoryRevision": v.State.Revision, "memoryHead": v.HeadSHA}})
	return AnalysisSpec{PreviewID: "preview", ProjectID: s.project.ID, ContextSHA: sha, MemoryRevision: v.State.Revision, MemoryHead: v.HeadSHA, UnknownCostAccepted: true, Recipient: models.Recipient{Epoch: 1, ProviderID: "provider", ProviderVersion: 1, ProviderName: "Fixture", Protocol: models.Chat, BaseURL: "https://fixture.invalid/v1", ModelRecordID: "model", ModelID: "fixture", ModelName: "Fixture"}, Request: models.GenerationRequest{System: "fixture", Input: string(input), MaxOutputTokens: AnalysisOutputLimit}}
}

func TestAnalysisPublicationChecksAfterProjectLock(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx := context.Background()
	spec := analysisSpecFixture(t, s)
	r, _, err := s.ReserveAnalysis(ctx, "publication-lock", spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	unlock := func() { once.Do(release) }
	defer unlock()
	entered := make(chan struct{})
	checked := make(chan struct{}, 1)
	done := make(chan error, 1)
	var changed atomic.Bool
	go func() {
		close(entered)
		out, e := s.FinishAnalysisChecked(ctx, r.ID, r.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, func() error {
			checked <- struct{}{}
			if changed.Load() {
				return ErrConflict
			}
			return nil
		})
		if e == nil && (out.Status != "stale" || len(out.Advice) != 0) {
			e = errors.New("published stale external input")
		}
		done <- e
	}()
	<-entered
	select {
	case <-checked:
		t.Fatal("checked external inputs before acquiring project lock")
	case <-time.After(40 * time.Millisecond):
	}
	changed.Store(true)
	unlock()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication blocked")
	}
	select {
	case <-checked:
	default:
		t.Fatal("external inputs were never checked")
	}
}
func TestAnalysisConsentReservationReplayAndUnknown(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	spec := analysisSpecFixture(t, s)
	ctx := context.Background()
	if _, err := s.Analysis(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.directory, "analysis.json")); !os.IsNotExist(err) {
		t.Fatal("read created a ledger")
	}
	for _, kind := range []string{"consent", "cost", "project", "packet"} {
		bad := spec
		switch kind {
		case "consent":
			bad.UnknownCostAccepted = false
		case "cost":
			bad.StrictCostLimit = true
		case "project":
			bad.ProjectID = "another"
		case "packet":
			bad.ContextSHA = strings.Repeat("b", 64)
		}
		if _, _, err := s.ReserveAnalysis(ctx, "bad", bad, testNow); err == nil {
			t.Fatal("invalid consent reserved", kind)
		}
	}
	r, fresh, err := s.ReserveAnalysis(ctx, "first", spec, testNow)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	if r.EffectiveStatus(testNow.Add(time.Minute)) != "unknown" {
		t.Fatal("lost request appeared safe to resend")
	}
	again, fresh, err := s.ReserveAnalysis(ctx, "first", spec, testNow.Add(time.Minute))
	if err != nil || fresh || again.SpecSHA != r.SpecSHA {
		t.Fatal("replayed request reserved again")
	}
	bad := spec
	bad.Recipient.ModelID = "another"
	if _, _, err = s.ReserveAnalysis(ctx, "first", bad, testNow); !errors.Is(err, ErrOperationReused) {
		t.Fatal("ID reused for another receiver", err)
	}
	finished, err := s.FinishAnalysis(ctx, "first", r.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[],"uncertainIDs":[]}`), models.Usage{}, testNow.Add(time.Minute))
	if err != nil || finished.Status != "unknown" || len(finished.Advice) != 0 {
		t.Fatal("late result published", err)
	}
	l, err := s.Analysis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := l.Budget(testNow.Add(time.Minute))
	if err != nil || budget.Reserved.Requests != 1 || budget.Reserved.OutputTokens != AnalysisOutputLimit || budget.CostStatus != "unknown" {
		t.Fatal("unknown result refunded quota")
	}
}

func TestAnalysisBudgetAcrossStoresAndProcesses(t *testing.T) {
	if path := os.Getenv("WORKSHELF_ANALYSIS_BUDGET_FIXTURE"); path != "" {
		var in struct {
			Base    string
			Project Project
			Spec    AnalysisSpec
			ID      string
			Result  string
		}
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, &in) != nil {
			t.Fatal("fixture")
		}
		s, err := NewStore(in.Base, in.Project)
		if err != nil {
			t.Fatal(err)
		}
		_, fresh, err := s.ReserveAnalysis(context.Background(), in.ID, in.Spec, testNow)
		if err != nil && !errors.Is(err, ErrAnalysisBudget) {
			t.Fatal(err)
		}
		out, _ := json.Marshal(map[string]bool{"fresh": fresh, "budget": errors.Is(err, ErrAnalysisBudget)})
		if err = os.WriteFile(in.Result, out, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	spec := analysisSpecFixture(t, s)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other, err := NewStore(filepath.Dir(s.directory), s.project)
			if err != nil {
				t.Error(err)
				return
			}
			_, fresh, err := other.ReserveAnalysis(context.Background(), fmt.Sprint("store-", i), spec, testNow)
			if err != nil {
				t.Error(err)
			}
			if fresh {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 9 {
		t.Fatal("lost reservations")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	commands := []*exec.Cmd{}
	outputs := []string{}
	for i := 0; i < 2; i++ {
		path := filepath.Join(t.TempDir(), "fixture.json")
		output := path + ".result"
		in := map[string]any{"Base": filepath.Dir(s.directory), "Project": s.project, "Spec": spec, "ID": fmt.Sprint("process-", i), "Result": output}
		b, _ := json.Marshal(in)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAnalysisBudgetAcrossStoresAndProcesses$")
		cmd.Env = append(os.Environ(), "WORKSHELF_ANALYSIS_BUDGET_FIXTURE="+path)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
		outputs = append(outputs, output)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	created, exhausted := 0, 0
	for _, path := range outputs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]bool
		if json.Unmarshal(b, &out) != nil {
			t.Fatal("receipt")
		}
		if out["fresh"] {
			created++
		}
		if out["budget"] {
			exhausted++
		}
	}
	if created != 1 || exhausted != 1 {
		t.Fatal("cross-process quota was overspent", created, exhausted)
	}
	l, err := s.Analysis(context.Background())
	if err != nil || len(l.Requests) != 10 {
		t.Fatal(err)
	}
	if _, _, err = s.ReserveAnalysis(context.Background(), "eleventh", spec, testNow); !errors.Is(err, ErrAnalysisBudget) {
		t.Fatal("quota bypass", err)
	}
	if _, err = l.Budget(testNow.Add(-time.Second)); err == nil {
		t.Fatal("clock rollback accepted")
	}
	if _, fresh, err := s.ReserveAnalysis(context.Background(), "tomorrow", spec, testNow.Add(25*time.Hour)); err != nil || !fresh {
		t.Fatal("window did not reopen", err)
	}
}

func TestAnalysisUncertainWriteRetainsReceiptAndProjectMemory(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	spec := analysisSpecFixture(t, s)
	ctx := context.Background()
	s.afterAnalysisCommit = func() error { return errors.New("synthetic lost acknowledgement") }
	if _, fresh, err := s.ReserveAnalysis(ctx, "uncertain", spec, testNow); err == nil || fresh {
		t.Fatal("uncertain reservation authorized send")
	}
	s.afterAnalysisCommit = nil
	r, fresh, err := s.ReserveAnalysis(ctx, "uncertain", spec, testNow)
	if err != nil || fresh {
		t.Fatal("lost acknowledgement led to a second send", err)
	}
	advice := json.RawMessage(`{"groups":[{"folderID":"draft","fileIDs":["f"],"reason":"fixture"}],"uncertainIDs":[]}`)
	r, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "succeeded", "", advice, models.Usage{}, testNow.Add(time.Second))
	if err != nil || r.Status != "succeeded" {
		t.Fatal(err)
	}
	if err = s.RecordAnalysisNote(ctx, r.ID, r.SpecSHA, testNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordAnalysisNote(ctx, r.ID, r.SpecSHA, testNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	v := readMemory(t, s)
	if len(v.State.Notes) != 1 || v.State.Notes[0].Kind != "agent_proposal" || v.State.Notes[0].Agent != "workshelf" || v.State.Revision != spec.MemoryRevision+1 {
		t.Fatal("note duplicated or upgraded")
	}
	l, err := s.Analysis(ctx)
	if err != nil || !l.Requests[0].NoteSaved {
		t.Fatal("note receipt lost", err)
	}
	before := v.State.Revision
	if _, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "request_failed", nil, models.Usage{}, testNow.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if readMemory(t, s).State.Revision != before {
		t.Fatal("completion replay touched facts")
	}
}

func TestAnalysisStalePauseAndCorruptLedger(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	spec := analysisSpecFixture(t, s)
	ctx := context.Background()
	r, _, err := s.ReserveAnalysis(ctx, "first", spec, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddNote(ctx, spec.MemoryRevision, "new-decision", Note{Kind: "user_decision", Text: "keep folders"}, testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	r, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, testNow.Add(2*time.Second))
	if err != nil || r.Status != "stale" || len(r.Advice) != 0 {
		t.Fatal("stale result published", err)
	}
	path := filepath.Join(s.directory, "analysis.json")
	if err = os.WriteFile(path, []byte(`{"schema":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReserveAnalysis(ctx, "second", spec, testNow); !errors.Is(err, ErrCorrupt) {
		t.Fatal("corrupt history overwritten", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"schema":999}` {
		t.Fatal("corrupt input changed")
	}
}
