package versions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrContentChanged = errors.New("文件内容已变化，请重新检查")

func sameFileState(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Fingerprint reads a complete regular file under one explicit authorized root.
// It rejects links below that root and returns no partial digest on cancellation,
// size limits, replacement, or changes while reading. It does not infer permission.
func Fingerprint(ctx context.Context, root, path string, maxBytes int64) (Content, error) {
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	if !validPath(root) || !validPath(path) || !within(path, root) || maxBytes <= 0 {
		return Content{}, errors.New("文件不在允许范围内，或内容读取限额无效")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return Content{}, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Content{}, errors.New("允许位置必须是实际文件夹")
	}
	// Resolve system aliases above the selected root (e.g. macOS /var); never
	// resolve links inside it. Each real ancestor is rechecked after the read.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Content{}, err
	}
	rel, _ := filepath.Rel(root, path)
	realPath := filepath.Join(canonicalRoot, rel)
	ancestors := map[string]os.FileInfo{canonicalRoot: rootInfo}
	current := canonicalRoot
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		fi, e := os.Lstat(current)
		if e != nil {
			return Content{}, e
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return Content{}, errors.New("文件路径包含链接或非文件夹")
		}
		ancestors[current] = fi
	}
	before, err := os.Lstat(realPath)
	if err != nil {
		return Content{}, err
	}
	if !before.Mode().IsRegular() {
		return Content{}, errors.New("只支持普通文件，链接与文件夹不能登记为内容版本")
	}
	if before.Size() > maxBytes {
		return Content{}, fmt.Errorf("文件超过本次内容读取限额 %d 字节", maxBytes)
	}
	f, err := os.Open(realPath)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameFileState(before, opened) {
		return Content{}, ErrContentChanged
	}
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return Content{}, err
		}
		n, readErr := f.Read(buffer)
		total += int64(n)
		if total > maxBytes || total > before.Size() {
			return Content{}, ErrContentChanged
		}
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Content{}, readErr
		}
	}
	if err = ctx.Err(); err != nil {
		return Content{}, err
	}
	after, err := f.Stat()
	if err != nil || !sameFileState(before, after) || total != before.Size() {
		return Content{}, ErrContentChanged
	}
	atPath, err := os.Lstat(realPath)
	if err != nil || !sameFileState(before, atPath) {
		return Content{}, ErrContentChanged
	}
	for ancestor, expected := range ancestors {
		now, e := os.Lstat(ancestor)
		if e != nil || !now.IsDir() || !os.SameFile(expected, now) {
			return Content{}, ErrContentChanged
		}
	}
	rootNow, e := os.Lstat(root)
	if e != nil || !rootNow.IsDir() || !os.SameFile(rootInfo, rootNow) {
		return Content{}, ErrContentChanged
	}
	return Content{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: total}, nil
}
