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
	"testing"
	"time"
)

// This advances the state protocol only. Receipts remain synthetic and do not
// represent real placement or a real project-memory event.
func journalAtStage(t *testing.T, stage string) (*Journal, Preview, JournalView, []string) {
	t.Helper()
	j, p, ids := journalFixture(t)
	ids = ids[:1]
	v := confirmed(t, j, p, ids)
	if stage == "confirmed" {
		return j, p, v, ids
	}
	v, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
		claim, err := s.claim(context.Background(), ids[0], time.Now())
		if err != nil || stage == "claimed" {
			return err
		}
		row, _ := selectedRow(s.view, ids[0])
		if err = s.physical(ids[0], claim, pretendReceipt(row), time.Now()); err != nil || stage == "memory_pending" {
			return err
		}
		if err = s.memory(ids[0], claim, protocolMemory(s.view, claim), time.Now()); err != nil || stage == "moved" {
			return err
		}
		return s.append(journalRecord{Kind: "completed", At: time.Now().UTC().Format(time.RFC3339Nano)})
	})
	if err != nil {
		t.Fatal(err)
	}
	return j, p, v, ids
}

func TestJournalLostTailOrCommitmentNeverRewinds(t *testing.T) {
	for _, stage := range []string{"confirmed", "claimed", "memory_pending", "moved", "completed"} {
		for _, component := range []string{"event", "commitment"} {
			t.Run(stage+"/"+component, func(t *testing.T) {
				j, p, v, ids := journalAtStage(t, stage)
				name := recordFile(v.Revision)
				if component == "commitment" {
					name = commitmentFile(v.Revision)
				}
				dir := filepath.Join(j.directory(), v.Intent.ID)
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
				before := disk(t, dir)
				reopened, _ := NewJournal(j.control, j.project)
				if _, err := reopened.Read(context.Background(), v.Intent.ID); !errors.Is(err, ErrReconcile) {
					t.Fatal("missing acknowledged tail accepted", err)
				}
				entered := false
				if _, err := reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error { entered = true; return nil }); !errors.Is(err, ErrReconcile) || entered {
					t.Fatal("damaged tail allowed a transition", err)
				}
				if _, err := reopened.Prepare(context.Background(), v.Intent.ID, p, ids); !errors.Is(err, ErrReconcile) {
					t.Fatal("damaged operation reinitialized", err)
				}
				if !reflect.DeepEqual(before, disk(t, dir)) {
					t.Fatal("damage evidence changed")
				}
			})
		}
	}
}

func TestJournalCommitmentMustMatchRecordAndOperation(t *testing.T) {
	for _, field := range []string{"operation", "intent", "record", "previous", "revision"} {
		t.Run(field, func(t *testing.T) {
			j, _, v, _ := journalAtStage(t, "claimed")
			path := filepath.Join(j.directory(), v.Intent.ID, commitmentFile(v.Revision))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var c journalCommitment
			if err = json.Unmarshal(data, &c); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "operation":
				c.OperationID = "another-operation"
			case "intent":
				c.IntentSHA = strings.Repeat("e", 64)
			case "record":
				c.RecordSHA = strings.Repeat("e", 64)
			case "previous":
				c.PreviousSHA = strings.Repeat("e", 64)
			case "revision":
				c.Revision++
			}
			data, _ = json.Marshal(c)
			if err = os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err = j.Read(context.Background(), v.Intent.ID); !errors.Is(err, ErrJournal) {
				t.Fatal("mismatched commitment accepted", err)
			}
		})
	}
}

func TestJournalLostStopMarkerNeverAllowsAnotherClaim(t *testing.T) {
	for _, name := range []string{"stop.json", "stop-commit.json"} {
		t.Run(name, func(t *testing.T) {
			j, _, v, ids := journalAtStage(t, "confirmed")
			if stopped, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); err != nil || !stopped.StopRequested {
				t.Fatal("initial durable stop failed", err)
			}
			if err := os.Remove(filepath.Join(j.directory(), v.Intent.ID, name)); err != nil {
				t.Fatal(err)
			}
			reopened, _ := NewJournal(j.control, j.project)
			var claim string
			_, err := reopened.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
				var err error
				claim, err = s.claim(context.Background(), ids[0], time.Now())
				return err
			})
			if err == nil || claim != "" {
				t.Fatal("lost acknowledged stop allowed another claim", err)
			}
			if _, err := reopened.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); !errors.Is(err, ErrReconcile) {
				t.Fatal("lost stop reconstructed by a new request", err)
			}
		})
	}
}

func TestJournalInFlightSessionDetectsStopPairLoss(t *testing.T) {
	for _, name := range []string{"stop.json", "stop-commit.json"} {
		t.Run(name, func(t *testing.T) {
			j, _, v, ids := journalAtStage(t, "confirmed")
			_, err := j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
				if _, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); err != nil {
					return err
				}
				if err := os.Remove(filepath.Join(j.directory(), v.Intent.ID, name)); err != nil {
					return err
				}
				claim, err := s.claim(context.Background(), ids[0], time.Now())
				if claim != "" {
					t.Fatal("live session ignored a lost stop pair")
				}
				return err
			})
			if !errors.Is(err, ErrReconcile) {
				t.Fatal("live session did not require reconciliation", err)
			}
		})
	}
}

func TestJournalStopPairPublicationWindows(t *testing.T) {
	for _, component := range []string{"stop-commit.json", "stop.json"} {
		for _, stage := range []string{"before_create", "before_file_sync", "before_publish", "after_publish", "before_directory_sync", "after_directory_sync"} {
			t.Run(component+"/"+stage, func(t *testing.T) {
				j, _, v, _ := journalAtStage(t, "confirmed")
				failure := errors.New("stop persistence failure")
				j.fault = func(point string) error {
					if point == component+":"+stage {
						return failure
					}
					return nil
				}
				if _, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); !errors.Is(err, failure) {
					t.Fatal("stop fault not reached", err)
				}
				j.fault = nil
				published := stage == "after_publish" || stage == "before_directory_sync" || stage == "after_directory_sync"
				incomplete := component == "stop-commit.json" && published || component == "stop.json" && !published
				before := disk(t, filepath.Join(j.directory(), v.Intent.ID))
				read, err := j.Read(context.Background(), v.Intent.ID)
				if incomplete {
					if !errors.Is(err, ErrReconcile) {
						t.Fatal("partial stop treated as absent", err)
					}
					if _, err = j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); !errors.Is(err, ErrReconcile) {
						t.Fatal("partial stop replaced", err)
					}
					if !reflect.DeepEqual(before, disk(t, filepath.Join(j.directory(), v.Intent.ID))) {
						t.Fatal("partial stop evidence rewritten")
					}
					return
				}
				if err != nil || read.StopRequested != published {
					t.Fatal("incorrect original stop state", err)
				}
				if got, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); err != nil || !got.StopRequested {
					t.Fatal("complete/no-publication stop could not be acknowledged", err)
				}
				if published && !reflect.DeepEqual(before, disk(t, filepath.Join(j.directory(), v.Intent.ID))) {
					t.Fatal("original stop receipt rewritten")
				}
			})
		}
	}
}

func TestJournalConcurrentStopRequestsKeepOneOriginalPair(t *testing.T) {
	j, _, v, _ := journalAtStage(t, "confirmed")
	const callers = 12
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrReconcile) {
			t.Fatal("unexpected concurrent stop error", err)
		}
	}
	if success == 0 {
		t.Fatal("no stop request completed")
	}
	before := disk(t, filepath.Join(j.directory(), v.Intent.ID))
	got, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now().Add(time.Hour))
	if err != nil || !got.StopRequested || got.Revision != v.Revision {
		t.Fatal("concurrent pair not readable", err)
	}
	if !reflect.DeepEqual(before, disk(t, filepath.Join(j.directory(), v.Intent.ID))) {
		t.Fatal("retry replaced original stop timestamp")
	}
}

func TestJournalStopRetriesAndTerminalRepliesRequireDurableDirectory(t *testing.T) {
	for _, stage := range []string{"confirmed", "stopped", "completed"} {
		t.Run(stage, func(t *testing.T) {
			initial := stage
			if stage == "stopped" {
				initial = "confirmed"
			}
			j, _, v, _ := journalAtStage(t, initial)
			if stage == "stopped" {
				var err error
				v, err = j.change(context.Background(), v.Intent.ID, func(s *journalSession) error {
					return s.append(journalRecord{Kind: "stopped", At: time.Now().UTC().Format(time.RFC3339Nano), Reason: "用户停止"})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("directory synchronization unavailable")
			j.fault = func(point string) error {
				if point == "stop.json:before_directory_sync" || point == "stop-ack:before_directory_sync" {
					return failure
				}
				return nil
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now()); !errors.Is(err, failure) {
					t.Fatal("stop acknowledged without a directory barrier", attempt, err)
				}
			}
			before := disk(t, filepath.Join(j.directory(), v.Intent.ID))
			j.fault = nil
			got, err := j.RequestStop(context.Background(), v.Intent.ID, v.IntentSHA, time.Now())
			if err != nil || got.Revision != v.Revision || (stage == "confirmed" && !got.StopRequested) {
				t.Fatal("stop receipt not restored after barrier recovered", err)
			}
			if !reflect.DeepEqual(before, disk(t, filepath.Join(j.directory(), v.Intent.ID))) {
				t.Fatal("retry rewrote original stop or terminal receipt")
			}
		})
	}
}
