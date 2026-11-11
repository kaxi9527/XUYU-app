//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

// ExecuteUndo is explicit user work. Reopening an operation never invokes it.
// A claimed or uncertain reverse move is not retried; only its durable memory
// receipt may be repaired without invoking a native file operation again.
func (e *Executor) executeUndo(ctx context.Context, id, intentSHA, groupID, manifestSHA string) (JournalView, error) {
	return e.journal.change(ctx, id, func(s *journalSession) error {
		index := undoGroupIndex(s.view, groupID)
		if s.view.IntentSHA != intentSHA || index < 0 || s.view.UndoGroups[index].ManifestSHA != manifestSHA {
			return ErrConsent
		}
		group := func() UndoGroup { return s.view.UndoGroups[index] }
		for _, item := range group().Items {
			if item.Stage == "memory_pending" {
				if err := e.saveUndoMemory(s, group(), item); err != nil {
					return err
				}
			}
		}
		if undoUnsettled(s.view) {
			return ErrReconcile
		}
		if group().Status == "completed" || group().Status == "stopped" {
			return nil
		}
		plan, ok := s.view.undoPlans[groupID]
		if !ok {
			return ErrJournal
		}
		pin, err := e.journal.pinUndoPlan(ctx, plan, manifestSHA)
		if err != nil {
			return err
		}
		defer pin.root.Close()
		round := &executionRound{executor: e, session: s, expected: map[string]string{}}
		for _, dir := range plan.Directories {
			round.expected[dir.Path] = dir.Object
		}
		appendRecord := func(record journalRecord) error {
			record.At = time.Now().UTC().Format(time.RFC3339Nano)
			record.Undo = &undoTransition{GroupID: groupID, ManifestSHA: manifestSHA}
			return s.append(record)
		}
		stop := func(cause error) error {
			if err := appendRecord(journalRecord{Kind: "undo_stopped", Reason: executionReason("已停止恢复：", cause)}); err != nil {
				return err
			}
			return cause
		}
		for _, item := range group().Items {
			if item.Stage != "pending" {
				continue
			}
			if err = e.undoReady(ctx, s.view, group(), pin); err != nil {
				return stop(err)
			}
			row, ok := undoRow(plan, item.FileID)
			if !ok {
				return ErrJournal
			}
			if _, err = e.undoCurrent(ctx, s.view, row); err != nil {
				if !errors.Is(err, ErrChanged) {
					return stop(err)
				}
				if err = appendRecord(journalRecord{Kind: "undo_conflict", FileID: item.FileID, Reason: "文件记录已变化，请重新查看后再恢复"}); err != nil {
					return err
				}
				continue
			}
			reverse, err := reverseRow(s.view, row)
			if err != nil {
				return err
			}
			p, err := round.preflight(ctx, reverse)
			if err == nil {
				err = undoSourceUnchanged(row, p)
			}
			if err != nil {
				p.close()
				if ctx.Err() != nil {
					return stop(ctx.Err())
				}
				if err = appendRecord(journalRecord{Kind: "undo_conflict", FileID: item.FileID, Reason: executionReason("保留原处：", err)}); err != nil {
					return err
				}
				continue
			}
			claim, err := randomID()
			if err != nil {
				p.close()
				return err
			}
			if err = appendRecord(journalRecord{Kind: "undo_claimed", FileID: item.FileID, Claim: claim}); err != nil {
				p.close()
				return err
			}
			if err = e.fail("undo_after_claim"); err != nil {
				p.close()
				return err
			}
			receipt, actionErr := round.performUndo(ctx, group(), row, reverse, p, pin)
			if err = e.fail("undo_after_action"); err != nil {
				return err
			}
			if err = appendRecord(journalRecord{Kind: "undo_physical", FileID: item.FileID, Claim: claim, Receipt: &receipt}); err != nil {
				return err
			}
			if actionErr != nil {
				if receipt.Outcome == "uncertain" {
					return actionErr
				}
				return stop(actionErr)
			}
			if err = e.fail("undo_after_physical"); err != nil {
				return err
			}
			current := group().Items[undoItemIndex(group(), item.FileID)]
			if err = e.saveUndoMemory(s, group(), current); err != nil {
				return err
			}
		}
		if err = e.undoReady(ctx, s.view, group(), pin); err != nil {
			return stop(err)
		}
		return appendRecord(journalRecord{Kind: "undo_completed"})
	})
}

func (e *Executor) undoReady(ctx context.Context, v JournalView, g UndoGroup, pin *undoPlanPin) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requested, err := e.journal.readUndoStop(ctx, v, g)
	if err != nil {
		return err
	}
	if requested {
		return ErrStopped
	}
	confirmed, err := time.Parse(time.RFC3339Nano, g.ConfirmedAt)
	if err != nil || time.Now().Before(confirmed) || !time.Now().Before(confirmed.Add(2*time.Hour)) {
		return ErrConsent
	}
	if err = pin.check(); err != nil {
		return err
	}
	_, err = e.recoveryAuthority(ctx, v)
	return err
}

func (e *Executor) undoCurrent(ctx context.Context, v JournalView, row UndoRow) (uint64, error) {
	memory, err := e.recoveryAuthority(ctx, v)
	if err != nil {
		return 0, err
	}
	if !undoMemoryReference(memory.State, row.Original) {
		return 0, ErrChanged
	}
	for _, file := range memory.State.Files {
		if file.ID == row.FileID && reflect.DeepEqual(file, row.ExpectedFile) {
			return memory.State.Revision, nil
		}
	}
	return 0, ErrChanged
}

func undoSourceUnchanged(row UndoRow, p *preparedFile) error {
	if p == nil || p.missing || row.Source.Fact == nil {
		return ErrChanged
	}
	change := changeTime(p.before)
	if uint32(p.before.Mode()) != row.Source.Mode || fmt.Sprintf("%d:%d", change.Sec, change.Nsec) != row.Source.ChangedAt {
		return ErrChanged
	}
	return nil
}

func (r *executionRound) performUndo(ctx context.Context, g UndoGroup, row UndoRow, reverse Row, p *preparedFile, pin *undoPlanPin) (PhysicalReceipt, error) {
	defer p.close()
	failure := func(outcome string, err error) (PhysicalReceipt, error) {
		return PhysicalReceipt{Outcome: outcome, Before: reverse.Before, SourceObject: reverse.Object, Reason: executionReason("本项未报告恢复完成，保留现场：", err)}, err
	}
	if err := r.executor.fail("undo_before_action"); err != nil {
		return failure("unchanged", err)
	}
	if err := r.executor.undoReady(ctx, r.session.view, g, pin); err != nil {
		return failure("unchanged", err)
	}
	revision, err := r.executor.undoCurrent(ctx, r.session.view, row)
	if err != nil {
		return failure("unchanged", err)
	}
	for _, pair := range []struct {
		path string
		root *os.Root
	}{{filepath.Dir(reverse.Source), p.source}, {filepath.Dir(reverse.Target), p.target}} {
		if err = boundDirectory(pair.path, pair.root); err != nil {
			return failure("unchanged", err)
		}
	}
	info, err := p.source.Lstat(filepath.Base(reverse.Source))
	if err != nil || !sameFile(info, p.before) {
		return failure("unchanged", ErrChanged)
	}
	if err = r.checkDirectoryObjects(p.builder); err != nil {
		return failure("unchanged", err)
	}
	if err = destinationFree(p.target, filepath.Base(reverse.Target)); err != nil {
		return failure("unchanged", err)
	}
	occurredAt := time.Now().UTC().Format(time.RFC3339Nano)
	err = renameAtExclusive(p.source, filepath.Base(reverse.Source), p.target, filepath.Base(reverse.Target))
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EXDEV) {
			return failure("unchanged", err)
		}
		return failure("uncertain", err)
	}
	if err = r.executor.fail("undo_after_native_action"); err != nil {
		return failure("uncertain", err)
	}
	// Once moved, cancellation only stops the next item. Finish this receipt.
	finish, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = syncRestoredFile(p.target, filepath.Base(reverse.Target), reverse.Object); err != nil {
		return failure("uncertain", err)
	}
	receipt, err := r.placed(finish, reverse, reverse.Object, p)
	if err != nil {
		return failure("uncertain", err)
	}
	if err = pin.check(); err != nil {
		return failure("uncertain", err)
	}
	receipt.MemoryRevision, receipt.OccurredAt = revision, occurredAt
	return receipt, nil
}

func syncRestoredFile(root *os.Root, name, wantObject string) error {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return err
	}
	key, _, single := object(before)
	if key != wantObject || !single || !before.Mode().IsRegular() {
		return ErrChanged
	}
	if err = f.Sync(); err != nil {
		return err
	}
	after, err := root.Lstat(name)
	if err != nil || !sameFile(before, after) {
		return ErrChanged
	}
	return nil
}

// Pin the checked immutable plan once, then detect replacement or writes at
// each action boundary without rehashing a large group for every single file.
type undoPlanPin struct {
	root  *os.Root
	path  string
	files map[string]os.FileInfo
}

func (j *Journal) pinUndoPlan(ctx context.Context, plan undoPlan, manifestSHA string) (*undoPlanPin, error) {
	control, err := openDirectory(j.control)
	if err != nil {
		return nil, err
	}
	defer control.Close()
	root, err := j.undoRoot(control, plan.Manifest.OperationID, plan.Manifest.GroupID, false)
	if err != nil {
		return nil, err
	}
	pin := &undoPlanPin{root: root, path: j.undoPath(plan.Manifest.OperationID, plan.Manifest.GroupID), files: map[string]os.FileInfo{}}
	checks := map[string]string{"manifest.json": manifestSHA}
	for i, chunk := range plan.Manifest.Chunks {
		checks[undoPartName(i)] = chunk.SHA
	}
	for name, sha := range checks {
		before, e := root.Lstat(name)
		if e != nil {
			root.Close()
			return nil, e
		}
		data, e := readJournalFile(ctx, root, name, maxJournalBytes)
		if e != nil {
			root.Close()
			return nil, e
		}
		after, e := root.Lstat(name)
		if e != nil || !sameFile(before, after) || sumBytes(data) != sha {
			root.Close()
			return nil, ErrChanged
		}
		pin.files[name] = after
	}
	if err = pin.check(); err != nil {
		root.Close()
		return nil, err
	}
	return pin, nil
}

func (p *undoPlanPin) check() error {
	if err := sameUndoRoot(p.path, p.root); err != nil {
		return err
	}
	for name, before := range p.files {
		after, err := p.root.Lstat(name)
		if err != nil || !sameFile(before, after) {
			return ErrChanged
		}
	}
	return nil
}
