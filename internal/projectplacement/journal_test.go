//go:build (darwin || linux) && cgo

package projectplacement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func journalFixture(t *testing.T) (*Journal, Preview, []string) {
	t.Helper()
	a, proposal := inputs(t, "project/a.txt", "project/b.txt")
	control := filepath.Join(a.Boundaries[0], "control")
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	a.Excluded = []string{control}
	preview, err := BuildPreview(context.Background(), a, proposal, PreviewLimits{})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := NewJournal(control, a.Project)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, row := range preview.snapshot.Rows {
		ids = append(ids, row.FileID)
	}
	return journal, preview, ids
}

func prepared(t *testing.T, j *Journal, p Preview, ids []string) JournalView {
	t.Helper()
	v, err := j.Prepare(context.Background(), "operation-one", p, ids)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func confirmed(t *testing.T, j *Journal, p Preview, ids []string) JournalView {
	t.Helper()
	v := prepared(t, j, p, ids)
	v, err := j.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func pretendReceipt(row Row) PhysicalReceipt {
	after := row.Before
	after.Path = row.Target
	return PhysicalReceipt{Outcome: "moved", Before: row.Before, After: &after, SourceObject: row.Object, TargetObject: row.Object, Reason: "状态协议夹具；不代表实际文件移动"}
}

func protocolMemory(v JournalView, claim string) MemoryReceipt {
	return MemoryReceipt{ProjectID: v.Intent.Preview.Authority.Project.ID, Operation: memoryOperationID(v.Intent.ID, claim), Revision: 7, RecordSHA: strings.Repeat("a", 64), EventID: "7:1"}
}

func TestJournalReadDoesNotCreateAndPreparationIsNotConsent(t *testing.T) {
	j, p, ids := journalFixture(t)
	before := disk(t, j.control)
	if _, err := j.Read(context.Background(), "operation-one"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, disk(t, j.control)) {
		t.Fatal("read created journal")
	}
	v := prepared(t, j, p, ids)
	if v.Status != "prepared" || v.ConfirmedAt != "" || v.Revision != 0 || v.Intent.Preview.Authority.Project.Path != p.snapshot.Authority.Project.Path {
		t.Fatal("prepare implied consent", v.Status)
	}
	_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[0], time.Now()); return err })
	if !errors.Is(err, ErrConsent) {
		t.Fatal("unconfirmed claim accepted", err)
	}
	for _, row := range p.snapshot.Rows {
		if data, err := os.ReadFile(row.Source); err != nil || string(data) != "example" {
			t.Fatal("prepare moved source")
		}
	}
	again, err := j.Prepare(context.Background(), v.Intent.ID, p, ids)
	if err != nil || again.IntentSHA != v.IntentSHA || again.Revision != 0 {
		t.Fatal("not idempotent", err)
	}
	if _, err = j.Prepare(context.Background(), v.Intent.ID, p, ids[:1]); !errors.Is(err, ErrOperation) {
		t.Fatal("selection changed under same operation", err)
	}
}

func TestJournalConfirmBindsScopeTTLAndKeepsOriginalReceipt(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := prepared(t, j, p, ids)
	wrong := p.Snapshot().Authority
	wrong.Scope.Files = []string{filepath.Join(wrong.Project.Path, "sibling")}
	if _, err := j.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, wrong, time.Now()); !errors.Is(err, ErrConsent) {
		t.Fatal("scope drift accepted", err)
	}
	expires, _ := time.Parse(time.RFC3339Nano, p.snapshot.ExpiresAt)
	if _, err := j.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, expires); !errors.Is(err, ErrConsent) {
		t.Fatal("expired consent accepted", err)
	}
	v, err := j.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now())
	if err != nil || v.Status != "confirmed" || v.Revision != 1 {
		t.Fatal("confirm", err)
	}
	reopened, err := NewJournal(j.control, j.project)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, expires.Add(time.Hour))
	if err != nil || again.Revision != v.Revision || again.ConfirmedAt != v.ConfirmedAt {
		t.Fatal("retry reconfirmed/resumed", err)
	}
}

func TestJournalClaimAndMemoryPendingNeverReexecuteOnReopen(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := confirmed(t, j, p, ids)
	var claim string
	_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
		var err error
		claim, err = s.claim(context.Background(), ids[0], time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, _ := NewJournal(j.control, j.project)
	v, err = reopened.Read(context.Background(), v.Intent.ID)
	if err != nil || v.Items[0].Stage != "claimed" || !v.NeedsReconciliation {
		t.Fatal("claim lost", err)
	}
	_, err = reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[0], time.Now()); return err })
	if !errors.Is(err, ErrReconcile) {
		t.Fatal("claim replayed", err)
	}
	row, _ := selectedRow(v, ids[0])
	receipt := pretendReceipt(row)
	v, err = reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error { return s.physical(ids[0], claim, receipt, time.Now()) })
	if err != nil || v.Items[0].Stage != "memory_pending" || !v.NeedsReconciliation {
		t.Fatal("missing pending memory", err)
	}
	_, err = reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[1], time.Now()); return err })
	if !errors.Is(err, ErrReconcile) {
		t.Fatal("next item advanced before memory", err)
	}
	memory := protocolMemory(v, claim)
	v, err = reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error { return s.memory(ids[0], claim, memory, time.Now()) })
	if err != nil || v.Items[0].Stage != "moved" || v.NeedsReconciliation {
		t.Fatal("memory acknowledgment", err)
	}
	previous := v.Revision
	v, err = reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
		if err := s.physical(ids[0], claim, receipt, time.Now()); err != nil {
			return err
		}
		return s.memory(ids[0], claim, memory, time.Now())
	})
	if err != nil || v.Revision != previous {
		t.Fatal("receipt retry appended again", err)
	}
	// There has deliberately been no executor call in this state-machine test.
	for _, row := range p.snapshot.Rows {
		if _, err := os.Stat(row.Source); err != nil {
			t.Fatal("journal moved a source")
		}
	}
}

func TestJournalPublicationFailuresPreserveEvidenceWithoutReplaying(t *testing.T) {
	for _, component := range []string{"commitment", "event"} {
		for _, stage := range []string{"before_create", "before_file_sync", "before_publish", "after_publish", "before_directory_sync", "after_directory_sync"} {
			t.Run(component+"/"+stage, func(t *testing.T) {
				j, p, ids := journalFixture(t)
				v := prepared(t, j, p, ids)
				failure := errors.New("injected persistence failure")
				file := recordFile(1)
				if component == "commitment" {
					file = commitmentFile(1)
				}
				j.fault = func(point string) error {
					if point == file+":"+stage {
						return failure
					}
					return nil
				}
				if _, err := j.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now()); !errors.Is(err, failure) {
					t.Fatal("fault not reached", err)
				}
				j.fault = nil
				reopened, _ := NewJournal(j.control, j.project)
				read, err := reopened.Read(context.Background(), v.Intent.ID)
				published := stage == "after_publish" || stage == "before_directory_sync" || stage == "after_directory_sync"
				if component == "commitment" && published || component == "event" && !published {
					if !errors.Is(err, ErrReconcile) {
						t.Fatal("orphan commitment did not require reconciliation", err)
					}
					before := disk(t, filepath.Join(j.directory(), v.Intent.ID))
					if _, err = reopened.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now()); !errors.Is(err, ErrReconcile) {
						t.Fatal("retry replaced an incomplete committed event", err)
					}
					if !reflect.DeepEqual(before, disk(t, filepath.Join(j.directory(), v.Intent.ID))) {
						t.Fatal("reconciliation discarded or rewrote evidence")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if (read.Status == "confirmed") != published {
					t.Fatal("publication state lost", read.Status)
				}
				read, err = reopened.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now())
				if err != nil || read.Revision != 1 || read.Status != "confirmed" {
					t.Fatal("retry did not return one original confirmation", err)
				}
			})
		}
	}
}

func TestJournalClaimAckLossDoesNotAuthorizeReplay(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := confirmed(t, j, p, ids)
	failure := errors.New("claim response lost")
	j.fault = func(point string) error {
		if point == recordFile(2)+":after_publish" {
			return failure
		}
		return nil
	}
	var returnedClaim string
	_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
		var err error
		returnedClaim, err = s.claim(context.Background(), ids[0], time.Now())
		return err
	})
	if !errors.Is(err, failure) || returnedClaim != "" {
		t.Fatal("uncertain claim returned execution capability", err)
	}
	j.fault = nil
	view, err := j.Read(context.Background(), v.Intent.ID)
	if err != nil || view.Items[0].Stage != "claimed" || !view.NeedsReconciliation {
		t.Fatal("claim missing after lost response", err)
	}
	_, err = j.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[0], time.Now()); return err })
	if !errors.Is(err, ErrReconcile) {
		t.Fatal("unknown claim executed again", err)
	}
}

func TestJournalInstancesSerializeConfirmationAndClaim(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := prepared(t, j, p, ids)
	other, _ := NewJournal(j.control, j.project)
	var group sync.WaitGroup
	errorsOut := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			store := j
			if i%2 == 1 {
				store = other
			}
			_, err := store.Confirm(context.Background(), v.Intent.ID, v.IntentSHA, p.snapshot.Authority, time.Now())
			errorsOut <- err
		}(i)
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	v, err := j.Read(context.Background(), v.Intent.ID)
	if err != nil || v.Revision != 1 {
		t.Fatal("duplicate confirmations", err)
	}
	var claimed atomic.Int32
	for _, store := range []*Journal{j, other} {
		group.Add(1)
		go func(store *Journal) {
			defer group.Done()
			_, err := store.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[0], time.Now()); return err })
			if err == nil {
				claimed.Add(1)
			} else if !errors.Is(err, ErrReconcile) {
				t.Error(err)
			}
		}(store)
	}
	group.Wait()
	if claimed.Load() != 1 {
		t.Fatal("multiple instances claimed one item")
	}
}

func TestJournalStopDoesNotWaitForMovementLockAndBlocksNextClaim(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := confirmed(t, j, p, ids)
	other, _ := NewJournal(j.control, j.project)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := j.change(ctx, v.Intent.ID, func(s *journalSession) error {
		result := make(chan error, 1)
		go func() { _, err := other.RequestStop(ctx, v.Intent.ID, v.IntentSHA, time.Now()); result <- err }()
		if err := <-result; err != nil {
			return err
		}
		_, err := s.claim(ctx, ids[0], time.Now())
		if !errors.Is(err, ErrStopped) {
			return errors.New("next claim ignored stop marker")
		}
		return s.append(journalRecord{Kind: "stopped", At: time.Now().UTC().Format(time.RFC3339Nano), Reason: "用户停止"})
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := j.Read(context.Background(), v.Intent.ID)
	if err != nil || !read.StopRequested || read.Status != "stopped" || read.Items[0].Stage != "pending" {
		t.Fatal("stop not durable", err)
	}
}

func TestJournalRejectsDamagedSpecialAndLinkedRecords(t *testing.T) {
	for _, kind := range []string{"duplicate-json", "missing-intent", "sequence-gap", "symlink", "fifo", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			j, p, ids := journalFixture(t)
			v := confirmed(t, j, p, ids)
			path := filepath.Join(j.directory(), v.Intent.ID, "intent.json")
			switch kind {
			case "duplicate-json":
				data, _ := os.ReadFile(path)
				data = []byte(strings.Replace(string(data), `"schema":1`, `"schema":1,"schema":1`, 1))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-intent":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "sequence-gap":
				_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error { _, err := s.claim(context.Background(), ids[0], time.Now()); return err })
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(filepath.Dir(path), recordFile(1))); err != nil {
					t.Fatal(err)
				}
			case "symlink", "fifo", "hardlink":
				data, _ := os.ReadFile(path)
				other := filepath.Join(filepath.Dir(j.control), "other-record")
				if err := os.WriteFile(other, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(other, path)
				} else if kind == "hardlink" {
					err = os.Link(other, path)
				} else {
					err = syscall.Mkfifo(path, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := j.Read(ctx, v.Intent.ID); err == nil {
				t.Fatal("damaged record accepted")
			}
			if _, err := j.Prepare(ctx, v.Intent.ID, p, ids); err == nil {
				t.Fatal("damaged operation overwritten")
			}
		})
	}
}

func TestJournalRejectsMismatchedPhysicalAndMemoryReceipts(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := confirmed(t, j, p, ids)
	_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
		claim, err := s.claim(context.Background(), ids[0], time.Now())
		if err != nil {
			return err
		}
		row, _ := selectedRow(s.view, ids[0])
		receipt := pretendReceipt(row)
		receipt.After.Path = filepath.Join(filepath.Dir(j.control), "outside")
		if err := s.physical(ids[0], claim, receipt, time.Now()); !errors.Is(err, ErrJournal) {
			return errors.New("foreign physical target accepted")
		}
		receipt = pretendReceipt(row)
		if err := s.physical(ids[0], claim, receipt, time.Now()); err != nil {
			return err
		}
		memory := protocolMemory(s.view, claim)
		memory.ProjectID = "other"
		if err := s.memory(ids[0], claim, memory, time.Now()); !errors.Is(err, ErrJournal) {
			return errors.New("foreign memory accepted")
		}
		if err := s.append(journalRecord{Kind: "completed", At: time.Now().UTC().Format(time.RFC3339Nano)}); !errors.Is(err, ErrJournal) {
			return errors.New("completed with pending memory")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := j.Read(context.Background(), v.Intent.ID)
	if err != nil || view.Items[0].Stage != "memory_pending" {
		t.Fatal("bad receipt changed state", err)
	}
}

func TestRenameAtExclusiveAnchorsParentsAndPreservesExisting(t *testing.T) {
	base := t.TempDir()
	from := filepath.Join(base, "from")
	to := filepath.Join(base, "to")
	outside := filepath.Join(base, "outside")
	write(t, filepath.Join(from, "one"), "original")
	write(t, filepath.Join(to, "occupied"), "keep")
	write(t, filepath.Join(outside, "marker"), "outside")
	f, err := openDirectory(from)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := openDirectory(to)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := renameAtExclusive(f, "one", d, "occupied"); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing target not protected", err)
	}
	if err := os.Rename(to, to+"-held"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, to); err != nil {
		t.Fatal(err)
	}
	if err := renameAtExclusive(f, "one", d, "one"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(to+"-held", "one")); err != nil || string(data) != "original" {
		t.Fatal("opened capability lost")
	}
	if _, err := os.Stat(filepath.Join(outside, "one")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rename followed replaced ancestor")
	}
	if data, _ := os.ReadFile(filepath.Join(to+"-held", "occupied")); string(data) != "keep" {
		t.Fatal("overwrote existing bytes")
	}
}

func TestJournalCanonicalDecoderAndLongOperationIDs(t *testing.T) {
	var record journalRecord
	for _, data := range []string{`{"schema":1,"schema":1}`, `{} {}`, `{"foreign":true}`} {
		if decodeCanonical([]byte(data), &record) == nil {
			t.Fatal("ambiguous record accepted")
		}
	}
	value := journalRecord{Schema: 1, Kind: "confirmed", Revision: 1}
	data, _ := json.Marshal(value)
	if err := decodeCanonical(data, &record); err != nil {
		t.Fatal(err)
	}
	if len(memoryOperationID(strings.Repeat("x", 128), strings.Repeat("a", 32))) > 128 {
		t.Fatal("memory id exceeds project ledger contract")
	}
}
