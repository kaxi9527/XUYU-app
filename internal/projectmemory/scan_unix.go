//go:build go1.24 && unix

package projectmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const maxFileHashBytes int64 = 32 << 20

type scanner struct {
	ctx     context.Context
	options ScanOptions
	out     Observation
	seen    map[string]bool
	hashed  int64
}

// Scan reads metadata and bounded hashes; it never writes, follows links, or
// reads tool history. Open directory handles anchor all descendant operations.
func Scan(ctx context.Context, scope Scope, options ScanOptions) (Observation, error) {
	if err := scope.Validate(); err != nil {
		return Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	for _, path := range options.Exclude {
		if !pathOK(path) {
			return Observation{}, errors.New("排除位置无效")
		}
	}
	if options.MaxFiles <= 0 || options.MaxFiles > maxObservations {
		options.MaxFiles = maxObservations
	}
	if options.MaxHashBytes <= 0 || options.MaxHashBytes > 256<<20 {
		options.MaxHashBytes = 256 << 20
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s := scanner{ctx: bounded, options: options, seen: map[string]bool{}, out: Observation{ScopeSHA: scope.Digest(), StartedAt: stamp(time.Now()), Complete: true, Files: []Fact{}}}
	for _, path := range scope.Roots {
		s.entry(path, true)
	}
	for _, path := range scope.Files {
		s.entry(path, false)
	}
	// Explicit caller cancellation aborts the commit. Our own time budget is a
	// recorded gap and retains all previously known, unobserved files.
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	s.out.EndedAt = stamp(time.Now())
	sort.Slice(s.out.Files, func(i, j int) bool { return s.out.Files[i].Path < s.out.Files[j].Path })
	return s.out, nil
}

func (s *scanner) gap(path, status, reason string) {
	s.out.Complete = false
	s.out.Coverage = append(s.out.Coverage, Coverage{Path: path, Status: status, Reason: reason})
}
func (s *scanner) excluded(path string) bool {
	for _, root := range s.options.Exclude {
		if within(platformPath(root), platformPath(path)) {
			return true
		}
	}
	return false
}
func (s *scanner) limit() bool { return s.ctx.Err() != nil || len(s.out.Files) >= s.options.MaxFiles }

// The only implicit alias is macOS's system /var and /tmp mapping. Arbitrary
// user symlinks, including ancestors above the selected scope, are rejected.
func openDirectory(path string) (*os.Root, error) {
	path = platformPath(path)
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if name == "" {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			root.Close()
			return nil, errors.New("目录不可用或包含链接")
		}
		next, err := childDirectory(root, name, info)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func childDirectory(parent *os.Root, name string, before os.FileInfo) (*os.Root, error) {
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := r.Stat(".")
	current, check := parent.Lstat(name)
	if err != nil || check != nil || !current.IsDir() || !os.SameFile(before, after) || !os.SameFile(before, current) {
		r.Close()
		return nil, errors.New("观察时目录位置已变化")
	}
	return r, nil
}

func (s *scanner) entry(path string, directory bool) {
	start := len(s.out.Coverage)
	if s.limit() {
		s.gap(path, "limited", "达到本轮观察时间或文件数量限额")
		return
	}
	if s.excluded(path) {
		s.gap(path, "excluded", "WorkShelf 自身记忆输出不纳入观察")
		return
	}
	parent, err := openDirectory(filepath.Dir(path))
	if err != nil {
		s.gap(path, "offline", "登记位置或上级目录不可访问；未推断删除")
		return
	}
	defer parent.Close()
	name := filepath.Base(path)
	info, err := parent.Lstat(name)
	if err != nil {
		s.gap(path, "offline", "登记位置当前不可访问；未推断删除")
		return
	}
	if directory && !info.IsDir() || !directory && info.IsDir() {
		s.gap(path, "error", "登记的位置类型已改变；请重新核对范围")
		return
	}
	s.visit(parent, name, path, info, 0)
	currentParent, err := openDirectory(filepath.Dir(path))
	if err != nil {
		s.gap(path, "error", "登记位置的上级目录在观察时变化")
	} else {
		current, checkErr := currentParent.Lstat(name)
		currentParent.Close()
		if checkErr != nil || !os.SameFile(info, current) {
			s.gap(path, "error", "登记位置在观察时变化")
		}
	}
	status, reason := "complete", "已枚举本次范围；内容核验状态逐项记录"
	if len(s.out.Coverage) > start {
		status, reason = "limited", "本范围存在未观察部分，保留其最近已知状态"
	}
	s.out.Coverage = append(s.out.Coverage, Coverage{Path: path, Status: status, Reason: reason})
}

func (s *scanner) visit(parent *os.Root, name, path string, info os.FileInfo, depth int) {
	if s.seen[path] {
		return
	}
	if s.excluded(path) {
		s.gap(path, "excluded", "WorkShelf 自身记忆输出不纳入观察")
		return
	}
	if s.limit() {
		s.gap(path, "limited", "达到本轮观察时间或文件数量限额")
		return
	}
	s.seen[path] = true
	kind := "other"
	switch {
	case info.Mode().IsRegular():
		kind = "file"
	case info.IsDir():
		kind = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		kind = "link"
	}
	f := Fact{Path: path, Kind: kind, Size: info.Size(), ModifiedAt: stamp(info.ModTime()), ObjectKey: objectKey(info), HashState: "metadata_only"}
	if f.Size < 0 {
		f.Size = 0
	}
	protected := metadataOnlyPath(path)
	if kind == "file" && !protected && info.Size() <= maxFileHashBytes && info.Size() <= s.options.MaxHashBytes-s.hashed {
		hash, used, err := s.hash(parent, name, info)
		s.hashed += used
		if err != nil {
			f.HashState = "unavailable"
			s.gap(path, "error", "内容读取失败或文件在观察时变化，未据此断言内容变化")
		} else {
			f.SHA256, f.HashState = hash, "verified"
		}
	}
	s.out.Files = append(s.out.Files, f)
	if kind != "directory" {
		return
	}
	if protected {
		s.gap(path, "excluded", "隐藏、配置或依赖目录只记录目录信息，不读取内部内容")
		return
	}
	if depth >= 64 {
		s.gap(path, "limited", "达到本轮目录深度限额")
		return
	}
	r, err := childDirectory(parent, name, info)
	if err != nil {
		s.gap(path, "error", "目录在观察时变化或无法打开")
		return
	}
	defer r.Close()
	dir, err := r.Open(".")
	if err != nil {
		s.gap(path, "error", "无法枚举目录")
		return
	}
	defer dir.Close()
	for {
		if s.limit() {
			s.gap(path, "limited", "达到本轮观察时间或文件数量限额")
			break
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if s.limit() {
				s.gap(path, "limited", "达到本轮观察时间或文件数量限额")
				break
			}
			child := filepath.Join(path, entry.Name())
			ci, err := r.Lstat(entry.Name())
			if err != nil {
				s.gap(child, "error", "条目在观察时变化或不可读取")
				continue
			}
			s.visit(r, entry.Name(), child, ci, depth+1)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				s.gap(path, "error", "目录枚举未完成")
			}
			break
		}
	}
	end, err := parent.Lstat(name)
	if err != nil || !os.SameFile(info, end) || !info.ModTime().Equal(end.ModTime()) {
		s.gap(path, "error", "目录在观察过程中变化；保留未观察条目的最近状态")
	}
}

func (s *scanner) hash(parent *os.Root, name string, before os.FileInfo) (string, int64, error) {
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) {
		return "", 0, errors.New("文件已变化")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := s.ctx.Err(); err != nil {
			return "", total, err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > before.Size() || total > s.options.MaxHashBytes-s.hashed {
			return "", total, ErrBudget
		}
		h.Write(buf[:n])
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", total, err
			}
			break
		}
	}
	after, err := f.Stat()
	current, check := parent.Lstat(name)
	if err != nil || check != nil || !os.SameFile(before, current) || !os.SameFile(before, after) || total != before.Size() || after.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) {
		return "", total, errors.New("文件在内容核验时变化")
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}
