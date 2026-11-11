//go:build (darwin && cgo) || linux

package projectplacement

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func previewSupported() bool { return true }

// os.Root stays bound to an opened directory. We additionally reject links in
// every ancestor, including links that would remain inside the same root.
// Only macOS's system /var and /tmp aliases are normalized.
func openDirectory(path string) (*os.Root, error) {
	if !absolute(path) && path != "/" {
		return nil, ErrScope
	}
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/var", "/tmp"} {
			if within(alias, path) {
				path = "/private" + path
				break
			}
		}
	}
	r, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if name == "" {
			continue
		}
		before, err := r.Lstat(name)
		if err != nil || !before.IsDir() {
			r.Close()
			if err != nil {
				return nil, err
			}
			return nil, ErrScope
		}
		next, err := r.OpenRoot(name)
		if err != nil {
			r.Close()
			return nil, err
		}
		after, e1 := next.Stat(".")
		current, e2 := r.Lstat(name)
		r.Close()
		if e1 != nil || e2 != nil || !current.IsDir() || !os.SameFile(before, current) || !os.SameFile(before, after) {
			next.Close()
			return nil, ErrChanged
		}
		r = next
	}
	return r, nil
}

func object(info os.FileInfo) (string, uint64, bool) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, false
	}
	key := projectmemory.ObjectIdentity(info)
	if key == "" {
		// This is a preview identity, not the observer's persistent FileID.
		key = fmt.Sprintf("%s:%d:%d", runtime.GOOS, s.Dev, s.Ino)
	}
	return key, uint64(s.Dev), s.Nlink == 1
}

func directoryFact(path string, info os.FileInfo) Directory {
	key, device, _ := object(info)
	change := changeTime(info)
	return Directory{Path: path, Object: key, Device: device, ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano), ChangedAt: fmt.Sprintf("%d:%d", change.Sec, change.Nsec)}
}

func sameFile(a, b os.FileInfo) bool {
	ka, _, oneA := object(a)
	kb, _, oneB := object(b)
	return ka != "" && ka == kb && oneA && oneB && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && changeTime(a) == changeTime(b)
}

func bundleName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", "node_modules", ".venv", "venv", "go.mod", "go.work", "package.json", "pyproject.toml", "cargo.toml", "pom.xml", "package.swift", "makefile", "cmakelists.txt":
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".prproj", ".aep", ".drp", ".fcpxml", ".fcpbundle", ".als", ".logicx", ".band":
		return true
	}
	return false
}

// Existing excluded objects also block Unicode/case aliases on filesystems
// where differently spelled names refer to the same object. Only metadata is
// read; excluded directory contents are never enumerated.
func exclusionObject(path string) (string, string, error) {
	r, err := openDirectory(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	defer r.Close()
	parent, err := r.Stat(".")
	if err != nil {
		return "", "", err
	}
	parentKey, _, _ := object(parent)
	if parentKey == "" {
		return "", "", ErrScope
	}
	info, err := r.Lstat(filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", parentKey, nil
	}
	if err != nil {
		return "", "", err
	}
	key, _, _ := object(info)
	if key == "" {
		return "", "", ErrScope
	}
	return key, parentKey, nil
}

func (b *previewBuilder) loadExclusions() error {
	for _, path := range b.authority.Excluded {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		key, parent, err := exclusionObject(path)
		if err != nil {
			return ErrScope
		}
		b.exclusionChecks[path] = parent + ":" + key
		name := pathKey(filepath.Base(path))
		if name == "" {
			return ErrScope
		}
		if parent != "" {
			b.excludedSlots[parent+"/"+name] = true
		}
		if key != "" {
			b.excludedObjects[key] = true
		}
	}
	return nil
}

func (b *previewBuilder) inspect(path string) (*os.Root, Directory, directoryCheck, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, Directory{}, directoryCheck{}, err
	}
	r, err := openDirectory(path)
	if err != nil {
		return nil, Directory{}, directoryCheck{}, err
	}
	before, err := r.Stat(".")
	if err != nil {
		r.Close()
		return nil, Directory{}, directoryCheck{}, err
	}
	d := directoryFact(path, before)
	if b.excludedObjects[d.Object] {
		check := directoryCheck{"excluded", "这个位置已排除，不参与整理"}
		b.directories[path], b.checks[path] = d, check
		r.Close()
		return nil, d, check, nil
	}
	if old, ok := b.directories[path]; ok {
		if old != d {
			r.Close()
			return nil, Directory{}, directoryCheck{}, ErrChanged
		}
		return r, d, b.checks[path], nil
	}
	check := directoryCheck{}
	remaining := b.limits.MaxEntries - b.entries
	if remaining <= 0 {
		check = directoryCheck{"inspection_limit", "目录检查达到本轮限额，暂时保留原处"}
		b.directories[path], b.checks[path] = d, check
		return r, d, check, nil
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, Directory{}, check, err
	}
	names, readErr := f.Readdirnames(remaining + 1)
	f.Close()
	b.entries += len(names)
	if len(names) > remaining {
		check = directoryCheck{"inspection_limit", "目录检查达到本轮限额，暂时保留原处"}
	} else if readErr != nil && !errors.Is(readErr, io.EOF) {
		check = directoryCheck{"unavailable", "目录未能完整检查，暂时保留原处"}
	} else {
		for _, name := range names {
			if bundleName(name) {
				check = directoryCheck{"protected_project", "完整工程及其依赖保留原位"}
				break
			}
		}
	}
	after, err := r.Stat(".")
	if err != nil || directoryFact(path, after) != d {
		r.Close()
		return nil, Directory{}, check, ErrChanged
	}
	b.directories[path], b.checks[path] = d, check
	return r, d, check, nil
}

// guard checks the host boundary itself and every descendant directory. If a
// target suffix is absent, return the nearest existing parent without making it.
func (b *previewBuilder) guard(path string, allowMissing bool) (*os.Root, Directory, bool, directoryCheck, error) {
	base := b.authority.boundary(path)
	if base == "" {
		return nil, Directory{}, false, directoryCheck{}, ErrScope
	}
	rel, _ := filepath.Rel(base, path)
	paths := []string{base}
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			paths = append(paths, filepath.Join(paths[len(paths)-1], part))
		}
	}
	var parent *os.Root
	var d Directory
	for _, p := range paths {
		r, next, check, err := b.inspect(p)
		if err != nil {
			if allowMissing && parent != nil && errors.Is(err, os.ErrNotExist) {
				name := pathKey(filepath.Base(p))
				if name == "" {
					parent.Close()
					return nil, d, false, directoryCheck{}, ErrScope
				}
				if b.excludedSlots[d.Object+"/"+name] {
					parent.Close()
					return nil, d, false, directoryCheck{"excluded", "这个位置已排除，不参与整理"}, nil
				}
				return parent, d, true, directoryCheck{}, nil
			}
			if parent != nil {
				parent.Close()
			}
			return nil, Directory{}, false, directoryCheck{}, err
		}
		if parent != nil {
			parent.Close()
		}
		if check.code != "" {
			if r != nil {
				r.Close()
			}
			return nil, next, false, check, nil
		}
		parent, d = r, next
	}
	return parent, d, false, directoryCheck{}, nil
}

func (b *previewBuilder) checkDirectories() error {
	for path, old := range b.exclusionChecks {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		key, parent, err := exclusionObject(path)
		if err != nil || parent+":"+key != old {
			return ErrChanged
		}
	}
	for path, old := range b.directories {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		r, err := openDirectory(path)
		if err != nil {
			return ErrChanged
		}
		info, err := r.Stat(".")
		r.Close()
		if err != nil || directoryFact(path, info) != old {
			return ErrChanged
		}
	}
	return nil
}

func (b *previewBuilder) hash(parent *os.Root, name string, before os.FileInfo) (string, error) {
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !sameFile(before, opened) {
		return "", ErrChanged
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := b.ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		total += int64(n)
		b.hashed += int64(n)
		if total > before.Size() || total > b.limits.MaxFileBytes || b.hashed > b.limits.MaxBytes {
			return "", ErrChanged
		}
		h.Write(buf[:n])
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", err
			}
			break
		}
	}
	after, err := f.Stat()
	current, checkErr := parent.Lstat(name)
	if err != nil || checkErr != nil || !sameFile(before, after) || !sameFile(before, current) || total != before.Size() {
		return "", ErrChanged
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (b *previewBuilder) rows(p projectcontext.PlacementProposal) ([]Row, error) {
	rows := make([]Row, 0, len(p.Suggestions))
	for _, s := range p.Suggestions {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		row, err := b.row(s)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (b *previewBuilder) row(s projectcontext.PlacementSuggestion) (Row, error) {
	old := s.File.Fact
	row := Row{FileID: s.File.ID, Source: old.Path, Folder: s.Folder, Action: "keep", Reference: s.Reference, Before: old}
	keep := func(code, reason string) (Row, error) {
		row.Code, row.Reason = code, reason
		return row, nil
	}
	if protectedPath(old.Path) || protectedPath(s.SuggestedTarget) {
		return keep("protected_path", "隐藏配置、凭据、依赖或数据库保留原位")
	}
	src, _, _, check, err := b.guard(filepath.Dir(old.Path), false)
	if errors.Is(err, ErrChanged) {
		return Row{}, err
	}
	if err != nil {
		return keep("source_unavailable", "来源无法安全读取，暂时保留原处")
	}
	if check.code != "" {
		return keep(check.code, check.reason)
	}
	defer src.Close()
	before, err := src.Lstat(filepath.Base(old.Path))
	if err != nil {
		return keep("source_unavailable", "来源当前不可访问，请重新观察项目")
	}
	key, _, single := object(before)
	if !before.Mode().IsRegular() || !single || key == "" {
		return keep("protected_object", "链接、共享硬链接或特殊文件保留原位")
	}
	if b.excludedObjects[key] {
		return keep("excluded", "这个文件已排除，不参与整理")
	}
	if before.Size() != old.Size || before.ModTime().UTC().Format(time.RFC3339Nano) != old.ModifiedAt || old.ObjectKey != "" && old.ObjectKey != projectmemory.ObjectIdentity(before) {
		return keep("source_changed", "文件已变化，请重新观察并分析")
	}
	if before.Size() > b.limits.MaxFileBytes || before.Size() > b.limits.MaxBytes-b.hashed {
		return keep("verification_limit", "完整内容核验达到本轮限额，暂时保留原处")
	}
	target, td, missing, check, err := b.guard(filepath.Dir(s.SuggestedTarget), true)
	if errors.Is(err, ErrChanged) {
		return Row{}, err
	}
	if err != nil {
		return keep("target_unavailable", "目标目录无法安全检查，暂时保留原处")
	}
	if check.code != "" {
		return keep(check.code, check.reason)
	}
	defer target.Close()
	sha, err := b.hash(src, filepath.Base(old.Path), before)
	if err != nil {
		if b.ctx.Err() != nil {
			return Row{}, b.ctx.Err()
		}
		return keep("source_changed", "文件读取失败或读取期间变化，暂时保留原处")
	}
	if old.HashState == "verified" && old.SHA256 != sha {
		return keep("source_changed", "文件内容与建议依据不同，请重新观察并分析")
	}
	row.Before = projectmemory.Fact{Path: old.Path, Kind: "file", Size: before.Size(), ModifiedAt: old.ModifiedAt, ObjectKey: projectmemory.ObjectIdentity(before), SHA256: sha, HashState: "verified"}
	row.Object = key
	want := s.SuggestedTarget
	for index := 1; index <= maxItems+1; index++ {
		if index > 1 {
			want = uniqueName(s.SuggestedTarget, index)
		}
		if len(filepath.Base(want)) > 255 {
			return keep("name_limit", "保留两份后的文件名过长，请先调整名称")
		}
		if !b.authority.allowed(want) {
			continue // Renaming never bypasses an excluded file or directory.
		}
		nameKey := pathKey(filepath.Base(want))
		if nameKey == "" {
			return Row{}, ErrScope
		}
		if !missing && b.excludedSlots[td.Object+"/"+nameKey] {
			continue
		}
		if !missing {
			info, err := target.Lstat(filepath.Base(want))
			if err == nil {
				if os.SameFile(before, info) {
					return keep("already_placed", "文件已在建议目录中")
				}
				continue // Existing files, folders and links are never replaced.
			}
			if !errors.Is(err, os.ErrNotExist) {
				return keep("target_unavailable", "目标无法确认是否为空，暂时保留原处")
			}
		}
		reservation := pathKey(want)
		if reservation == "" {
			return Row{}, ErrScope
		}
		if b.reserved[reservation] {
			continue
		}
		b.reserved[reservation] = true
		row.Target, row.Action, row.Code, row.Reason = want, "move", "proposed", "按建议归入项目目录，确认后仍须执行前核验"
		if crossesDevice(before, td) {
			row.Action, row.Reason = "copy", "跨磁盘复制到项目目录，原件保留"
		}
		row.Renamed = want != s.SuggestedTarget
		if row.Renamed {
			row.Reason += "；同名内容保留两份"
		}
		return row, nil
	}
	return keep("name_limit", "同名项过多，暂时保留原处")
}

func crossesDevice(file os.FileInfo, target Directory) bool {
	_, device, _ := object(file)
	return device != target.Device
}
