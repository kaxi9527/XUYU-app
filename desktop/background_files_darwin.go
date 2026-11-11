//go:build darwin

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errBackgroundChanged = errors.New("后台配置的位置或文件已变化，未继续修改，请重新读取")

// Use the pinned-directory and same-handle read pattern of the project journal.
func openBackgroundDirectory(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, errBackgroundChanged
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/") {
		if part == "" {
			continue
		}
		before, e := root.Lstat(part)
		if errors.Is(e, os.ErrNotExist) && create {
			e = root.Mkdir(part, 0700)
			if e != nil && !errors.Is(e, os.ErrExist) {
				root.Close()
				return nil, e
			}
			before, e = root.Lstat(part)
		}
		if e != nil {
			root.Close()
			return nil, e
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, errBackgroundChanged
		}
		next, e := root.OpenRoot(part)
		if e != nil {
			root.Close()
			return nil, e
		}
		a, e1 := next.Stat(".")
		b, e2 := root.Lstat(part)
		if e1 != nil || e2 != nil || !b.IsDir() || !os.SameFile(before, a) || !os.SameFile(before, b) {
			next.Close()
			root.Close()
			return nil, errBackgroundChanged
		}
		root.Close()
		root = next
	}
	return root, nil
}

func sameBackgroundDirectory(path string, root *os.Root) error {
	current, err := openBackgroundDirectory(path, false)
	if err != nil {
		return errBackgroundChanged
	}
	defer current.Close()
	a, e1 := root.Stat(".")
	b, e2 := current.Stat(".")
	if e1 != nil || e2 != nil || !os.SameFile(a, b) {
		return errBackgroundChanged
	}
	return nil
}

func readBackgroundAt(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 16*1024 {
		return nil, nil, errBackgroundChanged
	}
	b, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil {
		return nil, nil, err
	}
	after, e1 := f.Stat()
	current, e2 := root.Lstat(name)
	if e1 != nil || e2 != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || !current.Mode().IsRegular() || after.Size() != before.Size() || after.ModTime() != before.ModTime() || int64(len(b)) != before.Size() {
		return nil, nil, errBackgroundChanged
	}
	return b, before, nil
}

func readBackgroundFile(path string) ([]byte, error) {
	root, err := openBackgroundDirectory(filepath.Dir(path), false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, _, err := readBackgroundAt(root, filepath.Base(path))
	if err == nil {
		err = sameBackgroundDirectory(filepath.Dir(path), root)
	}
	return b, err
}

func lockBackgroundDirectory(path string) (*os.Root, func(), error) {
	root, err := openBackgroundDirectory(path, true)
	if err != nil {
		return nil, nil, err
	}
	f, err := root.OpenFile(".workshelf-background.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		root.Close()
		return nil, nil, errBackgroundChanged
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return root, func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close(); root.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			root.Close()
			return nil, nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func syncBackgroundDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Keep the retired entry instead of unlinking a pathname another writer could
// replace. A changed claimant is restored exclusively, or retained for recovery.
func retireBackgroundFile(root *os.Root, name string, expected []byte) (string, error) {
	b, identity, err := readBackgroundAt(root, name)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(b, expected) {
		return "", errBackgroundChanged
	}
	if identity == nil {
		return "", nil
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return "", err
	}
	retired := ".workshelf-retired-" + hex.EncodeToString(nonce[:]) + ".disabled"
	if err = renameBackgroundExclusive(root, name, retired); err != nil {
		return "", err
	}
	claimed, after, err := readBackgroundAt(root, retired)
	if err != nil || after == nil || !os.SameFile(identity, after) || !bytes.Equal(claimed, expected) {
		if restore := renameBackgroundExclusive(root, retired, name); restore != nil {
			return retired, fmt.Errorf("%w；冲突文件保留在 %s", errBackgroundChanged, retired)
		}
		return "", errBackgroundChanged
	}
	return retired, syncBackgroundDirectory(root)
}

func createBackgroundFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
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
	return syncBackgroundDirectory(root)
}

func writeBackgroundPreferences(root *os.Root, path string, previous, data []byte) error {
	if err := sameBackgroundDirectory(filepath.Dir(path), root); err != nil {
		return err
	}
	name := filepath.Base(path)
	retired, err := retireBackgroundFile(root, name, previous)
	if err != nil {
		return err
	}
	if err = createBackgroundFile(root, name, data); err != nil {
		if retired != "" {
			_ = renameBackgroundExclusive(root, retired, name)
		}
		return err
	}
	return sameBackgroundDirectory(filepath.Dir(path), root)
}

func (f backgroundFiles) setLogin(enabled bool) error {
	path := f.loginPath()
	root, unlock, err := lockBackgroundDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()
	b, _, err := readBackgroundAt(root, filepath.Base(path))
	if err != nil {
		return err
	}
	if len(b) > 0 && !bytes.Equal(b, f.loginDocument()) {
		return errors.New("登录启动项已变化，未作修改")
	}
	if err = sameBackgroundDirectory(filepath.Dir(path), root); err != nil {
		return err
	}
	if enabled && len(b) == 0 {
		err = createBackgroundFile(root, filepath.Base(path), f.loginDocument())
	}
	if !enabled && len(b) > 0 {
		_, err = retireBackgroundFile(root, filepath.Base(path), b)
	}
	if err != nil {
		return err
	}
	return sameBackgroundDirectory(filepath.Dir(path), root)
}
