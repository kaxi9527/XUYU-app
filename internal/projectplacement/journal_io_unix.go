//go:build (darwin || linux) && cgo

package projectplacement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type journalSession struct {
	j    *Journal
	root *os.Root
	view JournalView
}

func syncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "\x00\r\n")
}

func privateInfo(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0o077 == 0
}

// Only one child name is used, always through the already opened parent. A
// successful return includes a persistence barrier even when recovering an
// existing directory from a previous failed Mkdir/parent-Sync operation.
func journalChild(parent *os.Root, name string, create, private bool) (*os.Root, error) {
	if !safeName(name) {
		return nil, ErrScope
	}
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || private && !privateInfo(info) {
		return nil, ErrJournal
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, e1 := r.Stat(".")
	current, e2 := parent.Lstat(name)
	if e1 != nil || e2 != nil || !current.IsDir() || !os.SameFile(info, after) || !os.SameFile(info, current) {
		r.Close()
		return nil, ErrChanged
	}
	if create {
		if err = syncDirectory(parent); err != nil {
			r.Close()
			return nil, err
		}
	}
	return r, nil
}

func flockContext(ctx context.Context, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (j *Journal) controlLock(ctx context.Context) (*os.Root, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	parent, err := openDirectory(filepath.Dir(j.control))
	if err != nil {
		return nil, nil, err
	}
	root, err := journalChild(parent, filepath.Base(j.control), true, false)
	parent.Close()
	if err != nil {
		return nil, nil, err
	}
	file, err := root.OpenFile("整理执行.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	fail := func(err error) (*os.Root, func(), error) { file.Close(); root.Close(); return nil, nil, err }
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	_, _, single := object(info)
	if !info.Mode().IsRegular() || !single || !privateInfo(info) {
		return fail(ErrJournal)
	}
	if err = flockContext(ctx, file); err != nil {
		return fail(err)
	}
	unlock := func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close(); root.Close() }
	current, err := root.Lstat("整理执行.lock")
	if err != nil || !sameFile(info, current) || !current.Mode().IsRegular() || !privateInfo(current) {
		unlock()
		return nil, nil, ErrChanged
	}
	if err = file.Sync(); err == nil {
		err = syncDirectory(root)
	}
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return root, unlock, nil
}

func (j *Journal) operationRoot(control *os.Root, id string, create bool) (*os.Root, error) {
	if !safeID(id) {
		return nil, ErrScope
	}
	base, err := journalChild(control, "项目整理", create, true)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	project, err := journalChild(base, j.project.ID, create, true)
	if err != nil {
		return nil, err
	}
	defer project.Close()
	return journalChild(project, id, create, true)
}

func readJournalFile(ctx context.Context, root *os.Root, name string, max int64) ([]byte, error) {
	if !safeName(name) {
		return nil, ErrJournal
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	_, _, single := object(before)
	if !before.Mode().IsRegular() || !single || !privateInfo(before) || before.Size() <= 0 || before.Size() > max {
		return nil, ErrJournal
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	after, e1 := f.Stat()
	current, e2 := root.Lstat(name)
	if e1 != nil || e2 != nil || !sameFile(before, after) || !sameFile(before, current) || int64(len(data)) != before.Size() {
		return nil, ErrJournal
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func (j *Journal) publish(ctx context.Context, root *os.Root, name string, data []byte) error {
	if !safeName(name) || len(data) == 0 || len(data) > maxIntentBytes {
		return ErrJournal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	nonce, err := randomID()
	if err != nil {
		return err
	}
	temporary := ".draft-" + nonce
	if err = j.fail(name + ":before_create"); err != nil {
		return err
	}
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	// Unpublished drafts are never interpreted as events or automatically
	// removed on recovery. Only exclusive publication makes a record visible.
	if _, err = f.Write(data); err == nil {
		err = j.fail(name + ":before_file_sync")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = j.fail(name + ":before_publish"); err != nil {
		return err
	}
	if err = renameAtExclusive(root, temporary, root, name); err != nil {
		return err
	}
	if err = j.fail(name + ":after_publish"); err != nil {
		return err
	}
	if err = j.fail(name + ":before_directory_sync"); err != nil {
		return err
	}
	if err = syncDirectory(root); err != nil {
		return err
	}
	return j.fail(name + ":after_directory_sync")
}

func recordFile(revision uint64) string { return fmt.Sprintf("event-%08d.json", revision) }

// A separate, write-ahead commitment detects a missing acknowledged tail.
// Neither an orphan commitment nor an uncommitted event is replay authority.
// Simultaneous rollback of all local control files needs an external trust
// anchor and is not something this local journal can prove or repair.
type journalCommitment struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operationID"`
	IntentSHA   string `json:"intentSHA"`
	Revision    uint64 `json:"revision"`
	PreviousSHA string `json:"previousSHA,omitempty"`
	RecordSHA   string `json:"recordSHA"`
}

func commitmentFile(revision uint64) string { return fmt.Sprintf("commit-%08d.json", revision) }

func commitmentFor(id string, record journalRecord) journalCommitment {
	return journalCommitment{Schema: 1, OperationID: id, IntentSHA: record.IntentSHA, Revision: record.Revision, PreviousSHA: record.PreviousSHA, RecordSHA: digestJSON(record)}
}

func (j *Journal) readRoot(ctx context.Context, root *os.Root, id string) (JournalView, error) {
	data, err := readJournalFile(ctx, root, "intent.json", maxIntentBytes)
	if errors.Is(err, os.ErrNotExist) {
		if err := noCommittedIntent(root); err != nil {
			return JournalView{}, err
		}
		return JournalView{}, ErrNotFound
	}
	if err != nil {
		return JournalView{}, err
	}
	var intent Intent
	if decodeCanonical(data, &intent) != nil || intent.validate() != nil || intent.ID != id || intent.Preview.Authority.Project != j.project {
		return JournalView{}, ErrJournal
	}
	view := initialView(intent)
	f, err := root.Open(".")
	if err != nil {
		return JournalView{}, err
	}
	const entryLimit = maxJournalRecords*2 + 259 // intent, stop pair and at most 256 drafts
	names, err := f.Readdirnames(entryLimit + 1)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return JournalView{}, err
	}
	if len(names) > entryLimit {
		return JournalView{}, ErrJournal
	}
	records, commitments, drafts := []string{}, []string{}, 0
	for _, name := range names {
		switch {
		case name == "intent.json", name == "stop.json", name == "stop-commit.json":
		case strings.HasPrefix(name, ".draft-") && safeID(strings.TrimPrefix(name, ".draft-")):
			drafts++
		case strings.HasPrefix(name, "event-"):
			records = append(records, name)
		case strings.HasPrefix(name, "commit-"):
			commitments = append(commitments, name)
		default:
			return JournalView{}, ErrJournal
		}
	}
	if len(records) > maxJournalRecords || len(commitments) > maxJournalRecords || drafts > 256 {
		return JournalView{}, ErrJournal
	}
	if len(records) != len(commitments) {
		// This can be an interrupted publication or a lost committed record.
		// Preserve both cases; never silently accept a shorter history prefix.
		return JournalView{}, ErrReconcile
	}
	sort.Strings(records)
	sort.Strings(commitments)
	for index, name := range records {
		if name != recordFile(uint64(index+1)) || commitments[index] != commitmentFile(uint64(index+1)) {
			return JournalView{}, ErrJournal
		}
		data, err := readJournalFile(ctx, root, name, maxJournalBytes)
		if err != nil {
			return JournalView{}, err
		}
		var record journalRecord
		if decodeCanonical(data, &record) != nil {
			return JournalView{}, ErrJournal
		}
		commitData, err := readJournalFile(ctx, root, commitments[index], 1024)
		if err != nil {
			return JournalView{}, err
		}
		var commitment journalCommitment
		if decodeCanonical(commitData, &commitment) != nil || commitment != commitmentFor(id, record) {
			return JournalView{}, ErrJournal
		}
		if err = j.hydrateUndo(ctx, &view, record); err != nil {
			return JournalView{}, err
		}
		view, err = applyJournal(view, record)
		if err != nil {
			return JournalView{}, err
		}
	}
	view.StopRequested, err = readStop(ctx, root, view)
	if err != nil {
		return JournalView{}, err
	}
	for _, item := range view.Items {
		if item.Recovery != nil && !view.StopRequested {
			return JournalView{}, ErrReconcile
		}
	}
	if len(view.UndoGroups) > 0 && !view.StopRequested && view.Status != "completed" && view.Status != "stopped" {
		return JournalView{}, ErrReconcile
	}
	for i, g := range view.UndoGroups {
		stopped, stopErr := j.readUndoStop(ctx, view, g)
		if stopErr != nil {
			return JournalView{}, stopErr
		}
		view.UndoGroups[i].StopRequested = stopped
	}
	return view, nil
}

func noCommittedIntent(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	names, readErr := f.Readdirnames(257)
	f.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || len(names) > 256 {
		return ErrJournal
	}
	for _, name := range names {
		if !strings.HasPrefix(name, ".draft-") || !safeID(strings.TrimPrefix(name, ".draft-")) {
			return ErrJournal
		}
	}
	return nil
}

func (j *Journal) Read(ctx context.Context, id string) (JournalView, error) {
	if !safeID(id) {
		return JournalView{}, ErrScope
	}
	control, err := openDirectory(j.control)
	if errors.Is(err, os.ErrNotExist) {
		return JournalView{}, ErrNotFound
	}
	if err != nil {
		return JournalView{}, err
	}
	defer control.Close()
	root, err := j.operationRoot(control, id, false)
	if errors.Is(err, os.ErrNotExist) {
		return JournalView{}, ErrNotFound
	}
	if err != nil {
		return JournalView{}, err
	}
	defer root.Close()
	return j.readRoot(ctx, root, id)
}

func (j *Journal) prepare(ctx context.Context, intent Intent) (JournalView, error) {
	control, unlock, err := j.controlLock(ctx)
	if err != nil {
		return JournalView{}, err
	}
	defer unlock()
	root, err := j.operationRoot(control, intent.ID, true)
	if err != nil {
		return JournalView{}, err
	}
	defer root.Close()
	old, err := j.readRoot(ctx, root, intent.ID)
	if err == nil {
		if old.Intent.RequestSHA != intent.RequestSHA {
			return JournalView{}, ErrOperation
		}
		if err = syncDirectory(root); err != nil {
			return JournalView{}, err
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return JournalView{}, err
	}
	// An absent intent in a directory containing committed records is damage,
	// not an unused operation ID. Never reconstruct authority over those records.
	if err = noCommittedIntent(root); err != nil {
		return JournalView{}, err
	}
	data, err := json.Marshal(intent)
	if err != nil || len(data) > maxIntentBytes {
		return JournalView{}, ErrJournal
	}
	if err = j.publish(ctx, root, "intent.json", data); err != nil {
		return JournalView{}, err
	}
	return j.readRoot(ctx, root, intent.ID)
}

func (j *Journal) change(ctx context.Context, id string, change func(*journalSession) error) (JournalView, error) {
	control, unlock, err := j.controlLock(ctx)
	if err != nil {
		return JournalView{}, err
	}
	defer unlock()
	root, err := j.operationRoot(control, id, false)
	if errors.Is(err, os.ErrNotExist) {
		return JournalView{}, ErrNotFound
	}
	if err != nil {
		return JournalView{}, err
	}
	defer root.Close()
	view, err := j.readRoot(ctx, root, id)
	if err != nil {
		return JournalView{}, err
	}
	// Repair the durability barrier of a fully published, previously unacked
	// record before any subsequent state transition may rely on it.
	if err = syncDirectory(root); err != nil {
		return JournalView{}, err
	}
	session := &journalSession{j: j, root: root, view: view}
	if err = change(session); err != nil {
		return session.view, err
	}
	return session.view, nil
}

func (s *journalSession) append(record journalRecord) error {
	if s.view.Revision >= maxJournalRecords {
		return ErrJournal
	}
	record.Schema, record.Revision, record.PreviousSHA, record.IntentSHA = 1, s.view.Revision+1, s.view.HeadSHA, s.view.IntentSHA
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxJournalBytes {
		return ErrJournal
	}
	var owned journalRecord
	if decodeCanonical(data, &owned) != nil {
		return ErrJournal
	}
	if err = s.j.hydrateUndo(context.Background(), &s.view, owned); err != nil {
		return err
	}
	next, err := applyJournal(s.view, owned)
	if err != nil {
		return err
	}
	if err = s.checkBinding(); err != nil {
		return err
	}
	// The runner's context is checked before each transition. A started
	// publication finishes its barriers instead of returning a half receipt.
	commitData, err := json.Marshal(commitmentFor(s.view.Intent.ID, owned))
	if err != nil {
		return err
	}
	if err = s.j.publish(context.Background(), s.root, commitmentFile(record.Revision), commitData); err != nil {
		return err
	}
	if err = s.j.publish(context.Background(), s.root, recordFile(record.Revision), data); err != nil {
		return err
	}
	s.view = next
	return s.checkBinding()
}

func (s *journalSession) checkBinding() error {
	current, err := openDirectory(filepath.Join(s.j.directory(), s.view.Intent.ID))
	if err != nil {
		return ErrChanged
	}
	defer current.Close()
	a, e1 := current.Stat(".")
	b, e2 := s.root.Stat(".")
	if e1 != nil || e2 != nil || !os.SameFile(a, b) {
		return ErrChanged
	}
	return nil
}

func (s *journalSession) checkStop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stopped, err := readStop(ctx, s.root, s.view)
	if err != nil {
		return err
	}
	if !stopped {
		return nil
	}
	s.view.StopRequested = true
	return ErrStopped
}

func (s *journalSession) claim(ctx context.Context, id string, now time.Time) (string, error) {
	if err := s.checkStop(ctx); err != nil {
		return "", err
	}
	if unsettled(s.view) {
		return "", ErrReconcile
	}
	if s.view.Status == "prepared" {
		return "", ErrConsent
	}
	if s.view.Status == "stopped" || s.view.Status == "completed" {
		return "", ErrStopped
	}
	claim, err := randomID()
	if err != nil {
		return "", err
	}
	if err = s.append(journalRecord{Kind: "claimed", FileID: id, Claim: claim, At: now.UTC().Format(time.RFC3339Nano)}); err != nil {
		return "", err
	}
	return claim, nil
}

func (s *journalSession) physical(id, claim string, receipt PhysicalReceipt, now time.Time) error {
	index := itemIndex(s.view, id)
	if index >= 0 && s.view.Items[index].Claim == claim && s.view.Items[index].Receipt != nil {
		if digestJSON(s.view.Items[index].Receipt) == digestJSON(receipt) {
			return nil
		}
		return ErrOperation
	}
	return s.append(journalRecord{Kind: "physical", FileID: id, Claim: claim, Receipt: &receipt, At: now.UTC().Format(time.RFC3339Nano)})
}

func (s *journalSession) memory(id, claim string, receipt MemoryReceipt, now time.Time) error {
	index := itemIndex(s.view, id)
	if index >= 0 && s.view.Items[index].Claim == claim && s.view.Items[index].Memory != nil {
		if *s.view.Items[index].Memory == receipt {
			return nil
		}
		return ErrOperation
	}
	return s.append(journalRecord{Kind: "memory_saved", FileID: id, Claim: claim, Memory: &receipt, At: now.UTC().Format(time.RFC3339Nano)})
}

type stopRequest struct {
	ID        string `json:"id"`
	IntentSHA string `json:"intentSHA"`
	At        string `json:"at"`
}

type stopCommitment struct {
	Schema     int    `json:"schema"`
	ID         string `json:"id"`
	IntentSHA  string `json:"intentSHA"`
	RequestSHA string `json:"requestSHA"`
}

func readStop(ctx context.Context, root *os.Root, view JournalView) (bool, error) {
	data, requestErr := readJournalFile(ctx, root, "stop.json", 1024)
	commitData, commitErr := readJournalFile(ctx, root, "stop-commit.json", 1024)
	requestMissing, commitMissing := errors.Is(requestErr, os.ErrNotExist), errors.Is(commitErr, os.ErrNotExist)
	if requestErr != nil && !requestMissing {
		return false, requestErr
	}
	if commitErr != nil && !commitMissing {
		return false, commitErr
	}
	if requestMissing && commitMissing {
		return false, nil
	}
	if requestMissing || commitMissing {
		return false, ErrReconcile
	}
	var request stopRequest
	var commitment stopCommitment
	if decodeCanonical(data, &request) != nil || request.ID != view.Intent.ID || request.IntentSHA != view.IntentSHA || !validTime(request.At) || decodeCanonical(commitData, &commitment) != nil || commitment != (stopCommitment{Schema: 1, ID: request.ID, IntentSHA: request.IntentSHA, RequestSHA: digestJSON(request)}) {
		return false, ErrJournal
	}
	return true, nil
}

// RequestStop has a separate immutable marker so another process can request
// stopping while the executor holds the shared movement lock. It never undoes
// an in-flight item or authorizes another claim.
func (j *Journal) RequestStop(ctx context.Context, id, intentSHA string, now time.Time) (JournalView, error) {
	view, err := j.Read(ctx, id)
	if err != nil {
		return JournalView{}, err
	}
	if view.IntentSHA != intentSHA {
		return JournalView{}, ErrConsent
	}
	root, err := openDirectory(filepath.Join(j.directory(), id))
	if err != nil {
		return JournalView{}, err
	}
	defer root.Close()
	view, err = j.readRoot(ctx, root, id)
	if err != nil {
		return JournalView{}, err
	}
	if view.IntentSHA != intentSHA {
		return JournalView{}, ErrConsent
	}
	session := &journalSession{j: j, root: root, view: view}
	if err = session.checkBinding(); err != nil {
		return JournalView{}, err
	}
	if !view.StopRequested && view.Status != "completed" && view.Status != "stopped" {
		request := stopRequest{ID: id, IntentSHA: intentSHA, At: now.UTC().Format(time.RFC3339Nano)}
		data, _ := json.Marshal(request)
		commitData, _ := json.Marshal(stopCommitment{Schema: 1, ID: id, IntentSHA: intentSHA, RequestSHA: digestJSON(request)})
		err = j.publish(ctx, root, "stop-commit.json", commitData)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return JournalView{}, err
		}
		// Only the caller that durably published this exact commitment may
		// publish its request. A concurrent loser reads the original pair; it
		// cannot replace its timestamp or reconstruct an orphan commitment.
		if err == nil {
			if err = j.publish(ctx, root, "stop.json", data); err != nil && !errors.Is(err, os.ErrExist) {
				return JournalView{}, err
			}
		}
	}
	// Visible stop/terminal records can follow a failed directory Sync. Every
	// acknowledgment, including an idempotent retry, must restore this barrier.
	if err = j.fail("stop-ack:before_directory_sync"); err != nil {
		return JournalView{}, err
	}
	if err = syncDirectory(root); err != nil {
		return JournalView{}, err
	}
	view, err = j.readRoot(ctx, root, id)
	if err != nil {
		return JournalView{}, err
	}
	if err = session.checkBinding(); err != nil {
		return JournalView{}, err
	}
	return view, nil
}
