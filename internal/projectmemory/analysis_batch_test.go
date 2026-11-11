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

func batchSpecFixture(t *testing.T, s *Store, n int) AnalysisBatchSpec {
	t.Helper()
	one := analysisSpecFixture(t, s)
	l, err := s.Analysis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec := AnalysisBatchSpec{PreviewID: one.PreviewID, PlanSHA: strings.Repeat("b", 64), ScopeSHA: strings.Repeat("c", 64), ScopeID: "dir-root", ScopeName: "整个项目", LedgerRevision: l.Revision, PreviousQuota: l.limit(), Quota: l.limit(), Requests: []AnalysisSpec{}}
	for i := 0; i < n; i++ {
		r := one
		var packet map[string]any
		if err = json.Unmarshal([]byte(r.Request.Input), &packet); err != nil {
			t.Fatal(err)
		}
		packet["body"].(map[string]any)["files"] = []any{map[string]any{"id": fmt.Sprintf("file-%d", i), "kind": "file", "status": "present"}}
		data, _ := json.Marshal(packet)
		r.Request.Input = string(data)
		spec.Requests = append(spec.Requests, r)
	}
	need := spec.Required()
	spec.Quota.Requests = max(spec.Quota.Requests, need.Requests)
	spec.Quota.InputBytes = max(spec.Quota.InputBytes, need.InputBytes)
	spec.Quota.OutputTokens = max(spec.Quota.OutputTokens, need.OutputTokens)
	spec.QuotaChangeAccepted = spec.Quota != spec.PreviousQuota
	return spec
}
func reserveBatchFixture(t *testing.T, s *Store, n int, now time.Time) AnalysisBatch {
	t.Helper()
	b, fresh, err := s.ReserveAnalysisBatch(context.Background(), "batch", batchSpecFixture(t, s, n), now)
	if err != nil || !fresh {
		t.Fatal("reserve", err, fresh)
	}
	return b
}
func finishBatchFixture(t *testing.T, s *Store, b AnalysisBatch, index int) AnalysisBatch {
	t.Helper()
	p := b.Items[index]
	out, err := s.FinishAnalysisBatchPart(context.Background(), b.ID, b.SpecSHA, index, p.RunEpoch, p.ClaimID, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, time.Now(), func() error { return nil })
	if err != nil {
		t.Fatal("finish", err)
	}
	return out
}

func TestBatchQuotaConsentAtomicReservationAndReplay(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx := context.Background()
	now := time.Now()
	spec := batchSpecFixture(t, s, 12)
	spec.QuotaChangeAccepted = false
	if _, _, err := s.ReserveAnalysisBatch(ctx, "batch", spec, now); !errors.Is(err, ErrAnalysisConsent) {
		t.Fatal("silent quota increase", err)
	}
	spec.QuotaChangeAccepted = true
	s.afterAnalysisCommit = func() error { return errors.New("lost acknowledgement") }
	if _, fresh, err := s.ReserveAnalysisBatch(ctx, "batch", spec, now); err == nil || fresh {
		t.Fatal("write acknowledgement loss dispatched")
	}
	s.afterAnalysisCommit = nil
	b, fresh, err := s.ReserveAnalysisBatch(ctx, "batch", spec, now)
	if err != nil || fresh || len(b.Items) != 12 {
		t.Fatal("replayed reservation", err, fresh)
	}
	l, err := s.Analysis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := l.Budget(now)
	if err != nil || budget.Limit.Requests != 12 || budget.Reserved.Requests != 12 || budget.RecordsRemaining != 88 {
		t.Fatal("quota", budget, err)
	}
	if _, _, err = s.ReserveAnalysis(ctx, "single", analysisSpecFixture(t, s), now); !errors.Is(err, ErrAnalysisBusy) {
		t.Fatal("overlapping single request", err)
	}
	other, otherScope := memoryFixture(t)
	initialize(t, other, otherScope)
	otherL, _ := other.Analysis(ctx)
	otherB, _ := otherL.Budget(now)
	if otherB.Limit.Requests != 10 {
		t.Fatal("changed other project")
	}
	changed := spec
	changed.ScopeName = "changed"
	if _, _, err = s.ReserveAnalysisBatch(ctx, "batch", changed, now); !errors.Is(err, ErrOperationReused) {
		t.Fatal("changed manifest reused", err)
	}
	if _, _, err = s.ReserveAnalysisBatch(ctx, "new", spec, now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale quota preview", err)
	}
}

func TestBatchClaimsSerializedPauseResumeFencesAndSingleNote(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx := context.Background()
	b := reserveBatchFixture(t, s, 2, time.Now())
	before := readMemory(t, s).State.Revision
	var won atomic.Int32
	var mu sync.Mutex
	var winner AnalysisBatch
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other, err := NewStore(filepath.Dir(s.directory), s.project)
			if err != nil {
				t.Error(err)
				return
			}
			got, index, fresh, err := other.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, fmt.Sprintf("claim-%d", i), time.Now())
			if fresh {
				won.Add(1)
				if index != 0 {
					t.Error("out of order")
				}
				mu.Lock()
				winner = got
				mu.Unlock()
			} else if !errors.Is(err, ErrAnalysisBusy) {
				t.Errorf("unexpected claim result %v", err)
			}
		}(i)
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatal("concurrent claims", won.Load())
	}
	b = winner
	if _, err := s.SetAnalysisBatchPaused(ctx, b.ID, b.SpecSHA, false, time.Now()); !errors.Is(err, ErrAnalysisBusy) {
		t.Fatal("resumed in-flight", err)
	}
	if _, err := s.SetAnalysisBatchPaused(ctx, b.ID, b.SpecSHA, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	b = finishBatchFixture(t, s, b, 0)
	if b.Status != "paused" || readMemory(t, s).State.Revision != before {
		t.Fatal("completion overwrote pause or appended note")
	}
	if _, _, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "paused-claim", time.Now()); fresh || !errors.Is(err, ErrAnalysisBatch) {
		t.Fatal("claimed while paused", err)
	}
	var err error
	b, err = s.SetAnalysisBatchPaused(ctx, b.ID, b.SpecSHA, false, time.Now())
	if err != nil || b.RunEpoch != 2 {
		t.Fatal(err, b.RunEpoch)
	}
	if old, err := s.PauseAnalysisBatchRun(ctx, b.ID, b.SpecSHA, 1, time.Now()); !errors.Is(err, ErrAnalysisBatch) || old.Status != "active" {
		t.Fatal("old worker paused resumed batch", old.Status, err)
	}
	if old, err := s.StopAnalysisBatchRun(ctx, b.ID, b.SpecSHA, "connection_changed", 1, time.Now()); !errors.Is(err, ErrAnalysisBatch) || old.Status != "active" {
		t.Fatal("old worker stopped resumed batch", old.Status, err)
	}
	if _, _, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "old-runner", time.Now()); fresh || !errors.Is(err, ErrAnalysisBatch) {
		t.Fatal("old runner claimed", err)
	}
	b, index, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 2, "second", time.Now())
	if err != nil || !fresh || index != 1 {
		t.Fatal(err, index)
	}
	if _, err = s.FinishAnalysisBatchPart(ctx, b.ID, b.SpecSHA, 1, 2, "forged", "failed", "request_failed", nil, models.Usage{}, time.Now(), nil); !errors.Is(err, ErrOperationReused) {
		t.Fatal("forged finish", err)
	}
	b = finishBatchFixture(t, s, b, 1)
	if b.Status != "completed" {
		t.Fatal(b.Status)
	}
	if err = s.RecordAnalysisBatchRunNote(ctx, b.ID, b.SpecSHA, 1, time.Now()); !errors.Is(err, ErrAnalysisBatch) {
		t.Fatal("old worker wrote current epoch note", err)
	}
	for i := 0; i < 2; i++ {
		if err = s.RecordAnalysisBatchNote(ctx, b.ID, b.SpecSHA, time.Now()); err != nil {
			t.Fatal("note", err)
		}
	}
	if readMemory(t, s).State.Revision != before+1 {
		t.Fatal("duplicate aggregate note")
	}
	l, err := s.Analysis(ctx)
	if err != nil || !l.Batches[0].NoteSaved {
		t.Fatal(err)
	}
}

func TestAnalysisReadWaitsForProjectWriter(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	reserveBatchFixture(t, s, 2, time.Now())
	unlock, err := s.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// A second Store models another process polling the same ledger.
	reader, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = reader.Analysis(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("read bypassed the active project writer", err)
	}
}

func TestBatchRestartUnknownExpiresAndRollingClaimBudget(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown never requeued", func(t *testing.T) {
		s, scope := memoryFixture(t)
		initialize(t, s, scope)
		now := time.Now()
		b := reserveBatchFixture(t, s, 2, now)
		b, _, _, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "uncertain", now)
		if err != nil {
			t.Fatal(err)
		}
		other, _ := NewStore(filepath.Dir(s.directory), s.project)
		got, err := other.SettleAnalysisBatch(ctx, b.ID, b.SpecSHA, now.Add(time.Minute))
		if err != nil || got.Status != "stopped" || got.Items[0].Status != "unknown" || got.Items[1].Status != "queued" {
			t.Fatal(got.Status, err)
		}
		if _, _, fresh, err := other.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "replay", now.Add(time.Minute)); fresh || err == nil {
			t.Fatal("replayed unknown")
		}
	})
	t.Run("expired queued cannot run", func(t *testing.T) {
		s, scope := memoryFixture(t)
		initialize(t, s, scope)
		now := time.Now()
		b := reserveBatchFixture(t, s, 2, now)
		got, err := s.SettleAnalysisBatch(ctx, b.ID, b.SpecSHA, now.Add(AnalysisBatchTTL))
		if err != nil || got.Problem != "expired" {
			t.Fatal(err)
		}
		if _, err = s.SetAnalysisBatchPaused(ctx, b.ID, b.SpecSHA, false, now.Add(25*time.Hour)); err == nil {
			t.Fatal("resumed expired authorization")
		}
		if _, _, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "expired", now.Add(25*time.Hour)); fresh || err == nil {
			t.Fatal("expired claim")
		}
	})
	t.Run("claim extends original reservation", func(t *testing.T) {
		s, scope := memoryFixture(t)
		initialize(t, s, scope)
		now := time.Now()
		created := now.Add(-119 * time.Minute)
		b := reserveBatchFixture(t, s, 2, created)
		_, _, _, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "delayed", now)
		if err != nil {
			t.Fatal(err)
		}
		l, _ := s.Analysis(ctx)
		budget, err := l.Budget(now)
		if err != nil || budget.Reserved.Requests != 2 {
			t.Fatal("double counted", budget, err)
		}
		budget, err = l.Budget(created.Add(24*time.Hour + time.Minute))
		if err != nil || budget.Reserved.Requests != 1 {
			t.Fatal("actual call fell out too early", budget, err)
		}
		if _, err = l.Budget(created.Add(-time.Second)); err == nil {
			t.Fatal("clock reversal ignored")
		}
	})
}

func TestBatchPublicationContextChangeAndReservationWriteFailure(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx := context.Background()
	b := reserveBatchFixture(t, s, 2, time.Now())
	b, _, _, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "publish", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.FinishAnalysisBatchPart(ctx, b.ID, b.SpecSHA, 0, 1, "publish", "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, time.Now(), func() error { return ErrConflict })
	if err != nil || out.Status != "stopped" || out.Items[0].Status != "stale" || len(out.Items[0].Advice) != 0 {
		t.Fatal("stale result published", err)
	}
	if _, _, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "next", time.Now()); fresh || err == nil {
		t.Fatal("continued after context change")
	}
	other, otherScope := memoryFixture(t)
	initialize(t, other, otherScope)
	spec := batchSpecFixture(t, other, 2)
	if os.Getuid() == 0 {
		t.Skip("read-only directory fault requires non-root fixture")
	}
	if err = os.Chmod(other.directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(other.directory, 0700) })
	if _, fresh, err := other.ReserveAnalysisBatch(ctx, "write", spec, time.Now()); fresh || err == nil {
		t.Fatal("failed write accepted")
	}
	l, err := other.Analysis(ctx)
	if err != nil || len(l.Batches) != 0 {
		t.Fatal("partial reservation", err)
	}
}

func TestBatchClaimAcrossProcesses(t *testing.T) {
	type fixture struct {
		Base                   string
		Project                Project
		ID, SHA, Claim, Output string
	}
	if input := os.Getenv("WORKSHELF_BATCH_CLAIM_FIXTURE"); input != "" {
		data, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if err = json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(f.Base, f.Project)
		if err != nil {
			t.Fatal(err)
		}
		_, _, fresh, err := s.ClaimAnalysisBatch(context.Background(), f.ID, f.SHA, 1, f.Claim, time.Now())
		out, _ := json.Marshal(map[string]bool{"fresh": fresh, "busy": errors.Is(err, ErrAnalysisBusy)})
		if err = os.WriteFile(f.Output, out, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	batch := reserveBatchFixture(t, s, 3, time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	commands := []*exec.Cmd{}
	outputs := []string{}
	for i := 0; i < 3; i++ {
		input := filepath.Join(t.TempDir(), "claim.json")
		output := input + ".result"
		f := fixture{filepath.Dir(s.directory), s.project, batch.ID, batch.SpecSHA, fmt.Sprintf("process-%d", i), output}
		data, _ := json.Marshal(f)
		if err := os.WriteFile(input, data, 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBatchClaimAcrossProcesses$")
		command.Env = append(os.Environ(), "WORKSHELF_BATCH_CLAIM_FIXTURE="+input)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
		outputs = append(outputs, output)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	wins, busy := 0, 0
	for _, output := range outputs {
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]bool
		if json.Unmarshal(data, &out) != nil {
			t.Fatal("invalid child result")
		}
		if out["fresh"] {
			wins++
		}
		if out["busy"] {
			busy++
		}
	}
	if wins != 1 || busy != 2 {
		t.Fatal("cross-process claims", wins, busy)
	}
}
