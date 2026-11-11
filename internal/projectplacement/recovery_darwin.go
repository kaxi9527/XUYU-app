//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func (e *Executor) reviewRecovery(ctx context.Context, id, intentSHA string) (RecoveryPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	v, err := e.journal.Read(ctx, id)
	if err != nil {
		return RecoveryPreview{}, err
	}
	if v.IntentSHA != intentSHA {
		return RecoveryPreview{}, ErrConsent
	}
	p, err := e.inspectRecovery(ctx, v)
	if err != nil {
		return RecoveryPreview{}, err
	}
	after, err := e.journal.Read(ctx, id)
	if err != nil {
		return RecoveryPreview{}, err
	}
	if after.HeadSHA != v.HeadSHA {
		return RecoveryPreview{}, ErrChanged
	}
	return RecoveryPreview{snapshot: p, sha: digestJSON(p)}, nil
}

func (e *Executor) inspectRecovery(ctx context.Context, v JournalView) (RecoverySnapshot, error) {
	memory, err := e.recoveryAuthority(ctx, v)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	var item *ItemState
	for _, candidate := range v.Items {
		if candidate.Stage == "memory_pending" || candidate.Stage == "reconciliation_pending" {
			return RecoverySnapshot{}, ErrReconcile
		}
		if candidate.Stage == "claimed" || candidate.Stage == "uncertain" {
			if item != nil {
				return RecoverySnapshot{}, ErrJournal
			}
			owned := candidate
			item = &owned
		}
	}
	if item == nil {
		return RecoverySnapshot{}, ErrReconcile
	}
	row, ok := selectedRow(v, item.FileID)
	if !ok || protectedPath(row.Source) || protectedPath(row.Target) {
		return RecoverySnapshot{}, ErrScope
	}
	r := &executionRound{executor: e, session: &journalSession{view: v}, expected: map[string]string{}}
	for _, d := range v.Intent.Preview.Directories {
		r.expected[d.Path] = d.Object
	}
	for _, i := range v.Items {
		if i.Receipt != nil {
			for _, d := range i.Receipt.Directories {
				if old := r.expected[d.Path]; old != "" && old != d.Object {
					return RecoverySnapshot{}, ErrChanged
				}
				r.expected[d.Path] = d.Object
			}
		}
	}
	b, err := r.builder(ctx)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	b.limits.MaxBytes = 1 << 30
	p := RecoverySnapshot{OperationID: v.Intent.ID, IntentSHA: v.IntentSHA, JournalHead: v.HeadSHA, FileID: item.FileID, Claim: item.Claim, Stage: item.Stage, ClaimedAt: item.ClaimedAt, MemoryRevision: memory.State.Revision, MemoryHead: memory.HeadSHA}
	for _, f := range memory.State.Files {
		if f.ID == item.FileID {
			p.ExpectedFile = f
			break
		}
	}
	if p.ExpectedFile.ID == "" {
		return RecoverySnapshot{}, ErrChanged
	}
	p.Source, err = readRecoveryObject(b, row.Source, true)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	p.Target, err = readRecoveryObject(b, row.Target, true)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if row.Action == "copy" {
		if item.Copy != nil {
			p.CopyObject = item.Copy.Object
		}
		temporary, err := readRecoveryObject(b, copyTemporary(row, item.Claim), true)
		if err != nil {
			return RecoverySnapshot{}, err
		}
		p.Temporary = &temporary
	}
	// A full hash is a point-in-time fact. Check all three objects again after
	// the last hash so a concurrent write to an earlier one cannot go unnoticed.
	proofs := []RecoveryObject{p.Source, p.Target}
	if p.Temporary != nil {
		proofs = append(proofs, *p.Temporary)
	}
	for _, old := range proofs {
		current, err := readRecoveryObject(b, old.Path, false)
		if err != nil {
			return RecoverySnapshot{}, err
		}
		if old.Fact != nil && current.Fact != nil {
			current.Fact.SHA256 = old.Fact.SHA256
		}
		if !reflect.DeepEqual(old, current) {
			return RecoverySnapshot{}, ErrChanged
		}
	}
	// An absent file does not release its original ancestor bindings. guard's
	// allowMissing is only for directories absent from the original evidence;
	// a formerly present parent must still exist at the same authorized path.
	for path, old := range r.expected {
		if within(path, filepath.Dir(row.Source)) || within(path, filepath.Dir(row.Target)) {
			current, exists := b.directories[path]
			if !exists || current.Object != old {
				return RecoverySnapshot{}, ErrChanged
			}
		}
	}
	for path, d := range b.directories {
		if old := r.expected[path]; old != "" && old != d.Object {
			return RecoverySnapshot{}, ErrChanged
		}
		p.Directories = append(p.Directories, d)
	}
	if err = b.checkDirectories(); err != nil {
		return RecoverySnapshot{}, err
	}
	sort.Slice(p.Directories, func(i, j int) bool { return p.Directories[i].Path < p.Directories[j].Path })
	p.Outcome, p.Reason = recoveryOutcome(row, p.Source, p.Target, p.Temporary, p.CopyObject)
	after, err := e.recoveryAuthority(ctx, v)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if after.HeadSHA != memory.HeadSHA || after.State.Revision != memory.State.Revision {
		return RecoverySnapshot{}, ErrChanged
	}
	now := time.Now().UTC()
	p.ObservedAt = now.Format(time.RFC3339Nano)
	p.ExpiresAt = now.Add(10 * time.Minute).Format(time.RFC3339Nano)
	return p, nil
}

func readRecoveryObject(b *previewBuilder, path string, fullHash bool) (RecoveryObject, error) {
	out := RecoveryObject{Path: path}
	root, _, missing, check, err := b.guard(filepath.Dir(path), true)
	if err != nil {
		return out, err
	}
	if root != nil {
		defer root.Close()
	}
	if check.code != "" {
		return out, ErrScope
	}
	if missing {
		return out, nil
	}
	info, err := root.Lstat(filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		if err = destinationFree(root, filepath.Base(path)); err != nil {
			return out, err
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	key, _, single := object(info)
	if !info.Mode().IsRegular() || !single || projectmemory.ObjectIdentity(info) == "" || b.excludedObjects[key] || info.Size() > b.limits.MaxFileBytes {
		return out, ErrChanged
	}
	sha := ""
	if fullHash {
		sha, err = b.hash(root, filepath.Base(path), info)
		if err != nil {
			return out, err
		}
	}
	change := changeTime(info)
	out.Mode, out.ChangedAt = uint32(info.Mode()), fmt.Sprintf("%d:%d", change.Sec, change.Nsec)
	out.Fact = &projectmemory.Fact{Path: path, Kind: "file", Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano), ObjectKey: projectmemory.ObjectIdentity(info), SHA256: sha, HashState: "verified"}
	return out, nil
}

func (e *Executor) confirmRecovery(ctx context.Context, preview RecoveryPreview) (JournalView, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	p := preview.Snapshot()
	if preview.sha == "" || digestJSON(p) != preview.sha {
		return JournalView{}, ErrConsent
	}
	return e.journal.change(ctx, p.OperationID, func(s *journalSession) error {
		if s.view.IntentSHA != p.IntentSHA {
			return ErrConsent
		}
		index := itemIndex(s.view, p.FileID)
		if index < 0 {
			return ErrConsent
		}
		item := s.view.Items[index]
		if item.Recovery != nil {
			if item.Recovery.ReviewSHA != preview.sha {
				return ErrOperation
			}
			if item.Stage == "reconciliation_pending" {
				return e.saveRecoveryMemory(s, item)
			}
			if item.Stage == "reconciled" {
				return nil
			}
			return ErrReconcile
		}
		decision := RecoveryDecision{Review: p, ReviewSHA: preview.sha, ConfirmedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := validateRecovery(s.view, decision); err != nil {
			return err
		}
		if err := e.fail("recovery_before_confirm"); err != nil {
			return err
		}
		fresh, err := e.inspectRecovery(ctx, s.view)
		if err != nil {
			return err
		}
		// The reviewed observation time remains part of the user's confirmation;
		// a fresh pass must prove the same facts, identities and ledger boundary.
		fresh.ObservedAt, fresh.ExpiresAt = p.ObservedAt, p.ExpiresAt
		if !reflect.DeepEqual(fresh, p) {
			return ErrChanged
		}
		decision.ConfirmedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err = validateRecovery(s.view, decision); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		stopped, err := e.journal.RequestStop(ctx, p.OperationID, p.IntentSHA, time.Now())
		if err != nil {
			return err
		}
		s.view.StopRequested = stopped.StopRequested
		latest, err := e.recoveryAuthority(ctx, s.view)
		if err != nil {
			return err
		}
		if latest.HeadSHA != p.MemoryHead || latest.State.Revision != p.MemoryRevision {
			return ErrChanged
		}
		pending := item
		pending.Stage, pending.Recovery = "reconciliation_pending", &decision
		evidence, err := recoveryEvidence(s.view, pending)
		if err != nil {
			return err
		}
		if err = evidence.Validate(); err != nil {
			return err
		}
		if err = s.append(journalRecord{Kind: "recovery_confirmed", At: decision.ConfirmedAt, FileID: p.FileID, Claim: p.Claim, Recovery: &decision}); err != nil {
			return err
		}
		if err = e.fail("after_recovery_confirmed"); err != nil {
			return err
		}
		return e.saveRecoveryMemory(s, s.view.Items[index])
	})
}
