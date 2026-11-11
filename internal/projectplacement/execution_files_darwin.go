//go:build darwin && cgo

package projectplacement

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

type executionRound struct {
	executor *Executor
	session  *journalSession
	expected map[string]string
	hashed   int64
	created  []Directory
}

type preparedFile struct {
	source, target                   *os.Root
	sourceDirectory, targetDirectory Directory
	before                           os.FileInfo
	missing                          bool
	builder                          *previewBuilder
}

func (p *preparedFile) close() {
	if p == nil {
		return
	}
	if p.source != nil {
		p.source.Close()
		p.source = nil
	}
	if p.target != nil {
		p.target.Close()
		p.target = nil
	}
}

func (r *executionRound) builder(ctx context.Context) (*previewBuilder, error) {
	b := &previewBuilder{ctx: ctx, authority: r.session.view.Intent.Preview.Authority, limits: PreviewLimits{MaxFileBytes: 256 << 20, MaxBytes: 8 << 30, MaxEntries: 20000}, directories: map[string]Directory{}, checks: map[string]directoryCheck{}, reserved: map[string]bool{}, excludedObjects: map[string]bool{}, excludedSlots: map[string]bool{}, exclusionChecks: map[string]string{}, hashed: r.hashed}
	if err := b.loadExclusions(); err != nil {
		return nil, err
	}
	return b, nil
}

func (r *executionRound) checkDirectoryObjects(b *previewBuilder) error {
	for path, dir := range b.directories {
		if r.expected[path] == "" || r.expected[path] != dir.Object {
			return ErrChanged
		}
	}
	return b.checkDirectories()
}

func boundDirectory(path string, root *os.Root) error {
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

func destinationFree(root *os.Root, name string) error {
	before, err := root.Stat(".")
	if err != nil {
		return err
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	names, readErr := f.Readdirnames(20001)
	f.Close()
	if len(names) > 20000 || readErr != nil && !errors.Is(readErr, io.EOF) {
		return ErrChanged
	}
	key := pathKey(name)
	if key == "" {
		return ErrScope
	}
	for _, existing := range names {
		if pathKey(existing) == key {
			return ErrChanged
		}
	}
	after, err := root.Stat(".")
	if err != nil || directoryFact("/", before) != directoryFact("/", after) {
		return ErrChanged
	}
	return nil
}

func (r *executionRound) preflight(ctx context.Context, row Row) (*preparedFile, error) {
	if protectedPath(row.Source) || protectedPath(row.Target) {
		return nil, ErrScope
	}
	b, err := r.builder(ctx)
	if err != nil {
		return nil, err
	}
	p := &preparedFile{builder: b}
	failed := func(err error) (*preparedFile, error) { p.close(); return nil, err }
	var check directoryCheck
	p.source, p.sourceDirectory, _, check, err = b.guard(filepath.Dir(row.Source), false)
	if err != nil {
		return failed(err)
	}
	if check.code != "" {
		return failed(ErrScope)
	}
	p.target, p.targetDirectory, p.missing, check, err = b.guard(filepath.Dir(row.Target), true)
	if err != nil {
		return failed(err)
	}
	if check.code != "" {
		return failed(ErrScope)
	}
	p.before, err = p.source.Lstat(filepath.Base(row.Source))
	if err != nil {
		return failed(err)
	}
	key, _, single := object(p.before)
	if !p.before.Mode().IsRegular() || !single || key != row.Object || b.excludedObjects[key] || p.before.Size() != row.Before.Size || p.before.ModTime().UTC().Format(time.RFC3339Nano) != row.Before.ModifiedAt || projectmemory.ObjectIdentity(p.before) != row.Before.ObjectKey {
		return failed(ErrChanged)
	}
	sha, err := b.hash(p.source, filepath.Base(row.Source), p.before)
	r.hashed = b.hashed
	if err != nil {
		return failed(err)
	}
	if sha != row.Before.SHA256 {
		return failed(ErrChanged)
	}
	if !p.missing {
		if err = destinationFree(p.target, filepath.Base(row.Target)); err != nil {
			return failed(err)
		}
	}
	if err = r.checkDirectoryObjects(b); err != nil {
		return failed(err)
	}
	probe := r.executor.busy
	if probe == nil {
		probe = checkActive
	}
	if err = probe(ctx, row.Source); err != nil {
		return failed(err)
	}
	return p, nil
}

func (r *executionRound) makeTargetDirectories(ctx context.Context, row Row, p *preparedFile) error {
	if !p.missing {
		return nil
	}
	rel, err := filepath.Rel(p.targetDirectory.Path, filepath.Dir(row.Target))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ErrScope
	}
	parent := p.target
	path := p.targetDirectory.Path
	owned := []*os.Root{}
	defer func() {
		for _, root := range owned {
			root.Close()
		}
	}()
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if !safeName(name) {
			return ErrScope
		}
		if err = r.executor.checkCurrent(ctx, r.session.view, row); err != nil {
			return err
		}
		if err = r.session.checkStop(ctx); err != nil {
			return err
		}
		if err = boundDirectory(path, parent); err != nil {
			return err
		}
		nextPath := filepath.Join(path, name)
		if !r.session.view.Intent.Preview.Authority.allowed(nextPath) || protectedPath(nextPath) {
			return ErrScope
		}
		if err = parent.Mkdir(name, 0o755); err != nil {
			return err
		} // Never adopt a concurrent creator.
		if err = syncDirectory(parent); err != nil {
			return err
		}
		next, err := journalChild(parent, name, false, false)
		if err != nil {
			return err
		}
		owned = append(owned, next)
		info, err := next.Stat(".")
		if err != nil {
			return err
		}
		dir := directoryFact(nextPath, info)
		r.expected[nextPath] = dir.Object
		r.created = append(r.created, dir)
		parent, path = next, nextPath
	}
	return nil
}

func (r *executionRound) actionReady(ctx context.Context, row Row, p *preparedFile) error {
	if p.missing {
		return ErrChanged
	}
	if err := r.executor.checkCurrent(ctx, r.session.view, row); err != nil {
		return err
	}
	if err := r.session.checkStop(ctx); err != nil {
		return err
	}
	if err := boundDirectory(filepath.Dir(row.Source), p.source); err != nil {
		return err
	}
	if err := boundDirectory(filepath.Dir(row.Target), p.target); err != nil {
		return err
	}
	current, err := p.source.Lstat(filepath.Base(row.Source))
	if err != nil || !sameFile(current, p.before) {
		return ErrChanged
	}
	if err = r.checkDirectoryObjects(p.builder); err != nil {
		return err
	}
	return destinationFree(p.target, filepath.Base(row.Target))
}

func (r *executionRound) placed(ctx context.Context, row Row, wantObject string, p *preparedFile) (PhysicalReceipt, error) {
	if err := boundDirectory(filepath.Dir(row.Source), p.source); err != nil {
		return PhysicalReceipt{}, err
	}
	if err := boundDirectory(filepath.Dir(row.Target), p.target); err != nil {
		return PhysicalReceipt{}, err
	}
	b, err := r.builder(ctx)
	if err != nil {
		return PhysicalReceipt{}, err
	}
	src, sd, _, sc, err := b.guard(filepath.Dir(row.Source), false)
	if err != nil {
		return PhysicalReceipt{}, err
	}
	if src != nil {
		defer src.Close()
	}
	if sc.code != "" {
		return PhysicalReceipt{}, ErrChanged
	}
	dst, td, missing, dc, err := b.guard(filepath.Dir(row.Target), false)
	if err != nil {
		return PhysicalReceipt{}, err
	}
	if dst != nil {
		defer dst.Close()
	}
	if dc.code != "" || missing {
		return PhysicalReceipt{}, ErrChanged
	}
	before, err := dst.Lstat(filepath.Base(row.Target))
	if err != nil {
		return PhysicalReceipt{}, err
	}
	key, _, single := object(before)
	if key != wantObject || !single || !before.Mode().IsRegular() || before.Size() != row.Before.Size {
		return PhysicalReceipt{}, ErrChanged
	}
	sha, err := b.hash(dst, filepath.Base(row.Target), before)
	r.hashed = b.hashed
	if err != nil {
		return PhysicalReceipt{}, err
	}
	if sha != row.Before.SHA256 {
		return PhysicalReceipt{}, ErrChanged
	}
	if row.Action == "move" {
		if _, err = src.Lstat(filepath.Base(row.Source)); !errors.Is(err, os.ErrNotExist) {
			return PhysicalReceipt{}, ErrChanged
		}
	} else {
		current, err := src.Lstat(filepath.Base(row.Source))
		if err != nil || !sameFile(current, p.before) {
			return PhysicalReceipt{}, ErrChanged
		}
	}
	if err = r.checkDirectoryObjects(b); err != nil {
		return PhysicalReceipt{}, err
	}
	if err = syncDirectory(src); err != nil {
		return PhysicalReceipt{}, err
	}
	if err = syncDirectory(dst); err != nil {
		return PhysicalReceipt{}, err
	}
	if err = boundDirectory(filepath.Dir(row.Source), src); err != nil {
		return PhysicalReceipt{}, err
	}
	if err = boundDirectory(filepath.Dir(row.Target), dst); err != nil {
		return PhysicalReceipt{}, err
	}
	after := projectmemory.Fact{Path: row.Target, Kind: "file", Size: before.Size(), ModifiedAt: before.ModTime().UTC().Format(time.RFC3339Nano), ObjectKey: projectmemory.ObjectIdentity(before), SHA256: sha, HashState: "verified"}
	dirs := make([]Directory, 0, len(b.directories))
	for _, dir := range b.directories {
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path < dirs[j].Path })
	outcome := "moved"
	if row.Action == "copy" {
		outcome = "copied"
	}
	return PhysicalReceipt{Outcome: outcome, Before: row.Before, After: &after, SourceObject: row.Object, TargetObject: key, SourceParent: sd.Object, TargetParent: td.Object, Directories: dirs, Reason: "已核验目录绑定、文件对象和完整内容，并同步文件所在目录"}, nil
}
