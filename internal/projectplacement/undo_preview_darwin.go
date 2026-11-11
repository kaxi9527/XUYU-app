//go:build darwin && cgo

package projectplacement

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func undoExpectedDirectories(v JournalView) (map[string]string, error) {
	out := map[string]string{}
	add := func(dirs []Directory) error {
		for _, d := range dirs {
			if old := out[d.Path]; old != "" && old != d.Object {
				return ErrChanged
			}
			out[d.Path] = d.Object
		}
		return nil
	}
	if err := add(v.Intent.Preview.Directories); err != nil {
		return nil, err
	}
	for _, item := range v.Items {
		if item.Receipt != nil {
			if err := add(item.Receipt.Directories); err != nil {
				return nil, err
			}
		}
		if item.Recovery != nil {
			if err := add(item.Recovery.Review.Directories); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func undoMemoryReference(s projectmemory.State, o projectmemory.UndoReference) bool {
	n := 0
	if o.Kind == "placement" {
		for _, p := range s.Placements {
			if p.OperationID == o.OperationID && p.Claim == o.Claim && p.EventID == o.EventID && p.PhysicalSHA == o.ProofSHA && p.Outcome == "moved" && p.SourceFileID == o.FileID && p.TargetFileID == o.FileID && p.From == o.From.Path && p.To == o.To.Path {
				n++
			}
		}
	}
	if o.Kind == "reconciliation" {
		for _, p := range s.Reconciliations {
			if p.OperationID == o.OperationID && p.Claim == o.Claim && p.EventID == o.EventID && p.ReviewSHA == o.ProofSHA && p.Outcome == "at_target" && p.SourceFileID == o.FileID && p.TargetFileID == o.FileID && p.From == o.From.Path && p.To == o.To.Path {
				n++
			}
		}
	}
	for _, u := range s.Undos {
		if u.OriginalOperation == o.OperationID && u.OriginalClaim == o.Claim {
			return false
		}
	}
	return n == 1
}

func (e *Executor) reviewUndo(ctx context.Context, id, intentSHA string) (UndoPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	v, err := e.journal.Read(ctx, id)
	if err != nil {
		return UndoPreview{}, err
	}
	if v.IntentSHA != intentSHA {
		return UndoPreview{}, ErrConsent
	}
	p, err := e.inspectUndo(ctx, v)
	if err != nil {
		return UndoPreview{}, err
	}
	after, err := e.journal.Read(ctx, id)
	if err != nil {
		return UndoPreview{}, err
	}
	if after.HeadSHA != v.HeadSHA {
		return UndoPreview{}, ErrChanged
	}
	return UndoPreview{snapshot: p, sha: digestJSON(p)}, nil
}

func (e *Executor) inspectUndo(ctx context.Context, v JournalView) (UndoSnapshot, error) {
	memory, err := e.recoveryAuthority(ctx, v)
	if err != nil {
		return UndoSnapshot{}, err
	}
	expected, err := undoExpectedDirectories(v)
	if err != nil {
		return UndoSnapshot{}, err
	}
	round := &executionRound{executor: e, session: &journalSession{view: v}, expected: expected}
	p := UndoSnapshot{OperationID: v.Intent.ID, IntentSHA: v.IntentSHA, JournalHead: v.HeadSHA, MemoryRevision: memory.State.Revision, MemoryHead: memory.HeadSHA, Rows: []UndoRow{}, Directories: []Directory{}}
	rowDirs := map[string][]Directory{}
	allDirs := map[string]Directory{}
	for _, item := range v.Items {
		if err = ctx.Err(); err != nil {
			return UndoSnapshot{}, err
		}
		row := UndoRow{FileID: item.FileID, Action: "keep", Reason: "原文件没有移动，无需恢复", DirectoryIDs: []int{}}
		original, proofErr := undoOriginal(v, item)
		if proofErr != nil {
			forward, _ := selectedRow(v, item.FileID)
			if forward.Action == "copy" {
				row.Reason = "复制保留了原件；副本也会保留"
			} else if item.Stage == "claimed" || item.Stage == "uncertain" || item.Stage == "memory_pending" || item.Stage == "reconciliation_pending" {
				row.Action, row.Reason = "conflict", "原整理尚需核对或补记，先保留现场"
			}
			p.Rows = append(p.Rows, row)
			continue
		}
		row.Original = original
		if undoOwned(v, item.FileID) {
			row.Action, row.Reason = "conflict", "这份文件已被撤销组占用或恢复，不会再次执行"
			p.Rows = append(p.Rows, row)
			continue
		}
		for _, f := range memory.State.Files {
			if f.ID == item.FileID {
				row.ExpectedFile = f
				break
			}
		}
		if !undoMemoryReference(memory.State, original) {
			row.Action, row.Reason = "conflict", "原事件或撤销记录需要核对"
			p.Rows = append(p.Rows, row)
			continue
		}
		dirs, readErr := inspectUndoRow(ctx, round, &row, memory.State.Revision)
		if readErr != nil {
			if ctx.Err() != nil {
				return UndoSnapshot{}, ctx.Err()
			}
			row.Action, row.Reason = "conflict", executionReason("当前位置无法安全恢复，文件保留：", readErr)
		} else {
			row.Action, row.Reason = "restore", "恢复到原登记位置，沿用原文件身份"
			rowDirs[row.FileID] = dirs
			for _, d := range dirs {
				if old, exists := allDirs[d.Path]; exists && old.Object != d.Object {
					return UndoSnapshot{}, ErrChanged
				}
				allDirs[d.Path] = d
			}
		}
		p.Rows = append(p.Rows, row)
	}
	for _, d := range allDirs {
		p.Directories = append(p.Directories, d)
	}
	sort.Slice(p.Directories, func(i, j int) bool { return p.Directories[i].Path < p.Directories[j].Path })
	indices := map[string]int{}
	for i, d := range p.Directories {
		indices[d.Path] = i
	}
	for i := range p.Rows {
		for _, d := range rowDirs[p.Rows[i].FileID] {
			p.Rows[i].DirectoryIDs = append(p.Rows[i].DirectoryIDs, indices[d.Path])
		}
		sort.Ints(p.Rows[i].DirectoryIDs)
	}
	sort.Slice(p.Rows, func(i, j int) bool { return p.Rows[i].FileID < p.Rows[j].FileID })
	after, err := e.recoveryAuthority(ctx, v)
	if err != nil {
		return UndoSnapshot{}, err
	}
	if after.HeadSHA != memory.HeadSHA || after.State.Revision != memory.State.Revision {
		return UndoSnapshot{}, ErrChanged
	}
	now := time.Now().UTC()
	p.CreatedAt, p.ExpiresAt = now.Format(time.RFC3339Nano), now.Add(10*time.Minute).Format(time.RFC3339Nano)
	return p, nil
}

func inspectUndoRow(ctx context.Context, round *executionRound, row *UndoRow, revision uint64) ([]Directory, error) {
	from, to := row.Original.To.Path, row.Original.From.Path
	if protectedPath(from) || protectedPath(to) {
		return nil, ErrScope
	}
	b, err := round.builder(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { round.hashed = b.hashed }()
	for _, path := range []string{filepath.Dir(from), filepath.Dir(to)} {
		root, _, missing, check, err := b.guard(path, false)
		if root != nil {
			root.Close()
		}
		if err != nil {
			return nil, err
		}
		if missing || check.code != "" {
			return nil, ErrChanged
		}
	}
	row.Source, err = readRecoveryObject(b, from, true)
	if err != nil {
		return nil, err
	}
	row.Destination, err = readRecoveryObject(b, to, true)
	if err != nil {
		return nil, err
	}
	if row.Source.Fact == nil || *row.Source.Fact != row.Original.To || row.Destination.Fact != nil {
		return nil, ErrChanged
	}
	for _, old := range []RecoveryObject{row.Source, row.Destination} {
		current, err := readRecoveryObject(b, old.Path, false)
		if err != nil {
			return nil, err
		}
		if old.Fact != nil && current.Fact != nil {
			current.Fact.SHA256 = old.Fact.SHA256
		}
		if !reflect.DeepEqual(old, current) {
			return nil, ErrChanged
		}
	}
	for path, object := range round.expected {
		if within(path, filepath.Dir(from)) || within(path, filepath.Dir(to)) {
			d, exists := b.directories[path]
			if !exists || d.Object != object {
				return nil, ErrChanged
			}
		}
	}
	if err = round.checkDirectoryObjects(b); err != nil {
		return nil, err
	}
	proof := projectmemory.UndoEvidence{Project: round.session.view.Intent.Preview.Authority.Project, UndoID: "preview", Claim: "preview", PlanSHA: round.session.view.IntentSHA, PhysicalSHA: round.session.view.IntentSHA, Original: row.Original, Scope: round.session.view.Intent.Preview.Authority.Scope, ExpectedFile: row.ExpectedFile, Before: row.Original.To, After: row.Original.From, MemoryRevision: revision, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if proof.Validate() != nil {
		return nil, ErrChanged
	}
	dirs := []Directory{}
	for _, d := range b.directories {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path < dirs[j].Path })
	return dirs, nil
}

func sameUndoProof(a, b undoPlan) bool {
	if !reflect.DeepEqual(a.Rows, b.Rows) || len(a.Directories) != len(b.Directories) {
		return false
	}
	for i, d := range a.Directories {
		if d.Path != b.Directories[i].Path || d.Object != b.Directories[i].Object {
			return false
		}
	}
	return true
}

func (e *Executor) confirmUndo(ctx context.Context, groupID string, preview UndoPreview, selected []string) (JournalView, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	p := preview.Snapshot()
	return e.journal.change(ctx, p.OperationID, func(s *journalSession) error {
		plan, root, chunks, err := makeUndoPlan(s.view, groupID, preview, selected)
		if err != nil {
			return err
		}
		manifestSHA := sumBytes(root)
		if index := undoGroupIndex(s.view, groupID); index >= 0 {
			if s.view.UndoGroups[index].ManifestSHA != manifestSHA {
				return ErrOperation
			}
			return nil
		}
		if s.view.HeadSHA != p.JournalHead {
			return ErrChanged
		}
		expires, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
		if err != nil || !time.Now().Before(expires) {
			return ErrConsent
		}
		if err = e.fail("undo_before_confirm"); err != nil {
			return err
		}
		fresh, err := e.inspectUndo(ctx, s.view)
		if err != nil {
			return err
		}
		if fresh.MemoryRevision != p.MemoryRevision || fresh.MemoryHead != p.MemoryHead {
			return ErrChanged
		}
		freshPlan, _, _, err := makeUndoPlan(s.view, groupID, UndoPreview{snapshot: fresh, sha: digestJSON(fresh)}, selected)
		if err != nil || !sameUndoProof(plan, freshPlan) {
			return ErrChanged
		}
		// Validate the small transition and worst-case journal reservation before
		// writing a plan or granting any native action.
		transition := journalRecord{Kind: "undo_confirmed", At: time.Now().UTC().Format(time.RFC3339Nano), Undo: &undoTransition{GroupID: groupID, ManifestSHA: manifestSHA, Count: plan.Manifest.Count, SelectionSHA: plan.Manifest.SelectionSHA}}
		check := s.view
		check.undoPlans = map[string]undoPlan{groupID: plan}
		if _, err = applyUndoRecord(check, transition); err != nil {
			return err
		}
		stopped, err := e.journal.RequestStop(ctx, p.OperationID, p.IntentSHA, time.Now())
		if err != nil {
			return err
		}
		s.view.StopRequested = stopped.StopRequested
		if err = e.journal.writeUndoPlan(ctx, plan, root, chunks); err != nil {
			return err
		}
		if err = e.fail("undo_after_plan"); err != nil {
			return err
		}
		latest, err := e.recoveryAuthority(ctx, s.view)
		if err != nil {
			return err
		}
		if latest.HeadSHA != p.MemoryHead || latest.State.Revision != p.MemoryRevision {
			return ErrChanged
		}
		transition.At = time.Now().UTC().Format(time.RFC3339Nano)
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = s.append(transition); err != nil {
			return err
		}
		return e.fail("undo_after_confirmed")
	})
}
