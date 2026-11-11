package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

func TestAnalysisDebitRestartArchiveAndReplay(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx, now := context.Background(), time.Now()
	spec := analysisSpecFixture(t, s)
	spec.Recipient.BaseURL = "https://openrouter.ai/api/v1"
	u := models.Usage{OpenRouterDebit: &models.OpenRouterDebit{Amount: "0.123456789012345678"}}
	r, _, err := s.ReserveAnalysis(ctx, "debit-single", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "unreliable_advice", nil, u, now.Add(time.Second))
	if err != nil || r.Usage.OpenRouterDebit == nil {
		t.Fatal("failed advice lost usage", err)
	}
	replay, err := s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "request_failed", nil, models.Usage{}, now.Add(2*time.Second))
	if err != nil || replay.Usage.OpenRouterDebit.Amount != u.OpenRouterDebit.Amount {
		t.Fatal("replay overwrote cost", err)
	}
	bspec := batchSpecFixture(t, s, 1)
	bspec.Requests[0].Recipient.BaseURL = spec.Recipient.BaseURL
	b, _, err := s.ReserveAnalysisBatch(ctx, "debit-batch", bspec, now)
	if err != nil {
		t.Fatal(err)
	}
	b, index, fresh, err := s.ClaimAnalysisBatch(ctx, b.ID, b.SpecSHA, 1, "debit-claim", now)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	p := b.Items[index]
	b, err = s.FinishAnalysisBatchPart(ctx, b.ID, b.SpecSHA, index, p.RunEpoch, p.ClaimID, "succeeded", "", json.RawMessage(`{"groups":[]}`), u, time.Now(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, archived := range []bool{false, true} {
		if archived {
			if err = s.RecordAnalysisBatchNote(ctx, b.ID, b.SpecSHA, now.Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ArchiveAnalyses(ctx, now.Add(25*time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		reopened, e := NewStore(filepath.Dir(s.directory), s.project)
		if e != nil {
			t.Fatal(e)
		}
		single, _, wasArchived, e := reopened.AnalysisReceipt(ctx, r.ID)
		if e != nil || single == nil || wasArchived != archived || single.Usage.OpenRouterDebit.Amount != u.OpenRouterDebit.Amount {
			t.Fatal("single restart/archive receipt", e, wasArchived)
		}
		_, batch, wasArchived, e := reopened.AnalysisReceipt(ctx, b.ID)
		if e != nil || batch == nil || wasArchived != archived || batch.Items[0].Usage.OpenRouterDebit.Amount != u.OpenRouterDebit.Amount {
			t.Fatal("batch restart/archive receipt", e, wasArchived)
		}
	}
}

func TestAnalysisDebitRejectsForeignRecipientAndStrictBudget(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	ctx, now := context.Background(), time.Now()
	spec := analysisSpecFixture(t, s)
	u := models.Usage{OpenRouterDebit: &models.OpenRouterDebit{Amount: "0"}}
	r, _, err := s.ReserveAnalysis(ctx, "foreign", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishAnalysis(ctx, r.ID, r.SpecSHA, "failed", "request_failed", nil, u, now); !errors.Is(err, ErrAnalysisConsent) {
		t.Fatal("foreign cost saved", err)
	}
	r.Status, r.FinishedAt, r.Problem, r.Usage = "failed", stamp(now), "request_failed", u
	if validateAnalysisRequest(s.project.ID, r) == nil {
		t.Fatal("foreign persisted cost accepted")
	}
	r.Spec.Recipient.BaseURL = "https://openrouter.ai/api/v1"
	r.SpecSHA = r.Spec.Digest()
	if validateAnalysisRequest(s.project.ID, r) != nil {
		t.Fatal("canonical source rejected")
	}
	r.Status = "running"
	r.FinishedAt = ""
	r.Problem = ""
	if validateAnalysisRequest(s.project.ID, r) == nil {
		t.Fatal("uncompleted cost accepted")
	}
	spec.StrictCostLimit = true
	if _, fresh, err := s.ReserveAnalysis(ctx, "strict", spec, now); fresh || !errors.Is(err, ErrAnalysisCost) {
		t.Fatal("strict budget bypass", err)
	}
}

// A content hash proves bytes, not their semantics. Historical summary reads
// must enforce the same receipt/source binding as the full archived payload.
func TestAnalysisDebitArchiveSummaryRejectsForeignRecipient(t *testing.T) {
	for _, protocol := range []models.Protocol{models.Chat, models.Anthropic} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx, now := context.Background(), time.Now()
			s, old := archiveFixture(t, 1, now.Add(-48*time.Hour))
			l, err := s.ArchiveAnalyses(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			key := archiveShard(old[0].ID)
			idx, err := s.archiveIndex(ctx, l, key)
			if err != nil {
				t.Fatal(err)
			}
			entry := idx.Entries[old[0].ID]
			if protocol == models.Anthropic {
				entry.Summary.Recipient.Protocol = protocol
				entry.Summary.Recipient.BaseURL = "https://openrouter.ai/api/v1"
			}
			if !entry.Summary.Recipient.Valid() {
				t.Fatal("fixture recipient invalid")
			}
			entry.Summary.Usage.OpenRouterDebit = &models.OpenRouterDebit{Amount: "0.123456789012345678"}
			idx.Entries[old[0].ID] = entry
			hash, err := s.writeArchiveBytes(ctx, "analysis-index-", idx)
			if err != nil {
				t.Fatal(err)
			}
			ref := l.Archive.Shards[key]
			ref.SHA = hash
			l.Archive.Shards[key] = ref
			if err = s.analysisWrite(ctx, l); err != nil {
				t.Fatal(err)
			}
			if _, err = s.AnalysisHistory(ctx, "single", 0, 0); !errors.Is(err, ErrCorrupt) {
				t.Fatal("history accepted mismatched debit", err)
			}
		})
	}
}
