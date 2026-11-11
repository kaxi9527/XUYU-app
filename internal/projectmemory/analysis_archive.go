package projectmemory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

// The small root is committed together with the working ledger. Every referenced
// index and receipt is immutable; unpublished files never become history.
type AnalysisArchive struct {
	Schema  int                             `json:"schema"`
	LastAt  string                          `json:"lastAt"`
	Records int                             `json:"records"`
	Calls   int                             `json:"calls"`
	Shards  map[string]AnalysisArchiveShard `json:"shards"`
}
type AnalysisArchiveShard struct {
	SHA     string `json:"sha"`
	Records int    `json:"records"`
	Calls   int    `json:"calls"`
}
type AnalysisSummary struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"`
	SpecSHA        string           `json:"specSHA"`
	Status         string           `json:"status"`
	Problem        string           `json:"problem,omitempty"`
	StartedAt      string           `json:"startedAt"`
	FinishedAt     string           `json:"finishedAt,omitempty"`
	Recipient      models.Recipient `json:"recipient"`
	ContextSHA     string           `json:"contextSHA,omitempty"`
	MemoryRevision uint64           `json:"memoryRevision,omitempty"`
	NoteSaved      bool             `json:"noteSaved"`
	Usage          models.Usage     `json:"usage"`
	ScopeName      string           `json:"scopeName,omitempty"`
	ExpiresAt      string           `json:"expiresAt,omitempty"`
	Total          int              `json:"total"`
	Completed      int              `json:"completed"`
	Running        int              `json:"running"`
	TotalFiles     int              `json:"totalFiles"`
	AnalyzedFiles  int              `json:"analyzedFiles"`
	ArchivedAt     string           `json:"archivedAt,omitempty"`
}
type analysisArchiveEntry struct {
	SHA     string          `json:"sha"`
	Summary AnalysisSummary `json:"summary"`
}
type analysisArchiveIndex struct {
	Schema    int                             `json:"schema"`
	ProjectID string                          `json:"projectID"`
	Shard     string                          `json:"shard"`
	Entries   map[string]analysisArchiveEntry `json:"entries"`
}
type analysisArchiveObject struct {
	Schema    int              `json:"schema"`
	ProjectID string           `json:"projectID"`
	ID        string           `json:"id"`
	Request   *AnalysisRequest `json:"request,omitempty"`
	Batch     *AnalysisBatch   `json:"batch,omitempty"`
}
type AnalysisPage struct {
	Items         []AnalysisSummary `json:"items"`
	Total         int               `json:"total"`
	Archived      int               `json:"archived"`
	Revision      uint64            `json:"revision"`
	ProjectID     string            `json:"projectID"`
	Budget        AnalysisBudget    `json:"budget"`
	BudgetProblem string            `json:"budgetProblem,omitempty"`
}

func analysisHash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func archiveShard(id string) string   { return analysisHash([]byte(id))[:2] }
func archiveHex(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(s) == n && hex.EncodeToString(b) == s
}

// Ambiguous duplicate keys must not turn a referenced identity into an absence.
func decodeAnalysis(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return ErrCorrupt
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return ErrCorrupt
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrCorrupt
		}
		end, err := d.Token()
		if err != nil {
			return err
		}
		if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return ErrCorrupt
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrCorrupt
	}
	return decode(data, out)
}
func (a *AnalysisArchive) valid() bool {
	if a == nil {
		return true
	}
	if a.Schema != 1 || !timeOK(a.LastAt) || a.Records < 1 || a.Calls < a.Records || len(a.Shards) == 0 || len(a.Shards) > 256 {
		return false
	}
	records, calls := 0, 0
	for key, ref := range a.Shards {
		if !archiveHex(key, 2) || !archiveHex(ref.SHA, 64) || ref.Records < 1 || ref.Records > 1<<30 || ref.Calls < ref.Records || ref.Calls > ref.Records*maxAnalysisRecords {
			return false
		}
		records += ref.Records
		calls += ref.Calls
	}
	return records == a.Records && calls == a.Calls
}
func summarizeAnalysis(r AnalysisRequest) AnalysisSummary {
	out := AnalysisSummary{ID: r.ID, Kind: "single", SpecSHA: r.SpecSHA, Status: r.Status, Problem: r.Problem, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Recipient: r.Spec.Recipient, ContextSHA: r.Spec.ContextSHA, MemoryRevision: r.Spec.MemoryRevision, NoteSaved: r.NoteSaved, Usage: r.Usage, Total: 1}
	if r.Status == "succeeded" {
		out.Completed = 1
	}
	if r.Status == "running" {
		out.Running = 1
	}
	return out
}
func summarizeBatch(b AnalysisBatch) AnalysisSummary {
	out := AnalysisSummary{ID: b.ID, Kind: "batch", SpecSHA: b.SpecSHA, Status: b.Status, Problem: b.Problem, StartedAt: b.CreatedAt, FinishedAt: b.FinishedAt, Recipient: b.Spec.Requests[0].Recipient, NoteSaved: b.NoteSaved, ScopeName: b.Spec.ScopeName, ExpiresAt: b.ExpiresAt, Total: len(b.Items)}
	for i, p := range b.Items {
		var packet struct {
			Body struct{ Files []json.RawMessage }
		}
		_ = json.Unmarshal([]byte(b.Spec.Requests[i].Request.Input), &packet)
		out.TotalFiles += len(packet.Body.Files)
		if p.Status == "succeeded" {
			out.Completed++
			out.AnalyzedFiles += len(packet.Body.Files)
		}
		if p.Status == "running" {
			out.Running++
		}
	}
	return out
}
func archiveEligibleRequest(r AnalysisRequest, now time.Time) bool {
	at, e := time.Parse(time.RFC3339Nano, r.StartedAt)
	finished, ef := time.Parse(time.RFC3339Nano, r.FinishedAt)
	return e == nil && ef == nil && !finished.After(now) && !at.After(now.Add(-24*time.Hour)) && r.Status != "running" && (r.Status != "succeeded" || r.NoteSaved)
}

// Called only while the project mutation lock is held by addNoteChecked.
func (s *Store) analysisIdle(ctx context.Context, now time.Time) error {
	l, err := s.analysisRead(ctx)
	if err != nil {
		return err
	}
	// The caller may have captured now before a different window reserved a
	// request. Future records are not idle: reject that stale clock snapshot
	// under the same mutation lock before appending a project note.
	if _, err = l.Budget(now); err != nil {
		return err
	}
	if l.batchBusy(now) {
		return ErrAnalysisBusy
	}
	for _, r := range l.Requests {
		if r.EffectiveStatus(now) == "running" {
			return ErrAnalysisBusy
		}
	}
	return nil
}
func archiveEligibleBatch(b AnalysisBatch, now time.Time) bool {
	if (b.Status != "completed" && b.Status != "stopped") || !b.NoteSaved {
		return false
	}
	finished, err := time.Parse(time.RFC3339Nano, b.FinishedAt)
	if err != nil || finished.After(now) {
		return false
	}
	for _, p := range b.Items {
		if p.Status == "running" {
			return false
		}
		if p.FinishedAt != "" {
			at, e := time.Parse(time.RFC3339Nano, p.FinishedAt)
			if e != nil || at.After(now) {
				return false
			}
		}
		value := b.CreatedAt
		if p.StartedAt != "" {
			value = p.StartedAt
		}
		at, e := time.Parse(time.RFC3339Nano, value)
		if e != nil || at.After(now.Add(-24*time.Hour)) {
			return false
		}
	}
	return true
}

// SettleAnalysis records an expired one-shot claim as unknown, never queued.
func (s *Store) SettleAnalysis(ctx context.Context, id, sha string, now time.Time) (AnalysisRequest, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisRequest{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return AnalysisRequest{}, err
	}
	for i := range l.Requests {
		r := &l.Requests[i]
		if r.ID != id {
			continue
		}
		if r.SpecSHA != sha {
			return *r, ErrOperationReused
		}
		if r.Status == "running" && r.EffectiveStatus(now) == "unknown" {
			r.Status = "unknown"
			r.Problem = "unknown_result"
			r.FinishedAt = stamp(now)
			if err = s.analysisWrite(ctx, l); err != nil {
				return *r, err
			}
		}
		return *r, nil
	}
	obj, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return AnalysisRequest{}, err
	}
	if found && obj.Request != nil && obj.Request.SpecSHA == sha {
		return *obj.Request, nil
	}
	return AnalysisRequest{}, os.ErrNotExist
}
func (s *Store) archiveBytes(ctx context.Context, prefix, sha string) ([]byte, error) {
	if !archiveHex(sha, 64) {
		return nil, ErrCorrupt
	}
	data, err := safeRead(ctx, filepath.Join(s.directory, prefix+sha+".json"), maxStateBytes)
	if err != nil {
		return nil, err
	}
	if analysisHash(data) != sha {
		return nil, ErrCorrupt
	}
	return data, nil
}
func (s *Store) writeArchiveBytes(ctx context.Context, prefix string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(data) > maxStateBytes {
		return "", ErrBudget
	}
	sha := analysisHash(data)
	path := filepath.Join(s.directory, prefix+sha+".json")
	if _, err = s.archiveBytes(ctx, prefix, sha); err == nil {
		// A previous attempt may have linked this exact object but failed its
		// directory sync. Readability alone does not establish durability.
		return sha, s.syncArchiveBytes(ctx, path, sha)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err = s.atomic(ctx, path, data, true); err != nil {
		return "", err
	}
	return sha, nil
}

func (s *Store) syncArchiveBytes(ctx context.Context, path, sha string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.ready(false); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Size() > maxStateBytes {
		return ErrCorrupt
	}
	f, err := openRegular(path, false)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return ErrCorrupt
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxStateBytes || analysisHash(data) != sha {
		return ErrCorrupt
	}
	if err = f.Sync(); err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return ErrCorrupt
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = s.ready(false); err != nil {
		return err
	}
	d, err := os.Open(s.directory)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) archiveIndex(ctx context.Context, l AnalysisLedger, key string) (analysisArchiveIndex, error) {
	out := analysisArchiveIndex{Schema: 1, ProjectID: s.project.ID, Shard: key, Entries: map[string]analysisArchiveEntry{}}
	if !archiveHex(key, 2) || !l.Archive.valid() {
		return out, ErrCorrupt
	}
	if l.Archive == nil {
		return out, nil
	}
	ref, ok := l.Archive.Shards[key]
	if !ok {
		return out, nil
	}
	data, err := s.archiveBytes(ctx, "analysis-index-", ref.SHA)
	if err != nil {
		return out, err
	}
	if decodeAnalysis(data, &out) != nil || out.Schema != 1 || out.ProjectID != s.project.ID || out.Shard != key || len(out.Entries) != ref.Records {
		return out, ErrCorrupt
	}
	last, _ := time.Parse(time.RFC3339Nano, l.Archive.LastAt)
	calls := 0
	for id, e := range out.Entries {
		v := e.Summary
		at, er := time.Parse(time.RFC3339Nano, v.ArchivedAt)
		started, es := time.Parse(time.RFC3339Nano, v.StartedAt)
		if !idOK(id) || len(id) > 64 || archiveShard(id) != key || v.ID != id || !archiveHex(e.SHA, 64) || !archiveHex(v.SpecSHA, 64) || er != nil || es != nil || at.After(last) || started.After(at.Add(-24*time.Hour)) || v.Running != 0 || !v.Recipient.Valid() || !v.Usage.ValidFor(v.Recipient) || v.Total < 1 || v.Total > maxAnalysisRecords || v.Completed < 0 || v.Completed > v.Total || v.TotalFiles < 0 || v.AnalyzedFiles < 0 || v.AnalyzedFiles > v.TotalFiles {
			return out, ErrCorrupt
		}
		if v.Kind == "single" {
			if v.Total != 1 || !contains([]string{"succeeded", "failed", "cancelled", "stale", "unknown"}, v.Status) || v.Status == "succeeded" && !v.NoteSaved {
				return out, ErrCorrupt
			}
		} else if v.Kind != "batch" || !contains([]string{"completed", "stopped"}, v.Status) || !v.NoteSaved {
			return out, ErrCorrupt
		}
		calls += v.Total
	}
	if calls != ref.Calls {
		return out, ErrCorrupt
	}
	return out, nil
}
func (s *Store) archivedAnalysis(ctx context.Context, l AnalysisLedger, id string) (analysisArchiveObject, bool, error) {
	var out analysisArchiveObject
	if !idOK(id) || len(id) > 64 {
		return out, false, ErrAnalysisConsent
	}
	index, err := s.archiveIndex(ctx, l, archiveShard(id))
	if err != nil {
		return out, false, err
	}
	entry, found := index.Entries[id]
	if !found {
		return out, false, nil
	}
	data, err := s.archiveBytes(ctx, "analysis-archive-", entry.SHA)
	if err != nil {
		return out, false, err
	}
	if decodeAnalysis(data, &out) != nil || out.Schema != 1 || out.ProjectID != s.project.ID || out.ID != id || (out.Request == nil) == (out.Batch == nil) {
		return out, false, ErrCorrupt
	}
	var summary AnalysisSummary
	at, _ := time.Parse(time.RFC3339Nano, entry.Summary.ArchivedAt)
	if out.Request != nil {
		if validateAnalysisRequest(s.project.ID, *out.Request) != nil || !archiveEligibleRequest(*out.Request, at) {
			return out, false, ErrCorrupt
		}
		summary = summarizeAnalysis(*out.Request)
	} else {
		if out.Batch.validate(s.project.ID) != nil || !archiveEligibleBatch(*out.Batch, at) {
			return out, false, ErrCorrupt
		}
		summary = summarizeBatch(*out.Batch)
	}
	summary.ArchivedAt = entry.Summary.ArchivedAt
	if summary.ID != id || !reflect.DeepEqual(summary, entry.Summary) {
		return out, false, ErrCorrupt
	}
	return out, true, nil
}

// ArchiveAnalyses preserves complete receipts, then commits one new archive root.
// Only reservations already outside the rolling window leave the working set.
func (s *Store) ArchiveAnalyses(ctx context.Context, now time.Time) (AnalysisLedger, error) {
	ready, err := s.ready(false)
	if err != nil || !ready {
		return s.Analysis(ctx)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return AnalysisLedger{}, err
	}
	defer release()
	l, err := s.analysisRead(ctx)
	if err != nil {
		return l, err
	}
	if _, err = l.Budget(now); err != nil {
		return l, err
	}
	objects := []analysisArchiveObject{}
	requests := []AnalysisRequest{}
	batches := []AnalysisBatch{}
	for _, r := range l.Requests {
		if archiveEligibleRequest(r, now) {
			copy := r
			objects = append(objects, analysisArchiveObject{Schema: 1, ProjectID: l.ProjectID, ID: r.ID, Request: &copy})
		} else {
			requests = append(requests, r)
		}
	}
	for _, b := range l.Batches {
		if archiveEligibleBatch(b, now) {
			copy := b
			objects = append(objects, analysisArchiveObject{Schema: 1, ProjectID: l.ProjectID, ID: b.ID, Batch: &copy})
		} else {
			batches = append(batches, b)
		}
	}
	if len(objects) == 0 {
		return l, nil
	}
	root := AnalysisArchive{Schema: 1, LastAt: stamp(now), Shards: map[string]AnalysisArchiveShard{}}
	if l.Archive != nil {
		root.Records = l.Archive.Records
		root.Calls = l.Archive.Calls
		for k, v := range l.Archive.Shards {
			root.Shards[k] = v
		}
	}
	indexes := map[string]analysisArchiveIndex{}
	for _, obj := range objects {
		key := archiveShard(obj.ID)
		index, ok := indexes[key]
		if !ok {
			index, err = s.archiveIndex(ctx, l, key)
			if err != nil {
				return l, err
			}
		}
		if _, exists := index.Entries[obj.ID]; exists {
			return l, ErrCorrupt
		}
		hash, err := s.writeArchiveBytes(ctx, "analysis-archive-", obj)
		if err != nil {
			return l, err
		}
		var summary AnalysisSummary
		if obj.Request != nil {
			summary = summarizeAnalysis(*obj.Request)
		} else {
			summary = summarizeBatch(*obj.Batch)
		}
		summary.ArchivedAt = stamp(now)
		index.Entries[obj.ID] = analysisArchiveEntry{SHA: hash, Summary: summary}
		indexes[key] = index
		root.Records++
		root.Calls += summary.Total
	}
	for key, index := range indexes {
		hash, err := s.writeArchiveBytes(ctx, "analysis-index-", index)
		if err != nil {
			return l, err
		}
		calls := 0
		for _, entry := range index.Entries {
			calls += entry.Summary.Total
		}
		root.Shards[key] = AnalysisArchiveShard{SHA: hash, Records: len(index.Entries), Calls: calls}
	}
	l.Archive = &root
	l.Requests = requests
	l.Batches = batches
	if s.beforeArchiveCommit != nil {
		if err = s.beforeArchiveCommit(); err != nil {
			return AnalysisLedger{}, err
		}
	}
	if err = s.analysisWrite(ctx, l); err != nil {
		return AnalysisLedger{}, err
	}
	return s.analysisRead(ctx)
}

func (s *Store) AnalysisReceipt(ctx context.Context, id string) (*AnalysisRequest, *AnalysisBatch, bool, error) {
	l, err := s.Analysis(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	for _, r := range l.Requests {
		if r.ID == id {
			return &r, nil, false, nil
		}
	}
	for _, b := range l.Batches {
		if b.ID == id {
			return nil, &b, false, nil
		}
	}
	obj, found, err := s.archivedAnalysis(ctx, l, id)
	if err != nil {
		return nil, nil, false, err
	}
	if !found {
		return nil, nil, false, os.ErrNotExist
	}
	return obj.Request, obj.Batch, true, nil
}

func (s *Store) AnalysisHistory(ctx context.Context, kind string, offset int, expectedRevision uint64) (AnalysisPage, error) {
	out := AnalysisPage{Items: []AnalysisSummary{}}
	if !contains([]string{"single", "batch"}, kind) || offset < 0 {
		return out, ErrAnalysisConsent
	}
	l, err := s.Analysis(ctx)
	if err != nil {
		return out, err
	}
	if offset > 0 && expectedRevision == 0 || expectedRevision != 0 && expectedRevision != l.Revision {
		return out, ErrConflict
	}
	out.Revision = l.Revision
	out.ProjectID = l.ProjectID
	out.Budget, err = l.Budget(time.Now())
	if err != nil {
		out.BudgetProblem = err.Error()
	}
	items := []AnalysisSummary{}
	ids := map[string]bool{}
	for _, r := range l.Requests {
		ids[r.ID] = true
		if kind == "single" {
			items = append(items, summarizeAnalysis(r))
		}
	}
	for _, b := range l.Batches {
		ids[b.ID] = true
		if kind == "batch" {
			items = append(items, summarizeBatch(b))
		}
	}
	if l.Archive != nil {
		for key := range l.Archive.Shards {
			index, err := s.archiveIndex(ctx, l, key)
			if err != nil {
				return out, err
			}
			for id, e := range index.Entries {
				if ids[id] {
					return out, ErrCorrupt
				}
				ids[id] = true
				if e.Summary.Kind == kind {
					items = append(items, e.Summary)
					out.Archived++
				}
			}
		}
	}
	out.Total = len(items)
	if offset > out.Total {
		return out, ErrAnalysisConsent
	}
	sort.Slice(items, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, items[i].StartedAt)
		b, _ := time.Parse(time.RFC3339Nano, items[j].StartedAt)
		if a.Equal(b) {
			return items[i].ID > items[j].ID
		}
		return a.After(b)
	})
	end := offset + 20
	if end > len(items) {
		end = len(items)
	}
	out.Items = append(out.Items, items[offset:end]...)
	return out, nil
}
