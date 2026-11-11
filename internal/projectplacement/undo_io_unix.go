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
	"strings"
	"time"
)

func undoPartName(index int) string { return fmt.Sprintf("part-%05d.json", index) }
func (j *Journal) undoPath(id, group string) string {
	return filepath.Join(j.control, "项目撤销计划", j.project.ID, id, group)
}

func (j *Journal) undoRoot(control *os.Root, id, group string, create bool) (*os.Root, error) {
	if !safeID(id) || !safeID(group) {
		return nil, ErrScope
	}
	parent := control
	owned := []*os.Root{}
	defer func() {
		for _, r := range owned {
			r.Close()
		}
	}()
	names := []string{"项目撤销计划", j.project.ID, id, group}
	for index, name := range names {
		next, err := journalChild(parent, name, create, true)
		if err != nil {
			return nil, err
		}
		if index == len(names)-1 {
			return next, nil
		}
		owned = append(owned, next)
		parent = next
	}
	return nil, ErrJournal
}

func sameUndoRoot(path string, root *os.Root) error {
	current, err := openDirectory(path)
	if err != nil {
		return ErrChanged
	}
	defer current.Close()
	a, e1 := current.Stat(".")
	b, e2 := root.Stat(".")
	if e1 != nil || e2 != nil || !os.SameFile(a, b) {
		return ErrChanged
	}
	return nil
}

func (j *Journal) writeUndoPlan(ctx context.Context, plan undoPlan, rootBytes []byte, chunks [][]byte) error {
	control, err := openDirectory(j.control)
	if err != nil {
		return err
	}
	defer control.Close()
	root, err := j.undoRoot(control, plan.Manifest.OperationID, plan.Manifest.GroupID, true)
	if err != nil {
		return err
	}
	defer root.Close()
	write := func(name string, data []byte) error {
		old, readErr := readJournalFile(ctx, root, name, maxIntentBytes)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if errors.Is(readErr, os.ErrNotExist) {
			if err := j.publish(ctx, root, name, data); err != nil {
				return err
			}
			old, readErr = readJournalFile(ctx, root, name, maxIntentBytes)
		}
		if readErr != nil {
			return readErr
		}
		if string(old) != string(data) {
			return ErrOperation
		}
		return syncDirectory(root)
	}
	for i, data := range chunks {
		if err = write(undoPartName(i), data); err != nil {
			return err
		}
	}
	if err = write("manifest.json", rootBytes); err != nil {
		return err
	}
	return sameUndoRoot(j.undoPath(plan.Manifest.OperationID, plan.Manifest.GroupID), root)
}

func (j *Journal) readUndoPlan(ctx context.Context, v JournalView, transition undoTransition) (undoPlan, error) {
	if !safeID(transition.GroupID) || !goodSHA(transition.ManifestSHA) {
		return undoPlan{}, ErrJournal
	}
	control, err := openDirectory(j.control)
	if err != nil {
		return undoPlan{}, err
	}
	defer control.Close()
	root, err := j.undoRoot(control, v.Intent.ID, transition.GroupID, false)
	if err != nil {
		return undoPlan{}, ErrReconcile
	}
	defer root.Close()
	data, err := readJournalFile(ctx, root, "manifest.json", maxJournalBytes)
	if err != nil {
		return undoPlan{}, ErrReconcile
	}
	var m UndoManifest
	if sumBytes(data) != transition.ManifestSHA || decodeCanonical(data, &m) != nil || m.GroupID != transition.GroupID || m.OperationID != v.Intent.ID || m.IntentSHA != v.IntentSHA || len(m.Chunks) == 0 || len(m.Chunks) > 4096 || m.Bytes <= 0 || m.Bytes+len(data) > maxUndoPlanBytes {
		return undoPlan{}, ErrJournal
	}
	bytes := m.Bytes + len(data)
	for _, g := range v.UndoGroups {
		if g.ID != m.GroupID {
			bytes += g.PlanBytes
		}
	}
	if bytes > maxUndoTotalBytes {
		return undoPlan{}, ErrUndoBudget
	}
	file, err := root.Open(".")
	if err != nil {
		return undoPlan{}, err
	}
	names, listErr := file.Readdirnames(len(m.Chunks) + 260)
	file.Close()
	if listErr != nil && !errors.Is(listErr, io.EOF) || len(names) > len(m.Chunks)+259 {
		return undoPlan{}, ErrJournal
	}
	allowed := map[string]bool{"manifest.json": true, "stop.json": true, "stop-commit.json": true}
	for i := range m.Chunks {
		allowed[undoPartName(i)] = true
	}
	drafts := 0
	for _, name := range names {
		if allowed[name] {
			continue
		}
		if strings.HasPrefix(name, ".draft-") && safeID(strings.TrimPrefix(name, ".draft-")) {
			drafts++
			continue
		}
		return undoPlan{}, ErrJournal
	}
	if drafts > 256 {
		return undoPlan{}, ErrJournal
	}
	p := undoPlan{Manifest: m, Rows: []UndoRow{}, Directories: []Directory{}}
	total := 0
	rowsStarted := false
	for i, reference := range m.Chunks {
		if reference.Bytes <= 0 || reference.Bytes > maxJournalBytes || reference.Count <= 0 || !goodSHA(reference.SHA) {
			return undoPlan{}, ErrJournal
		}
		data, err = readJournalFile(ctx, root, undoPartName(i), maxJournalBytes)
		if err != nil {
			return undoPlan{}, ErrReconcile
		}
		var chunk undoChunk
		if len(data) != reference.Bytes || sumBytes(data) != reference.SHA || decodeCanonical(data, &chunk) != nil || chunk.Schema != 1 || chunk.Index != i || chunk.Kind != reference.Kind {
			return undoPlan{}, ErrJournal
		}
		switch chunk.Kind {
		case "directories":
			if rowsStarted || len(chunk.Rows) != 0 || len(chunk.Directories) != reference.Count {
				return undoPlan{}, ErrJournal
			}
			p.Directories = append(p.Directories, chunk.Directories...)
		case "rows":
			rowsStarted = true
			if len(chunk.Directories) != 0 || len(chunk.Rows) != reference.Count {
				return undoPlan{}, ErrJournal
			}
			p.Rows = append(p.Rows, chunk.Rows...)
		default:
			return undoPlan{}, ErrJournal
		}
		total += len(data)
	}
	if total != m.Bytes || validateUndoPlan(v, p) != nil {
		return undoPlan{}, ErrJournal
	}
	if err = sameUndoRoot(j.undoPath(v.Intent.ID, m.GroupID), root); err != nil {
		return undoPlan{}, err
	}
	return p, nil
}

func (j *Journal) hydrateUndo(ctx context.Context, v *JournalView, r journalRecord) error {
	if r.Kind != "undo_confirmed" {
		return nil
	}
	if r.Undo == nil {
		return ErrJournal
	}
	p, err := j.readUndoPlan(ctx, *v, *r.Undo)
	if err != nil {
		return err
	}
	if v.undoPlans == nil {
		v.undoPlans = map[string]undoPlan{}
	}
	v.undoPlans[p.Manifest.GroupID] = p
	return nil
}

type undoStopRequest struct {
	OperationID string `json:"operationID"`
	IntentSHA   string `json:"intentSHA"`
	GroupID     string `json:"groupID"`
	ManifestSHA string `json:"manifestSHA"`
	At          string `json:"at"`
}
type undoStopCommitment struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operationID"`
	GroupID     string `json:"groupID"`
	ManifestSHA string `json:"manifestSHA"`
	RequestSHA  string `json:"requestSHA"`
}

func (j *Journal) readUndoStop(ctx context.Context, v JournalView, g UndoGroup) (bool, error) {
	control, err := openDirectory(j.control)
	if err != nil {
		return false, err
	}
	defer control.Close()
	root, err := j.undoRoot(control, v.Intent.ID, g.ID, false)
	if err != nil {
		return false, ErrReconcile
	}
	defer root.Close()
	data, requestErr := readJournalFile(ctx, root, "stop.json", 2048)
	commitData, commitErr := readJournalFile(ctx, root, "stop-commit.json", 2048)
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
	var request undoStopRequest
	var commitment undoStopCommitment
	if decodeCanonical(data, &request) != nil || request.OperationID != v.Intent.ID || request.IntentSHA != v.IntentSHA || request.GroupID != g.ID || request.ManifestSHA != g.ManifestSHA || !validTime(request.At) || decodeCanonical(commitData, &commitment) != nil || commitment != (undoStopCommitment{Schema: 1, OperationID: v.Intent.ID, GroupID: g.ID, ManifestSHA: g.ManifestSHA, RequestSHA: digestJSON(request)}) {
		return false, ErrJournal
	}
	return true, sameUndoRoot(j.undoPath(v.Intent.ID, g.ID), root)
}

// The marker is independent from the movement lock so a running group can be
// stopped by another instance. It grants no new action or original-claim reuse.
func (j *Journal) RequestUndoStop(ctx context.Context, id, intentSHA, groupID, manifestSHA string, now time.Time) (JournalView, error) {
	v, err := j.Read(ctx, id)
	if err != nil {
		return JournalView{}, err
	}
	index := undoGroupIndex(v, groupID)
	if v.IntentSHA != intentSHA || index < 0 || v.UndoGroups[index].ManifestSHA != manifestSHA {
		return JournalView{}, ErrConsent
	}
	control, err := openDirectory(j.control)
	if err != nil {
		return JournalView{}, err
	}
	defer control.Close()
	root, err := j.undoRoot(control, id, groupID, false)
	if err != nil {
		return JournalView{}, err
	}
	defer root.Close()
	if err = sameUndoRoot(j.undoPath(id, groupID), root); err != nil {
		return JournalView{}, err
	}
	g := v.UndoGroups[index]
	if !g.StopRequested && g.Status != "completed" && g.Status != "stopped" {
		request := undoStopRequest{OperationID: id, IntentSHA: intentSHA, GroupID: groupID, ManifestSHA: manifestSHA, At: now.UTC().Format(time.RFC3339Nano)}
		data, _ := json.Marshal(request)
		commit, _ := json.Marshal(undoStopCommitment{Schema: 1, OperationID: id, GroupID: groupID, ManifestSHA: manifestSHA, RequestSHA: digestJSON(request)})
		err = j.publish(ctx, root, "stop-commit.json", commit)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return JournalView{}, err
		}
		if err == nil {
			if err = j.publish(ctx, root, "stop.json", data); err != nil && !errors.Is(err, os.ErrExist) {
				return JournalView{}, err
			}
		}
	}
	if err = j.fail("undo-stop-ack:before_directory_sync"); err != nil {
		return JournalView{}, err
	}
	if err = syncDirectory(root); err != nil {
		return JournalView{}, err
	}
	if err = sameUndoRoot(j.undoPath(id, groupID), root); err != nil {
		return JournalView{}, err
	}
	return j.Read(ctx, id)
}
