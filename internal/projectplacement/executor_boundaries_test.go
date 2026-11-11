//go:build darwin && cgo

package projectplacement

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestExecutorLateCopyReceiptPreservesObservedAbsence(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry-%t", retry), func(t *testing.T) {
			e, view, preview := executorFixture(t, true, "project/a.txt")
			ctx := context.Background()
			row := preview.snapshot.Rows[0]
			baseline, err := e.memory.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("lost physical receipt response")
			observed := false
			e.fault = func(point string) error {
				if point == "after_physical" && retry {
					return failure
				}
				if point != "after_action" || observed {
					return nil
				}
				observed = true
				if err := os.Remove(row.Target); err != nil {
					return err
				}
				current, err := e.memory.Read(ctx)
				if err != nil {
					return err
				}
				// Even equal observation timestamps must not hide a new committed
				// observation. The source is unchanged; the new target is absent.
				stamp := baseline.State.LastObservedAt
				observation := projectmemory.Observation{ScopeSHA: current.State.Scope.Digest(), StartedAt: stamp, EndedAt: stamp, Complete: true}
				for _, path := range append(append([]string{}, current.State.Scope.Roots...), current.State.Scope.Files...) {
					observation.Coverage = append(observation.Coverage, projectmemory.Coverage{Path: path, Status: "complete"})
				}
				for _, file := range current.State.Files {
					observation.Files = append(observation.Files, file.Fact)
				}
				_, err = e.memory.RecordObservation(ctx, current.State.Revision, "observed-copy-removed", observation)
				return err
			}
			got, err := e.Execute(ctx, view.Intent.ID, view.IntentSHA)
			var original *PhysicalReceipt
			if retry {
				if !errors.Is(err, failure) || got.Items[0].Stage != "memory_pending" {
					t.Fatal("missing durable result", err)
				}
				original = got.Items[0].Receipt
				e.fault = nil
				e.journal, err = NewJournal(e.journal.control, e.journal.project)
				if err != nil {
					t.Fatal(err)
				}
				got, err = e.Execute(ctx, view.Intent.ID, view.IntentSHA)
			}
			if err != nil || !observed || !got.StopRequested {
				t.Fatal("late observation not respected", err)
			}
			if original != nil && !reflect.DeepEqual(original, got.Items[0].Receipt) {
				t.Fatal("retry refreshed action boundary")
			}
			if got.Items[0].Receipt.MemoryRevision != baseline.State.Revision {
				t.Fatal("wrong pre-action revision")
			}
			current, err := e.memory.Read(ctx)
			if err != nil || !reflect.DeepEqual(current.State.Files, baseline.State.Files) || len(current.State.Placements) != 1 || current.State.Placements[0].Disposition != "history_only" {
				t.Fatal("late copy receipt recreated an absent target", err)
			}
			if _, err := os.Stat(row.Target); !os.IsNotExist(err) {
				t.Fatal("retry recreated physical target", err)
			}
			if data, err := os.ReadFile(row.Source); err != nil || string(data) != "example" {
				t.Fatal("source changed", err)
			}
		})
	}
}

func TestExecutorChangedFileAndOccupiedTargetPreserveFiles(t *testing.T) {
	for _, kind := range []string{"source-changed", "target-appeared"} {
		t.Run(kind, func(t *testing.T) {
			e, v, p := executorFixture(t, false, "project/a.txt")
			row := p.snapshot.Rows[0]
			if kind == "source-changed" {
				write(t, row.Source, "changed")
			} else {
				e.fault = func(point string) error {
					if point == "before_action" {
						write(t, row.Target, "foreign")
					}
					return nil
				}
			}
			got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if err == nil || got.Status != "stopped" {
				t.Fatal("drift did not stop file work", err)
			}
			data, err := os.ReadFile(row.Source)
			want := "example"
			if kind == "source-changed" {
				want = "changed"
			}
			if err != nil || string(data) != want {
				t.Fatal("drifted source was moved or overwritten", err)
			}
			if kind == "target-appeared" {
				data, err = os.ReadFile(row.Target)
				if err != nil || string(data) != "foreign" {
					t.Fatal("occupied destination overwritten", err)
				}
			}
			memory, err := e.memory.Read(context.Background())
			if err != nil || len(memory.State.Placements) != 0 {
				t.Fatal("unperformed action became executed history", err)
			}
		})
	}
}

func TestExecutorRejectsDirectoryRebindingBeforeNativeAction(t *testing.T) {
	e, v, p := executorFixture(t, false, "project/a.txt")
	project := p.snapshot.Authority.Project.Path
	foreign := filepath.Join(p.snapshot.Authority.Boundaries[0], "foreign")
	write(t, filepath.Join(foreign, "marker.txt"), "unchanged")
	e.fault = func(point string) error {
		if point == "before_action" {
			if err := os.Rename(project, project+"-moved"); err != nil {
				return err
			}
			return os.Symlink(foreign, project)
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err == nil || got.Status != "stopped" {
		t.Fatal("rebound source directory accepted", err)
	}
	data, err := os.ReadFile(filepath.Join(project+"-moved", "a.txt"))
	if err != nil || string(data) != "example" {
		t.Fatal("anchored source changed", err)
	}
	if !reflect.DeepEqual(disk(t, foreign), map[string]string{".": "directory", "marker.txt": sum([]byte("unchanged"))}) {
		t.Fatal("external replacement received a file")
	}
}

func TestExecutorRevocationAfterMoveRecordsHistoryAndStopsRemaining(t *testing.T) {
	e, v, _ := executorFixture(t, false, "project/a.txt", "project/b.txt")
	first, _ := selectedRow(v, v.Items[0].FileID)
	second, _ := selectedRow(v, v.Items[1].FileID)
	revoked := false
	e.fault = func(point string) error {
		if point == "after_action" && !revoked {
			revoked = true
			current, err := e.memory.Read(context.Background())
			if err != nil {
				return err
			}
			_, err = e.memory.SetTracking(context.Background(), current.State.Revision, "pause-during-placement", current.State.Scope, false, time.Now())
			return err
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if !errors.Is(err, ErrStopped) || got.Status != "stopped" || !got.StopRequested || got.Items[0].Memory == nil || got.Items[1].Stage != "pending" {
		t.Fatal("revocation did not stop after historical receipt", got.Status, err)
	}
	if _, err = os.Lstat(first.Target); err != nil {
		t.Fatal("completed first move missing", err)
	}
	if _, err = os.Lstat(second.Source); err != nil {
		t.Fatal("remaining file moved after revocation", err)
	}
	memory, err := e.memory.Read(context.Background())
	if err != nil || memory.State.Enabled || len(memory.State.Placements) != 1 || memory.State.Placements[0].Disposition != "history_only" {
		t.Fatal("revoked scope lost history or reactivated tracking", err)
	}
}

func TestExecutorObservationAfterActionDoesNotSplitIdentities(t *testing.T) {
	for _, copyPlan := range []bool{false, true} {
		t.Run(fmt.Sprint("copy=", copyPlan), func(t *testing.T) {
			e, v, _ := executorFixture(t, copyPlan, "project/a.txt")
			e.fault = func(point string) error {
				if point == "after_action" {
					current, err := e.memory.Read(context.Background())
					if err != nil {
						return err
					}
					_, err = e.memory.Observe(context.Background(), current.State.Revision, "observe-after-action", current.State.Scope.Digest())
					return err
				}
				return nil
			}
			got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if err != nil || got.Status != "completed" {
				t.Fatal("observed actual file action failed", err)
			}
			memory, err := e.memory.Read(context.Background())
			if err != nil || len(memory.State.Placements) != 1 || memory.State.Placements[0].Disposition != "already_observed" {
				t.Fatal("execution overwrote observer evidence", err)
			}
			link := memory.State.Placements[0]
			count := 0
			for _, file := range memory.State.Files {
				if file.ID == link.TargetFileID {
					count++
					if file.Fact.Path != link.To {
						t.Fatal("target identity points elsewhere")
					}
				}
			}
			if count != 1 {
				t.Fatal("target identity duplicated")
			}
		})
	}
}

func TestExecutorCopyPreservesModeAndExtendedAttribute(t *testing.T) {
	e, v, p := executorFixture(t, true, "project/a.txt")
	row := p.snapshot.Rows[0]
	if err := os.Chmod(row.Source, 0o640); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/xattr", "-w", "com.workshelf.test", "metadata-fixture", row.Source).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || got.Status != "completed" {
		t.Fatal(err)
	}
	info, err := os.Stat(row.Target)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatal("copy permissions lost", err)
	}
	out, err := exec.Command("/usr/bin/xattr", "-p", "com.workshelf.test", row.Target).Output()
	if err != nil || string(out) != "metadata-fixture\n" {
		t.Fatal("copy extended attribute lost", err, string(out))
	}
}

func TestExecutorCopyResidueIsNotReplayedOrDeleted(t *testing.T) {
	for _, stage := range []string{"copy_before_ready", "copy_before_publish"} {
		t.Run(stage, func(t *testing.T) {
			e, v, p := executorFixture(t, true, "project/a.txt")
			row := p.snapshot.Rows[0]
			e.fault = func(point string) error {
				if point != stage {
					return nil
				}
				if stage == "copy_before_ready" {
					return errors.New("copy staging response lost")
				}
				current, err := e.journal.Read(context.Background(), v.Intent.ID)
				if err != nil {
					return err
				}
				write(t, current.Items[0].Copy.Path, "changed")
				return nil
			}
			got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if err == nil || got.Items[0].Stage != "uncertain" || got.Items[0].Copy == nil {
				t.Fatal("copy residue falsely completed", err)
			}
			if _, err = os.Lstat(row.Target); !os.IsNotExist(err) {
				t.Fatal("unverified staged object published", err)
			}
			data, err := os.ReadFile(row.Source)
			if err != nil || string(data) != "example" {
				t.Fatal("copy lost source", err)
			}
			before := disk(t, p.snapshot.Authority.Project.Path)
			e.fault = nil
			if _, err = e.Execute(context.Background(), v.Intent.ID, v.IntentSHA); !errors.Is(err, ErrReconcile) {
				t.Fatal("uncertain copy replayed", err)
			}
			if !reflect.DeepEqual(before, disk(t, p.snapshot.Authority.Project.Path)) {
				t.Fatal("copy residue removed or altered")
			}
		})
	}
}

func TestExecutorOpenFileHelper(t *testing.T) {
	path := os.Getenv("WORKSHELF_EXECUTOR_OPEN_FIXTURE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestExecutorDetectsAnotherProcessHoldingSource(t *testing.T) {
	e, v, p := executorFixture(t, false, "project/a.txt")
	row := p.snapshot.Rows[0]
	if err := checkActive(context.Background(), row.Source); err != nil {
		t.Fatal("probe unavailable before helper", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutorOpenFileHelper$")
	cmd.Env = append(os.Environ(), "WORKSHELF_EXECUTOR_OPEN_FIXTURE="+row.Source)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Error("owned file-holder child did not exit cleanly", err)
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "READY\n" {
		t.Fatal("helper not ready", err, line)
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if !errors.Is(err, ErrActive) || got.Status != "stopped" {
		t.Fatal("active source was not preserved", err)
	}
	if _, err = os.Lstat(row.Source); err != nil {
		t.Fatal("active source moved", err)
	}
}

func TestExecutorChecksCurrentTemplateAndUserRules(t *testing.T) {
	for _, kind := range []string{"user-rule", "template", "agent-note"} {
		t.Run(kind, func(t *testing.T) {
			e, v, p := executorFixture(t, false, "project/a.txt")
			if kind == "template" {
				old := e.current
				e.current = func(ctx context.Context, intent Intent) (ExecutionAuthority, error) {
					a, err := old(ctx, intent)
					a.Profile.Name = "另一模板"
					return a, err
				}
			} else {
				current, err := e.memory.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				note := projectmemory.Note{Kind: "user_decision", Text: "先不要把文档放入资料目录"}
				if kind == "agent-note" {
					note.Kind = "agent_proposal"
					note.Agent = "assistant"
					note.Model = "other-model"
					note.Text = "新模型已读取项目上下文"
				}
				if _, err = e.memory.AddNote(context.Background(), current.State.Revision, "new-note", note, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
			if kind == "agent-note" {
				if err != nil || got.Status != "completed" {
					t.Fatal("agent note incorrectly changed user authorization", err)
				}
				return
			}
			if !errors.Is(err, ErrConsent) || got.Status != "stopped" {
				t.Fatal("changed policy still executed", err)
			}
			if _, err = os.Lstat(p.snapshot.Rows[0].Source); err != nil {
				t.Fatal("stale policy moved source", err)
			}
		})
	}
}

func TestExecutorProjectionFailureKeepsMemoryPendingUntilReadbackRepairs(t *testing.T) {
	e, v, p := executorFixture(t, false, "project/a.txt")
	index := filepath.Join(e.journal.control, "memory", e.journal.project.ID, "index.json")
	backup := index + ".fixture-backup"
	e.fault = func(point string) error {
		if point == "before_memory" {
			if err := os.Rename(index, backup); err != nil {
				return err
			}
			return os.Mkdir(index, 0o700)
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if !errors.Is(err, ErrReconcile) || got.Items[0].Stage != "memory_pending" {
		t.Fatal("unreadable projection acknowledged", err)
	}
	current, err := e.memory.Read(context.Background())
	if err != nil || len(current.State.Placements) != 1 || !current.ProjectionPending {
		t.Fatal("actual event missing behind projection failure", err)
	}
	if err = os.Remove(index); err != nil {
		t.Fatal(err)
	} // This fixture-created empty directory only.
	if err = os.Rename(backup, index); err != nil {
		t.Fatal(err)
	}
	e.fault = nil
	got, err = e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err != nil || got.Status != "completed" {
		t.Fatal("projection retry did not complete original receipt", err)
	}
	after, err := e.memory.Read(context.Background())
	if err != nil || after.ProjectionPending || len(after.State.Placements) != 1 {
		t.Fatal("projection retry duplicated history", err)
	}
	if _, err = os.Lstat(p.snapshot.Rows[0].Source); !os.IsNotExist(err) {
		t.Fatal("memory retry restored or recopied source")
	}
}

func TestExecutorCopyRechecksRevocationImmediatelyBeforePublication(t *testing.T) {
	e, v, p := executorFixture(t, true, "project/a.txt")
	row := p.snapshot.Rows[0]
	e.fault = func(point string) error {
		if point == "copy_before_publish" {
			current, err := e.memory.Read(context.Background())
			if err != nil {
				return err
			}
			_, err = e.memory.SetTracking(context.Background(), current.State.Revision, "pause-before-copy-publication", current.State.Scope, false, time.Now())
			return err
		}
		return nil
	}
	got, err := e.Execute(context.Background(), v.Intent.ID, v.IntentSHA)
	if err == nil || got.Items[0].Stage != "uncertain" {
		t.Fatal("copy published after final-stage scope revocation", err)
	}
	if _, err = os.Lstat(row.Target); !os.IsNotExist(err) {
		t.Fatal("copy was published after revocation", err)
	}
	if _, err = os.Lstat(row.Source); err != nil {
		t.Fatal("copy lost original after revocation", err)
	}
}
