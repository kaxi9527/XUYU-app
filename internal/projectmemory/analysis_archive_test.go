package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

func archiveFixture(t *testing.T, n int, at time.Time) (*Store, []AnalysisRequest) {
	t.Helper()
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx := context.Background()
	l, _ := s.Analysis(ctx)
	l.Quota = &AnalysisQuota{Requests: 100, InputBytes: 8 << 20, OutputTokens: 409600}
	if err := s.analysisWrite(ctx, l); err != nil {
		t.Fatal(err)
	}
	spec := analysisSpecFixture(t, s)
	out := []AnalysisRequest{}
	for i := 0; i < n; i++ {
		r, fresh, err := s.ReserveAnalysis(ctx, fmt.Sprintf("old-%03d", i), spec, at)
		if err != nil || !fresh {
			t.Fatal(i, err)
		}
		r, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "request_failed", nil, models.Usage{}, at.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return s, out
}
func TestArchiveCapacityHistoryAndIdempotency(t *testing.T) {
	now := time.Now()
	s, original := archiveFixture(t, 100, now.Add(-48*time.Hour))
	ctx := context.Background()
	l, _ := s.Analysis(ctx)
	before, _ := l.Budget(now)
	if before.RecordsRemaining != 0 {
		t.Fatal(before)
	}
	l, err := s.ArchiveAnalyses(ctx, now)
	if err != nil || len(l.Requests) != 0 || l.Archive == nil || l.Archive.Records != 100 {
		t.Fatal(l.Archive, err)
	}
	budget, err := l.Budget(now)
	if err != nil || budget.RecordsRemaining != 100 || budget.Reserved != before.Reserved {
		t.Fatal("archive changed rolling reservations", budget, err)
	}
	r, fresh, err := s.ReserveAnalysis(ctx, "new-request", analysisSpecFixture(t, s), now)
	if err != nil || !fresh {
		t.Fatal("capacity not recovered", err)
	}
	page, err := s.AnalysisHistory(ctx, "single", 0, 0)
	if err != nil || page.Total != 101 || page.Archived != 100 || page.Items[0].ID != r.ID {
		t.Fatal(page, err)
	}
	seen := map[string]bool{}
	for offset := 0; offset < page.Total; offset += 20 {
		p, err := s.AnalysisHistory(ctx, "single", offset, page.Revision)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range p.Items {
			if seen[item.ID] {
				t.Fatal("duplicate page item")
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 101 {
		t.Fatal("missing history")
	}
	for _, old := range original {
		got, batch, archived, err := s.AnalysisReceipt(ctx, old.ID)
		if err != nil || batch != nil || !archived || !reflect.DeepEqual(*got, old) {
			t.Fatal("changed archived receipt", old.ID, err)
		}
		replayed, fresh, err := s.ReserveAnalysis(ctx, old.ID, old.Spec, now)
		if err != nil || fresh || !reflect.DeepEqual(replayed, old) {
			t.Fatal("replay gained send right", err)
		}
	}
	changed := original[0].Spec
	changed.PreviewID = "different-preview"
	if _, fresh, err = s.ReserveAnalysis(ctx, original[0].ID, changed, now); fresh || !errors.Is(err, ErrOperationReused) {
		t.Fatal("reused archived ID", err)
	}
	if _, fresh, err = s.ReserveAnalysisBatch(ctx, original[0].ID, batchSpecFixture(t, s, 2), now); fresh || !errors.Is(err, ErrOperationReused) {
		t.Fatal("cross-kind archived ID", err)
	}
	if _, err = s.ArchiveAnalyses(ctx, now.Add(-time.Second)); err == nil {
		t.Fatal("archive clock rollback accepted")
	}
	if _, fresh, err = s.ReserveAnalysis(ctx, "clock-rollback", analysisSpecFixture(t, s), now.Add(-time.Second)); fresh || err == nil {
		t.Fatal("clock rollback sent", err)
	}
	if _, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "request_failed", nil, models.Usage{}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AnalysisHistory(ctx, "single", 20, page.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale page root accepted", err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(48*time.Hour))
	if err != nil || l.Archive.Records != 101 {
		t.Fatal("second archive", err)
	}
	reopened, err := NewStore(filepath.Dir(s.directory), s.project)
	if err != nil {
		t.Fatal(err)
	}
	page, err = reopened.AnalysisHistory(ctx, "single", 0, 0)
	if err != nil || page.Total != 101 || page.Archived != 101 {
		t.Fatal("restart lost history", err, page.Total)
	}
}

func TestArchiveWindowUnknownAndPendingNotes(t *testing.T) {
	now := time.Now()
	s, old := archiveFixture(t, 1, now.Add(-24*time.Hour))
	ctx := context.Background()
	l, err := s.ArchiveAnalyses(ctx, now.Add(-time.Nanosecond))
	if err != nil || len(l.Requests) != 1 {
		t.Fatal("archived inside window", err)
	}
	l, err = s.ArchiveAnalyses(ctx, now)
	if err != nil || l.Archive.Records != 1 {
		t.Fatal("exact boundary not freed", err)
	}
	if _, _, archived, err := s.AnalysisReceipt(ctx, old[0].ID); err != nil || !archived {
		t.Fatal(err)
	}
	r, _, err := s.ReserveAnalysis(ctx, "unfinished", analysisSpecFixture(t, s), now)
	if err != nil {
		t.Fatal(err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(48*time.Hour))
	if err != nil || len(l.Requests) != 1 {
		t.Fatal("archived unsettled request", err)
	}
	if _, err = s.SettleAnalysis(ctx, r.ID, r.SpecSHA, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(48*time.Hour))
	if err != nil || len(l.Requests) != 0 || l.Archive.Records != 2 {
		t.Fatal("settled unknown not archived", err)
	}
	got, _, _, err := s.AnalysisReceipt(ctx, r.ID)
	if err != nil || got.Status != "unknown" {
		t.Fatal("unknown became retryable", err)
	}
	// A successful result remains in the working set until its original note is saved.
	s, _ = archiveFixture(t, 0, now)
	r, _, err = s.ReserveAnalysis(ctx, "needs-note", analysisSpecFixture(t, s), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.FinishAnalysisChecked(ctx, r.ID, r.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(25*time.Hour))
	if err != nil || len(l.Requests) != 1 {
		t.Fatal("unnoted success archived", err)
	}
	if err = s.RecordAnalysisNote(ctx, r.ID, r.SpecSHA, time.Now()); err != nil {
		t.Fatal(err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(25*time.Hour))
	if err != nil || len(l.Requests) != 0 {
		t.Fatal("noted success not archived", err)
	}
}

func TestArchiveBatchRetainsActualStartWindowAndWholeReceipt(t *testing.T) {
	now := time.Now()
	s, singles := archiveFixture(t, 1, now.Add(-48*time.Hour))
	ctx := context.Background()
	b := reserveBatchFixture(t, s, 2, now)
	var err error
	b, _, _, err = s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, b.RunEpoch, "later-start", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.FinishAnalysisBatchPart(ctx, b.ID, b.SpecSHA, 0, b.RunEpoch, "later-start", "failed", "request_failed", nil, models.Usage{}, now.Add(time.Hour+time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ArchiveAnalyses(ctx, now.Add(24*time.Hour))
	if err != nil || len(l.Batches) != 1 {
		t.Fatal("unnoted batch archived", err)
	}
	if err = s.RecordAnalysisBatchNote(ctx, b.ID, b.SpecSHA, now.Add(time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if l.Archive != nil && l.Archive.Records != len(singles) {
		t.Fatal("single archive missing")
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(25*time.Hour-time.Nanosecond))
	if err != nil || len(l.Batches) != 1 {
		t.Fatal("actual start reservation was returned early", err)
	}
	l, err = s.ArchiveAnalyses(ctx, now.Add(25*time.Hour))
	if err != nil || len(l.Batches) != 0 || l.Archive.Calls != 3 {
		t.Fatal("whole batch not archived", err)
	}
	_, got, archived, err := s.AnalysisReceipt(ctx, b.ID)
	if err != nil || !archived || got == nil || len(got.Items) != 2 || got.Items[0].Status != "failed" || got.Items[1].Status != "queued" {
		t.Fatal("partial receipt changed", err)
	}
	if _, fresh, err := s.ReserveAnalysisBatch(ctx, b.ID, b.Spec, now.Add(27*time.Hour)); err != nil || fresh {
		t.Fatal("archived batch replay", err)
	}
	// The pure eligibility predicate is shared by commit and read validation.
	if archiveEligibleBatch(*got, now.Add(25*time.Hour-time.Nanosecond)) || !archiveEligibleBatch(*got, now.Add(25*time.Hour)) {
		t.Fatal("actual start window changed")
	}
}

func TestArchiveTwoPhaseFailuresAndConcurrentRetry(t *testing.T) {
	now := time.Now()
	s, old := archiveFixture(t, 3, now.Add(-48*time.Hour))
	ctx := context.Background()
	s.beforeArchiveCommit = func() error { return errors.New("before root commit") }
	if _, err := s.ArchiveAnalyses(ctx, now); err == nil {
		t.Fatal("missing injected failure")
	}
	l, _ := s.Analysis(ctx)
	if l.Archive != nil || len(l.Requests) != 3 {
		t.Fatal("unpublished files became committed")
	}
	p, err := s.AnalysisHistory(ctx, "single", 0, 0)
	if err != nil || p.Total != 3 || p.Archived != 0 {
		t.Fatal("unpublished history visible", err)
	}
	s.beforeArchiveCommit = nil
	s.afterAnalysisCommit = func() error { return errors.New("lost root acknowledgement") }
	if _, err = s.ArchiveAnalyses(ctx, now); err == nil {
		t.Fatal("missing lost reply")
	}
	s.afterAnalysisCommit = nil
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, e := NewStore(filepath.Dir(s.directory), s.project)
			if e == nil {
				_, e = other.ArchiveAnalyses(ctx, now.Add(time.Second))
			}
			failures <- e
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	l, err = s.Analysis(ctx)
	if err != nil || l.Archive.Records != 3 || len(l.Requests) != 0 {
		t.Fatal("duplicated root", err)
	}
	got, _, _, err := s.AnalysisReceipt(ctx, old[0].ID)
	if err != nil || !reflect.DeepEqual(*got, old[0]) {
		t.Fatal("retry lost receipt", err)
	}
}

func TestArchiveCorruptionDoesNotAuthorizeReplay(t *testing.T) {
	for _, failure := range []string{"missing-index", "bad-index", "index-link", "missing-object", "bad-object", "object-link"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Now()
			s, records := archiveFixture(t, 1, now.Add(-48*time.Hour))
			ctx := context.Background()
			l, err := s.ArchiveAnalyses(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			key := archiveShard(records[0].ID)
			index, err := s.archiveIndex(ctx, l, key)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.directory, "analysis-index-"+l.Archive.Shards[key].SHA+".json")
			if failure == "missing-object" || failure == "bad-object" || failure == "object-link" {
				path = filepath.Join(s.directory, "analysis-archive-"+index.Entries[records[0].ID].SHA+".json")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "bad-index", "bad-object":
				err = os.WriteFile(path, []byte(`{}`), 0600)
			case "index-link", "object-link":
				target := filepath.Join(t.TempDir(), "target")
				if err = os.WriteFile(target, data, 0600); err == nil {
					err = os.Symlink(target, path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, fresh, err := s.ReserveAnalysis(ctx, records[0].ID, records[0].Spec, now); fresh || err == nil {
				t.Fatal("corruption treated as new ID", err)
			}
			if _, _, _, err := s.AnalysisReceipt(ctx, records[0].ID); err == nil {
				t.Fatal("corrupt history accepted")
			}
		})
	}
}

func TestArchiveIdleNoteGuardAndAmbiguousData(t *testing.T) {
	now := time.Now()
	s, _ := archiveFixture(t, 0, now)
	ctx := context.Background()
	old, _, err := s.ReserveAnalysis(ctx, "old-success", analysisSpecFixture(t, s), now)
	if err != nil {
		t.Fatal(err)
	}
	old, err = s.FinishAnalysisChecked(ctx, old.ID, old.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := s.ReserveAnalysis(ctx, "current", analysisSpecFixture(t, s), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := readMemory(t, s).State.Revision
	if err = s.RecordAnalysisNoteIfIdle(ctx, old.ID, old.SpecSHA, time.Now()); !errors.Is(err, ErrAnalysisBusy) {
		t.Fatal("maintenance disturbed live context", err)
	}
	if readMemory(t, s).State.Revision != before {
		t.Fatal("busy maintenance appended note")
	}
	if _, err = s.FinishAnalysis(ctx, current.ID, current.SpecSHA, "failed", "request_failed", nil, models.Usage{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordAnalysisNoteIfIdle(ctx, old.ID, old.SpecSHA, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordAnalysisNoteIfIdle(ctx, old.ID, old.SpecSHA, time.Now()); err != nil {
		t.Fatal(err)
	}
	if readMemory(t, s).State.Revision != before+1 {
		t.Fatal("note not idempotent")
	}
	for _, data := range []string{`{"id":"a","id":"b"}`, `{"nested":{"id":"a","id":"b"}}`, `{} {}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		var out any
		if err = decodeAnalysis([]byte(data), &out); err == nil {
			t.Fatal("ambiguous archive accepted", data[:2])
		}
	}
}

func TestArchiveIdleNoteRejectsConcurrentReservationAfterCapturedTime(t *testing.T) {
	for _, oldKind := range []string{"single", "batch"} {
		for _, liveKind := range []string{"single", "batch"} {
			t.Run(oldKind+"-with-"+liveKind, func(t *testing.T) {
				ctx := context.Background()
				now := time.Now()
				oldAt := now.Add(-25 * time.Hour)
				s, _ := archiveFixture(t, 0, now)
				var record func() error
				if oldKind == "single" {
					old, _, err := s.ReserveAnalysis(ctx, "old-success", analysisSpecFixture(t, s), oldAt)
					if err != nil {
						t.Fatal(err)
					}
					old, err = s.FinishAnalysis(ctx, old.ID, old.SpecSHA, "succeeded", "", json.RawMessage(`{"groups":[]}`), models.Usage{}, oldAt.Add(time.Second))
					if err != nil {
						t.Fatal(err)
					}
					record = func() error { return s.RecordAnalysisNoteIfIdle(ctx, old.ID, old.SpecSHA, now) }
				} else {
					old, _, err := s.ReserveAnalysisBatch(ctx, "old-batch", batchSpecFixture(t, s, 1), oldAt)
					if err != nil {
						t.Fatal(err)
					}
					old, err = s.StopAnalysisBatch(ctx, old.ID, old.SpecSHA, "cancelled", oldAt.Add(time.Second))
					if err != nil {
						t.Fatal(err)
					}
					record = func() error { return s.RecordAnalysisBatchNoteIfIdle(ctx, old.ID, old.SpecSHA, now) }
				}
				// Simulate another window committing after maintenance captured now.
				if liveKind == "single" {
					if _, _, err := s.ReserveAnalysis(ctx, "new-running", analysisSpecFixture(t, s), now.Add(time.Millisecond)); err != nil {
						t.Fatal(err)
					}
				} else {
					reserveBatchFixture(t, s, 1, now.Add(time.Millisecond))
				}
				before := readMemory(t, s).State.Revision
				if err := record(); err == nil {
					t.Fatal("stale maintenance snapshot treated a new request as idle")
				}
				if readMemory(t, s).State.Revision != before {
					t.Fatal("maintenance changed confirmed context")
				}
			})
		}
	}
}

func TestArchiveAcrossProcesses(t *testing.T) {
	type fixture struct {
		Base    string
		Project Project
		Now     time.Time
	}
	if input := os.Getenv("WORKSHELF_ARCHIVE_FIXTURE"); input != "" {
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
		l, err := s.ArchiveAnalyses(context.Background(), f.Now)
		if err != nil || l.Archive == nil || l.Archive.Records != 3 {
			t.Fatal("process archive failed", err)
		}
		return
	}
	now := time.Now()
	s, _ := archiveFixture(t, 3, now.Add(-48*time.Hour))
	input := filepath.Join(t.TempDir(), "archive.json")
	data, _ := json.Marshal(fixture{filepath.Dir(s.directory), s.project, now})
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := []*exec.Cmd{}
	defer func() {
		for _, cmd := range commands {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}()
	for i := 0; i < 3; i++ {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveAcrossProcesses$")
		cmd.Env = append(os.Environ(), "WORKSHELF_ARCHIVE_FIXTURE="+input)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.Analysis(context.Background())
	if err != nil || l.Archive.Records != 3 || l.Archive.Calls != 3 || len(l.Requests) != 0 {
		t.Fatal("cross-process duplicated archive", err)
	}
}
