package projectmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Event segments are authoritative. index.json and memory.md are rebuildable
// views, so a crash after event publication cannot erase a committed change.
type Store struct {
	directory           string
	project             Project
	gate                chan struct{}
	afterCommit         func() error // fault injection, never part of the public API
	afterAnalysisCommit func() error // uncertain receipt fault injection
	beforeArchiveCommit func() error // unpublished archive fault injection
	beforePlacementSync func() error // local execution receipt durability fault
}
type cache struct {
	HeadSHA string `json:"headSHA"`
	State   State  `json:"state"`
}

func NewStore(base string, project Project) (*Store, error) {
	if !pathOK(base) || !idOK(project.ID) || !textOK(project.Name, 1200) || !pathOK(project.Path) {
		return nil, errors.New("项目记忆位置或稳定项目身份无效")
	}
	directory := filepath.Join(base, project.ID)
	ancestor := directory
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return nil, err
			}
			if resolved != platformPath(ancestor) {
				return nil, errors.New("项目记忆目录不能是链接")
			}
			rel, err := filepath.Rel(ancestor, directory)
			if err != nil {
				return nil, err
			}
			return &Store{directory: filepath.Join(resolved, rel), project: project, gate: make(chan struct{}, 1)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil, err
		}
		ancestor = parent
	}
}

func (s *Store) ready(create bool) (bool, error) {
	var dirs []string
	for p := filepath.Join(s.directory, "events"); ; p = filepath.Dir(p) {
		dirs = append(dirs, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		p := dirs[i]
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return false, nil
			}
			if err = os.Mkdir(p, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return false, err
			}
			parent, syncErr := os.Open(filepath.Dir(p))
			if syncErr != nil {
				return false, syncErr
			}
			syncErr = parent.Sync()
			closeErr := parent.Close()
			if syncErr != nil {
				return false, syncErr
			}
			if closeErr != nil {
				return false, closeErr
			}
			fi, err = os.Lstat(p)
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("项目记忆路径含链接或非目录")
		}
	}
	return true, nil
}
func (s *Store) acquire(ctx context.Context) (func(), error) {
	if !writesSupported() {
		return nil, errors.New("此平台尚未支持项目记忆安全写入")
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	fail := func(err error) (func(), error) { <-s.gate; return nil, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := s.ready(true); err != nil {
		return fail(err)
	}
	f, err := openRegular(filepath.Join(s.directory, "memory.lock"), true)
	if err != nil {
		return fail(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return fail(ErrCorrupt)
	}
	if err = lockContext(ctx, f); err != nil {
		f.Close()
		return fail(err)
	}
	current, e := os.Lstat(filepath.Join(s.directory, "memory.lock"))
	if e != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		unlock(f)
		f.Close()
		return fail(ErrConflict)
	}
	return func() { unlock(f); f.Close(); <-s.gate }, nil
}
func safeRead(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, ErrCorrupt
	}
	f, err := openRegular(path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, ErrCorrupt
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(b)) > limit {
		return nil, ErrCorrupt
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return b, nil
}
func decode(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrCorrupt
	}
	var more any
	if d.Decode(&more) != io.EOF {
		return ErrCorrupt
	}
	return nil
}
func recordName(rev uint64) string { return fmt.Sprintf("%020d.json", rev) }
func (s *Store) latest(ctx context.Context) (uint64, error) {
	ok, err := s.ready(false)
	if err != nil || !ok {
		return 0, err
	}
	f, err := os.Open(filepath.Join(s.directory, "events"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var latest, count uint64
	for {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		entries, e := f.ReadDir(256)
		for _, entry := range entries {
			n := entry.Name()
			if strings.HasPrefix(n, ".memory-") && strings.HasSuffix(n, ".tmp") {
				continue
			}
			rev, e := strconv.ParseUint(strings.TrimSuffix(n, ".json"), 10, 64)
			if e != nil || rev == 0 || n != recordName(rev) || entry.IsDir() {
				return 0, ErrCorrupt
			}
			count++
			if rev > latest {
				latest = rev
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return 0, e
		}
	}
	// Unique numeric filenames + count == max prove a contiguous sequence.
	if count != latest {
		return 0, ErrCorrupt
	}
	return latest, nil
}
func (s *Store) readRecord(ctx context.Context, revision uint64) (Record, []byte, error) {
	b, err := safeRead(ctx, filepath.Join(s.directory, "events", recordName(revision)), maxRecordBytes)
	if err != nil {
		return Record{}, nil, err
	}
	var r Record
	if err = decode(b, &r); err != nil || r.Schema != schema || r.Project != s.project || r.Revision != revision || !hashOK(r.RequestSHA) || !hashOK(r.StateSHA) || revision == 1 && r.PreviousSHA != "" || revision > 1 && !hashOK(r.PreviousSHA) {
		return Record{}, nil, ErrCorrupt
	}
	return r, b, nil
}
func (s *Store) read(ctx context.Context) (View, error) {
	latest, err := s.latest(ctx)
	if err != nil {
		return View{}, err
	}
	if latest == 0 {
		return View{State: State{Schema: schema, Project: s.project}}, nil
	}
	head, b, err := s.readRecord(ctx, latest)
	if err != nil {
		return View{}, err
	}
	headSHA := digest(b)
	if latest > 1 {
		_, previous, err := s.readRecord(ctx, latest-1)
		if err != nil || digest(previous) != head.PreviousSHA {
			return View{}, ErrCorrupt
		}
	}
	var state State
	validCache := false
	if raw, e := safeRead(ctx, filepath.Join(s.directory, "index.json"), maxStateBytes); e == nil {
		var c cache
		if decode(raw, &c) == nil && c.HeadSHA == headSHA && c.State.Revision == latest && c.State.Project == s.project && c.State.Validate() == nil && digestJSON(c.State) == head.StateSHA {
			state = c.State
			validCache = true
		}
	}
	if !validCache {
		previousSHA := ""
		for rev := uint64(1); rev <= latest; rev++ {
			r, data, e := s.readRecord(ctx, rev)
			if e != nil {
				return View{}, e
			}
			if r.PreviousSHA != previousSHA {
				return View{}, ErrCorrupt
			}
			state, e = applyRecord(state, r)
			if e != nil || digestJSON(state) != r.StateSHA {
				return View{}, ErrCorrupt
			}
			previousSHA = digest(data)
		}
	}
	markdown, err := safeRead(ctx, filepath.Join(s.directory, "memory.md"), maxStateBytes)
	pending := !validCache || err != nil || !bytes.Equal(markdown, renderMemory(state, headSHA))
	return View{State: state, HeadSHA: headSHA, ProjectionPending: pending}, nil
}

// Read never creates storage or repairs a projection.
func (s *Store) Read(ctx context.Context) (View, error) { return s.read(ctx) }

func (s *Store) update(ctx context.Context, revision uint64, operationID, kind string, request any, build func(State) ([]Event, error)) (Result, error) {
	if !textOK(operationID, 128) {
		return Result{}, errors.New("缺少有效操作标识")
	}
	requestSHA := digestJSON(struct {
		Kind    string
		Request any
	}{kind, request})
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	var placementPin *placementRecordPin
	if kind == "placement" || kind == "reconciliation" || kind == "undo" {
		placementPin, err = s.pinPlacementRecords()
		if err != nil {
			return Result{}, err
		}
		defer placementPin.root.Close()
	}
	view, err := s.read(ctx)
	if err != nil {
		return Result{}, err
	}
	for _, op := range view.State.Operations {
		if op.ID == operationID {
			if op.RequestSHA != requestSHA {
				return Result{}, ErrOperationReused
			}
			r, _, err := s.readRecord(ctx, op.Revision)
			if err != nil {
				return Result{}, err
			}
			result := Result{Record: r}
			if placementPin != nil {
				result.RecordSHA, err = placementPin.receipt(ctx, r)
				if err != nil {
					return Result{}, err
				}
			}
			if view.ProjectionPending {
				if err = s.projectView(ctx, view.State, view.HeadSHA); err != nil {
					result.ProjectionPending = true
					result.ProjectionError = err.Error()
				}
			}
			return result, nil
		}
	}
	if view.State.Revision != revision {
		return Result{}, ErrConflict
	}
	events, err := build(view.State)
	if err != nil {
		return Result{}, err
	}
	r := Record{Schema: schema, Project: s.project, Revision: revision + 1, PreviousSHA: view.HeadSHA, OperationID: operationID, RequestSHA: requestSHA, Events: events}
	for i := range r.Events {
		e := &r.Events[i]
		e.ID = fmt.Sprintf("%d:%d", r.Revision, i+1)
		if e.After != nil {
			e.After.LastEvent = e.ID
		}
		if e.Note != nil {
			e.Note.EventID = e.ID
		}
		if e.Origin != nil {
			e.Origin.EventID = e.ID
		}
		if e.Placement != nil {
			for index := range e.Placement.Changes {
				e.Placement.Changes[index].After.LastEvent = e.ID
			}
		}
		if e.Undo != nil {
			for index := range e.Undo.Changes {
				e.Undo.Changes[index].After.LastEvent = e.ID
			}
		}
	}
	state, err := applyRecord(view.State, r)
	if err != nil {
		return Result{}, err
	}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return Result{}, err
	}
	if len(stateBytes) > maxStateBytes {
		return Result{}, ErrBudget
	}
	r.StateSHA = digest(stateBytes)
	data, err := json.Marshal(r)
	if err != nil {
		return Result{}, err
	}
	if len(data) > maxRecordBytes {
		return Result{}, ErrBudget
	}
	data = append(data, '\n')
	if err = s.atomic(ctx, filepath.Join(s.directory, "events", recordName(r.Revision)), data, true); err != nil {
		return Result{}, err
	}
	result := Result{Record: r}
	if placementPin != nil {
		result.RecordSHA, err = placementPin.receipt(ctx, r)
		if err != nil {
			return Result{}, err
		}
	}
	if s.afterCommit != nil {
		err = s.afterCommit()
	}
	if err == nil {
		err = s.projectView(ctx, state, digest(data))
	}
	if err != nil {
		result.ProjectionPending = true
		result.ProjectionError = err.Error()
	}
	return result, nil
}
func (s *Store) atomic(ctx context.Context, target string, data []byte, exclusive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.ready(false); err != nil {
		return err
	}
	if fi, err := os.Lstat(target); err == nil {
		if exclusive || !fi.Mode().IsRegular() {
			return errors.New("项目记忆目标已存在或不是普通文件")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".memory-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
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
	if _, err = s.ready(false); err != nil {
		return err
	}
	if exclusive {
		err = os.Link(f.Name(), target)
	} else {
		err = os.Rename(f.Name(), target)
	}
	if err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) projectView(ctx context.Context, state State, headSHA string) error {
	b, err := json.Marshal(cache{HeadSHA: headSHA, State: state})
	if err != nil {
		return err
	}
	if len(b) > maxStateBytes {
		return ErrBudget
	}
	if err = s.atomic(ctx, filepath.Join(s.directory, "index.json"), b, false); err != nil {
		return err
	}
	return s.atomic(ctx, filepath.Join(s.directory, "memory.md"), renderMemory(state, headSHA), false)
}
func (s *Store) RepairProjection(ctx context.Context) (View, error) {
	unlock, err := s.acquire(ctx)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	v, err := s.read(ctx)
	if err != nil || v.State.Revision == 0 {
		return v, err
	}
	if err = s.projectView(ctx, v.State, v.HeadSHA); err != nil {
		return v, err
	}
	v.ProjectionPending = false
	return v, nil
}

// History verifies each returned segment and its predecessor. It is not a claim
// that every unrequested historical segment was audited during this read.
func (s *Store) History(ctx context.Context, after uint64, limit int) ([]Record, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("历史每页应为 1–100 段")
	}
	latest, err := s.latest(ctx)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	if after >= latest {
		return out, nil
	}
	previousSHA := ""
	if after > 0 {
		_, b, e := s.readRecord(ctx, after)
		if e != nil {
			return nil, e
		}
		previousSHA = digest(b)
	}
	remaining := int64(maxStateBytes)
	for rev := after + 1; rev <= latest && len(out) < limit; rev++ {
		r, b, e := s.readRecord(ctx, rev)
		if e != nil {
			return nil, e
		}
		remaining -= int64(len(b))
		if remaining < 0 {
			return nil, ErrBudget
		}
		if r.PreviousSHA != previousSHA {
			return nil, ErrCorrupt
		}
		out = append(out, r)
		previousSHA = digest(b)
	}
	return out, nil
}
