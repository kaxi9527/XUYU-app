package appdir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func privateRelative(path string) (*os.Root, string, error) {
	base := UXQARoot()
	if base == "" {
		return nil, "", errors.New("missing private model root")
	}
	fi, err := os.Lstat(base)
	if err != nil {
		return nil, "", err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("invalid private model root")
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", errors.New("path outside private model root")
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	part := ""
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		part = filepath.Join(part, name)
		fi, e := r.Lstat(part)
		if e != nil && !os.IsNotExist(e) {
			r.Close()
			return nil, "", e
		}
		if e == nil && fi.Mode()&os.ModeSymlink != 0 {
			r.Close()
			return nil, "", errors.New("symlink in private model state")
		}
	}
	return r, rel, nil
}

func WorkShelfReadFile(path string) ([]byte, error) {
	r, rel, err := privateRelative(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	info, err := r.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("private model state is not a regular file")
	}
	f, err := r.OpenFile(rel, privateReadFlags(), 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("private model state is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, errors.New("private model state exceeds size limit")
	}
	return b, nil
}

func WorkShelfRemoveAll(path string) error {
	r, rel, err := privateRelative(path)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.RemoveAll(rel)
}

// Validate an account directory before external credential cleanup. The later
// removal is independently rooted, so a concurrent path replacement cannot
// redirect filesystem deletion.
func WorkShelfCheckDirectory(path string) error {
	r, rel, err := privateRelative(path)
	if err != nil {
		return err
	}
	defer r.Close()
	fi, err := r.Lstat(rel)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("account path is not a directory")
	}
	return nil
}

// WorkShelfWriteFile confines all path resolution to an opened private root.
// Rename replaces an entry, never a symlink target. No check-then-WriteFile gap.
func WorkShelfWriteFile(path string, data []byte) error {
	r, rel, err := privateRelative(path)
	if err != nil {
		return err
	}
	defer r.Close()
	part := ""
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		part = filepath.Join(part, name)
		fi, err := r.Lstat(part)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (fi.Mode()&os.ModeSymlink != 0 || part == rel && !fi.Mode().IsRegular()) {
			return errors.New("private model path is not a regular file")
		}
	}
	parent := filepath.Dir(rel)
	if err = r.MkdirAll(parent, 0700); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := filepath.Join(parent, ".workshelf-write-"+hex.EncodeToString(nonce[:]))
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	_, err = f.Write(data)
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
	return r.Rename(tmp, rel)
}
