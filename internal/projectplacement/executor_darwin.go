//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (e *Executor) execute(ctx context.Context, id, intentSHA string) (JournalView, error) {
	return e.journal.change(ctx, id, func(s *journalSession) error {
		if s.view.IntentSHA != intentSHA {
			return ErrConsent
		}
		for _, item := range s.view.Items {
			if item.Stage == "reconciliation_pending" {
				if err := e.saveRecoveryMemory(s, item); err != nil {
					return err
				}
			}
			if item.Stage == "memory_pending" {
				if err := e.saveMemory(s, item); err != nil {
					return err
				}
			}
		}
		if unsettled(s.view) {
			return ErrReconcile
		}
		if s.view.Status == "completed" || s.view.Status == "stopped" {
			return nil
		}
		if s.view.Status == "prepared" {
			return ErrConsent
		}
		r := &executionRound{executor: e, session: s, expected: map[string]string{}}
		for _, d := range s.view.Intent.Preview.Directories {
			r.expected[d.Path] = d.Object
		}
		for _, item := range s.view.Items {
			if item.Receipt != nil {
				for _, d := range item.Receipt.Directories {
					if old := r.expected[d.Path]; old != "" && old != d.Object {
						return ErrJournal
					}
					r.expected[d.Path] = d.Object
				}
			}
		}
		for _, item := range s.view.Items {
			if item.Stage == "moved" || item.Stage == "copied" {
				continue
			}
			if item.Stage != "pending" {
				return ErrReconcile
			}
			row, ok := selectedRow(s.view, item.FileID)
			if !ok {
				return ErrJournal
			}
			if err := s.checkStop(ctx); err != nil {
				return r.stop(err)
			}
			if err := e.checkCurrent(ctx, s.view, row); err != nil {
				return r.stop(err)
			}
			p, err := r.preflight(ctx, row)
			if err != nil {
				return r.stop(fmt.Errorf("%s：%w", filepath.Base(row.Source), err))
			}
			baseline, err := e.memory.Read(ctx)
			if err != nil {
				p.close()
				return r.stop(err)
			}
			claim, err := s.claim(ctx, row.FileID, time.Now())
			if err != nil {
				p.close()
				return err
			}
			if err = e.fail("after_claim"); err != nil {
				p.close()
				return err
			}
			if row.Action == "copy" {
				err = s.append(journalRecord{Kind: "copy_reserved", At: time.Now().UTC().Format(time.RFC3339Nano), FileID: row.FileID, Claim: claim, Copy: &CopyStage{Path: copyTemporary(row, claim)}})
				if err != nil {
					p.close()
					return err
				}
			}
			receipt, actionErr := r.perform(ctx, row, claim, p)
			receipt.MemoryRevision = baseline.State.Revision
			if err = e.fail("after_action"); err != nil {
				return err
			}
			if err = s.physical(row.FileID, claim, receipt, time.Now()); err != nil {
				return err
			}
			if actionErr != nil {
				return actionErr
			}
			if err = e.fail("after_physical"); err != nil {
				return err
			}
			current := s.view.Items[itemIndex(s.view, row.FileID)]
			if err = e.saveMemory(s, current); err != nil {
				return err
			}
		}
		return s.append(journalRecord{Kind: "completed", At: time.Now().UTC().Format(time.RFC3339Nano)})
	})
}

func (r *executionRound) stop(cause error) error {
	if err := r.session.append(journalRecord{Kind: "stopped", At: time.Now().UTC().Format(time.RFC3339Nano), Reason: executionReason("停止后续处理；尚未处理的文件保留原处：", cause)}); err != nil {
		return err
	}
	return cause
}

func (r *executionRound) perform(ctx context.Context, row Row, claim string, p *preparedFile) (PhysicalReceipt, error) {
	failure := func(outcome string, err error) (PhysicalReceipt, error) {
		return PhysicalReceipt{Outcome: outcome, Before: row.Before, SourceObject: row.Object, Directories: append([]Directory{}, r.created...), Reason: executionReason("本项未报告完成，保留现场：", err)}, err
	}
	if err := r.makeTargetDirectories(ctx, row, p); err != nil {
		p.close()
		return failure("unchanged", err)
	}
	p.close()
	var err error
	p, err = r.preflight(ctx, row)
	if err != nil {
		return failure("unchanged", err)
	}
	defer func() { p.close() }()
	if err = r.executor.fail("before_action"); err != nil {
		return failure("unchanged", err)
	}
	if err = r.actionReady(ctx, row, p); err != nil {
		return failure("unchanged", err)
	}
	wantObject := row.Object
	var occurredAt time.Time
	if row.Action == "copy" {
		wantObject, err = r.stageCopy(ctx, row, claim, p)
		if err != nil {
			return failure("uncertain", err)
		}
		p.close()
		p, err = r.preflight(ctx, row)
		if err != nil {
			return failure("uncertain", err)
		}
		if err = r.actionReady(ctx, row, p); err != nil {
			return failure("uncertain", err)
		}
		if err = r.executor.fail("copy_before_publish"); err != nil {
			return failure("uncertain", err)
		}
		temporary := filepath.Base(copyTemporary(row, claim))
		info, statErr := p.target.Lstat(temporary)
		if statErr != nil {
			return failure("uncertain", statErr)
		}
		key, _, single := object(info)
		if !info.Mode().IsRegular() || !single || key != wantObject || info.Size() != row.Before.Size {
			return failure("uncertain", ErrChanged)
		}
		sha, hashErr := p.builder.hash(p.target, temporary, info)
		r.hashed = p.builder.hashed
		if hashErr != nil {
			return failure("uncertain", hashErr)
		}
		if sha != row.Before.SHA256 {
			return failure("uncertain", ErrChanged)
		}
		// Hashing a staged copy can take time. Re-read cancellation, scope and
		// directory bindings again at its final publication boundary.
		if err = r.actionReady(ctx, row, p); err != nil {
			return failure("uncertain", err)
		}
		occurredAt = time.Now()
		err = renameAtExclusive(p.target, filepath.Base(copyTemporary(row, claim)), p.target, filepath.Base(row.Target))
	} else {
		occurredAt = time.Now()
		err = renameAtExclusive(p.source, filepath.Base(row.Source), p.target, filepath.Base(row.Target))
	}
	if err != nil {
		if row.Action == "move" && (errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EXDEV)) {
			return failure("unchanged", err)
		}
		return failure("uncertain", err)
	}
	if err = r.executor.fail("after_native_action"); err != nil {
		return failure("uncertain", err)
	}
	// Cancellation stops future actions. Once a native action ran, finish its
	// verification and durable receipt instead of abandoning that evidence.
	finish, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := r.placed(finish, row, wantObject, p)
	if err != nil {
		return failure("uncertain", err)
	}
	receipt.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
	return receipt, nil
}

func executionReason(prefix string, cause error) string {
	message := []rune(cause.Error())
	if len(message) > 350 {
		message = message[:350]
	}
	return prefix + string(message)
}

func (r *executionRound) stageCopy(ctx context.Context, row Row, claim string, p *preparedFile) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := filepath.Base(copyTemporary(row, claim))
	source, err := p.source.OpenFile(filepath.Base(row.Source), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil || !sameFile(before, p.before) {
		return "", ErrChanged
	}
	target, err := p.target.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return "", err
	}
	err = copyFileMetadataAndData(source, target)
	if err == nil {
		err = target.Sync()
	}
	closeErr := target.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	after, err := source.Stat()
	current, e2 := p.source.Lstat(filepath.Base(row.Source))
	if err != nil || e2 != nil || !sameFile(before, after) || !sameFile(before, current) {
		return "", ErrChanged
	}
	if err = syncDirectory(p.target); err != nil {
		return "", err
	}
	info, err := p.target.Lstat(name)
	if err != nil {
		return "", err
	}
	key, _, single := object(info)
	if !info.Mode().IsRegular() || !single || key == "" || key == row.Object || info.Size() != row.Before.Size {
		return "", ErrChanged
	}
	b, err := r.builder(ctx)
	if err != nil {
		return "", err
	}
	sha, err := b.hash(p.target, name, info)
	r.hashed = b.hashed
	if err != nil {
		return "", err
	}
	if sha != row.Before.SHA256 {
		return "", ErrChanged
	}
	if err = boundDirectory(filepath.Dir(row.Target), p.target); err != nil {
		return "", err
	}
	if err = r.executor.fail("copy_before_ready"); err != nil {
		return "", err
	}
	if err = r.session.append(journalRecord{Kind: "copy_ready", At: time.Now().UTC().Format(time.RFC3339Nano), FileID: row.FileID, Claim: claim, Copy: &CopyStage{Path: copyTemporary(row, claim), Object: key, SHA256: sha}}); err != nil {
		return "", err
	}
	return key, nil
}
