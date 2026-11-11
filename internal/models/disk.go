package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

const maxConfigBytes = 2 << 20

func canonicalDirectory(directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", errors.New("模型配置目录必须是完整路径")
	}
	clean := filepath.Clean(directory)
	for ancestor := clean; ; ancestor = filepath.Dir(ancestor) {
		fi, err := os.Lstat(ancestor)
		if err == nil {
			if ancestor == clean && fi.Mode()&os.ModeSymlink != 0 {
				return "", ErrCorrupt
			}
			resolved, e := filepath.EvalSymlinks(ancestor)
			if e != nil {
				return "", e
			}
			rel, _ := filepath.Rel(ancestor, clean)
			return filepath.Join(resolved, rel), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(ancestor) == ancestor {
			return "", err
		}
	}
}
func (s *Store) directoryReady(create bool) (bool, error) {
	var chain []string
	for p := s.directory; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		fi, err := os.Lstat(chain[i])
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return false, nil
			}
			if err = os.Mkdir(chain[i], 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return false, err
			}
			fi, err = os.Lstat(chain[i])
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, ErrCorrupt
		}
	}
	return true, nil
}
func (s *Store) readBytes(name string) ([]byte, error) {
	ready, err := s.directoryReady(false)
	if err != nil || !ready {
		return nil, err
	}
	path := filepath.Join(s.directory, name)
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxConfigBytes {
		return nil, ErrCorrupt
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrCorrupt
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || len(data) > maxConfigBytes {
		return nil, ErrCorrupt
	}
	return data, nil
}
func decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrCorrupt
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrCorrupt
	}
	return nil
}
func (s *Store) readCatalog() (catalog, []byte, error) {
	c := catalog{Schema: 1, Providers: []Provider{}, Models: []Model{}, Settings: Settings{Mode: "tool"}, Operations: []Receipt{}}
	data, err := s.readBytes("connections.json")
	if err != nil {
		return c, nil, err
	}
	if data == nil {
		return c, nil, nil
	}
	if err = decode(data, &c); err != nil {
		return c, data, err
	}
	return c, data, validate(c)
}
func validate(c catalog) error {
	if c.Schema != 1 || c.Revision > 1<<53-1 || c.Settings.Epoch > 1<<53-1 || (c.Settings.Mode != "tool" && c.Settings.Mode != "agent") {
		return ErrCorrupt
	}
	providers := map[string]Provider{}
	models := map[string]bool{}
	for _, p := range c.Providers {
		if _, err := NormalizeOptions(p.Protocol, p.Options); err != nil {
			return ErrCorrupt
		}
		endpoint, e := normalizeEndpoint(p.BaseURL)
		if p.ID == "" || providers[p.ID].ID != "" || !label(p.Name, 120) || !validProtocol(p.Protocol) || e != nil || endpoint != p.BaseURL || p.Version == 0 || p.Version > 1<<53-1 || (p.SecretRef != "" && !credentials.ValidReference(p.SecretRef)) {
			return ErrCorrupt
		}
		providers[p.ID] = p
	}
	for _, m := range c.Models {
		p, ok := providers[m.ProviderID]
		if !ok || m.ID == "" || models[m.ID] || !label(m.Name, 120) || !label(m.ModelID, 256) {
			return ErrCorrupt
		}
		models[m.ID] = true
		if v := m.Verification; v != nil {
			if v.ProviderVersion != p.Version || !validCapabilities(v.Capabilities) || v.Capabilities.Text != "supported" || v.At == "" {
				return ErrCorrupt
			}
		}
	}
	if c.Settings.ActiveModelID != "" && !models[c.Settings.ActiveModelID] {
		return ErrCorrupt
	}
	if c.Settings.Mode == "agent" {
		found := false
		for _, m := range c.Models {
			if m.ID == c.Settings.ActiveModelID && m.Verification != nil {
				found = true
			}
		}
		if !found {
			return ErrCorrupt
		}
	}
	ops := map[string]bool{}
	for _, op := range c.Operations {
		if op.Problem != nil && (!op.Problem.Valid() || op.Verified == nil || *op.Verified) {
			return ErrCorrupt
		}
		if !label(op.OperationID, 128) || op.Revision == 0 || op.Revision > c.Revision || len(op.Digest) != 64 || ops[op.OperationID] {
			return ErrCorrupt
		}
		ops[op.OperationID] = true
	}
	return nil
}
func (s *Store) writeAtomic(ctx context.Context, name string, data, original []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(data) > maxConfigBytes {
		return false, errors.New("模型配置已达到本地保存限额")
	}
	if _, err := s.directoryReady(false); err != nil {
		return false, err
	}
	dirInfo, err := os.Stat(s.directory)
	if err != nil {
		return false, err
	}
	f, err := os.CreateTemp(s.directory, ".model-config-*.tmp")
	if err != nil {
		return false, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return false, err
	}
	current, err := s.readBytes(name)
	if err != nil || !bytes.Equal(current, original) {
		return false, ErrConflict
	}
	newDir, err := os.Stat(s.directory)
	if err != nil || !os.SameFile(dirInfo, newDir) {
		return false, ErrConflict
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if err = s.replace(temp, filepath.Join(s.directory, name)); err != nil {
		return false, err
	}
	dir, err := os.Open(s.directory)
	if err == nil {
		defer dir.Close()
		err = dir.Sync()
	}
	return true, err
}
