package projectmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type placementRecordPin struct {
	store *Store
	root  *os.Root
	info  os.FileInfo
}

func (s *Store) pinPlacementRecords() (*placementRecordPin, error) {
	if _, err := s.ready(false); err != nil {
		return nil, err
	}
	path := filepath.Join(s.directory, "events")
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, ErrCorrupt
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(before, info) {
		root.Close()
		return nil, ErrCorrupt
	}
	return &placementRecordPin{store: s, root: root, info: info}, nil
}

func (p *placementRecordPin) bound() error {
	if _, err := p.store.ready(false); err != nil {
		return err
	}
	current, err := os.Lstat(filepath.Join(p.store.directory, "events"))
	if err != nil || !current.IsDir() || !os.SameFile(current, p.info) {
		return ErrCorrupt
	}
	return nil
}

// The hash is read from the actual immutable segment, including its newline.
// A visible record alone cannot acknowledge a previously failed directory Sync.
func (p *placementRecordPin) receipt(ctx context.Context, record Record) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.bound(); err != nil {
		return "", err
	}
	name := recordName(record.Revision)
	before, err := p.root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxRecordBytes {
		return "", ErrCorrupt
	}
	f, err := p.root.Open(name)
	if err != nil {
		return "", err
	}
	opened, e1 := f.Stat()
	data, e2 := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	after, e3 := f.Stat()
	closeErr := f.Close()
	current, e4 := p.root.Lstat(name)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || closeErr != nil || !current.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != int64(len(data)) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", ErrCorrupt
	}
	expected, err := json.Marshal(record)
	if err != nil || !bytes.Equal(data, append(expected, '\n')) {
		return "", ErrCorrupt
	}
	if p.store.beforePlacementSync != nil {
		if err := p.store.beforePlacementSync(); err != nil {
			return "", err
		}
	}
	d, err := p.root.Open(".")
	if err != nil {
		return "", err
	}
	err = d.Sync()
	closeErr = d.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := p.bound(); err != nil {
		return "", err
	}
	return digest(data), nil
}
