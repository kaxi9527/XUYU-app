package organize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

type mergeSlot struct {
	source, target, hash string
	row                  int
	existing             bool
	directory            bool
}
type mergeDirectory struct {
	path     string
	count    int
	existing bool
}

type mergeGuardState struct {
	modified  time.Time
	protected bool
}

// The preview is not permission to split a directory that became an engineering
// project while the user was reviewing it. The exact executor calls this again.
func validateMergeParents(m home.ExactMove, targetRoot string, stages map[string]bool, cache map[string]mergeGuardState) error {
	for _, boundary := range []struct {
		path, root string
		target     bool
	}{{filepath.Dir(m.Source), m.SourceRoot, false}, {filepath.Dir(m.Target), targetRoot, true}} {
		for path := boundary.path; in(path, boundary.root); path = filepath.Dir(path) {
			if path == boundary.root && !boundary.target {
				break
			}
			if boundary.target && path == boundary.root && stages[path] {
				break
			}
			fi, err := os.Stat(path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				key := fmt.Sprintf("%t:%s", boundary.target, path)
				state, ok := cache[key]
				if !ok || !state.modified.Equal(fi.ModTime()) {
					guarded := protected(path) != "" || bundleKind(path) != ""
					if boundary.target && !stages[path] {
						guarded = guarded || mergeBundleKind(path) != ""
					}
					state = mergeGuardState{modified: fi.ModTime(), protected: guarded}
					cache[key] = state
				}
				if state.protected {
					return fmt.Errorf("来源或目标在预览后成为完整工程，需重新预览：%s", path)
				}
			}
			if path == boundary.root {
				break
			}
		}
	}
	return nil
}

type mergePlanner struct {
	ctx             context.Context
	cfg             config.Config
	plan            *Plan
	slots           map[string][]*mergeSlot
	dirs            map[string]*mergeDirectory
	reserved        map[string]bool
	hashes          map[string]string
	bytes           int64
	entries         int
	deadline        time.Time
	caseInsensitive bool
	ancestorCache   map[string]bool
	sourceOrder     []string
}

func (p *Plan) resolveMerge(ctx context.Context, cfg config.Config, order []string) error {
	oldRows, oldMoves := p.Rows, p.moves
	p.Rows = nil
	p.moves = map[string]home.ExactMove{}
	p.duplicates = map[string]home.ExactMove{}
	p.Merge = true
	rank := func(path string) int {
		for i, root := range order {
			if in(path, root) {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(oldRows, func(i, j int) bool {
		if oldRows[i].Selected != oldRows[j].Selected {
			return oldRows[i].Selected
		}
		return rank(oldRows[i].Source) < rank(oldRows[j].Source)
	})
	b := &mergePlanner{ctx: ctx, cfg: cfg, plan: p, slots: map[string][]*mergeSlot{}, dirs: map[string]*mergeDirectory{}, reserved: map[string]bool{}, hashes: map[string]string{}, bytes: 1 << 30, deadline: time.Now().Add(15 * time.Second)}
	ancestor := cfg.Root
	for {
		if _, err := os.Stat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		ancestor = parent
	}
	b.caseInsensitive = filesystemCaseInsensitive(ancestor)
	b.ancestorCache = map[string]bool{}
	b.sourceOrder = order
	for _, row := range oldRows {
		if err := ctx.Err(); err != nil {
			return err
		}
		move, ok := oldMoves[row.ID]
		if !ok || row.Blocked || row.Existing {
			found := false
			for _, known := range p.Rows {
				if known.Source == row.Source {
					found = true
					break
				}
			}
			if !found {
				p.Rows = append(p.Rows, row)
			}
			continue
		}
		b.visit(row, move)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.groupNamedVersions()
	return ctx.Err()
}

func (b *mergePlanner) key(path string) string {
	key := normalizeFilename(filepath.Clean(path))
	if b.caseInsensitive {
		key = strings.ToLower(key)
	}
	return key
}
func mergeID(path string) string { h := sha256.Sum256([]byte(path)); return hex.EncodeToString(h[:12]) }
func (b *mergePlanner) limit() error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if b.entries >= 5000 || time.Now().After(b.deadline) {
		b.incomplete()
		return errors.New("合并比较达到条目或时间上限，未检查的内容保留原处")
	}
	return nil
}

func (b *mergePlanner) incomplete() {
	const warning = "部分内容尚未完成比较，已保留原处；可缩小来源范围重新预览。"
	if b.plan != nil && !contains(b.plan.Warnings, warning) {
		b.plan.Warnings = append(b.plan.Warnings, warning)
	}
}
func (b *mergePlanner) keep(row Row, reason string) {
	row.ID = mergeID(row.Source)
	row.Blocked = true
	row.Selected = false
	row.KeepOriginal = true
	row.Operation = "kept"
	row.Reason = reason
	row.Target = ""
	b.plan.Rows = append(b.plan.Rows, row)
}
func (b *mergePlanner) visit(row Row, move home.ExactMove) {
	if err := b.limit(); err != nil {
		b.keep(row, err.Error())
		return
	}
	b.entries++
	if err := home.CheckPath(move.SourceRoot, move.Source); err != nil {
		b.keep(row, err.Error())
		return
	}
	if err := home.CheckPath(b.cfg.Root, move.Target); err != nil {
		b.keep(row, err.Error())
		return
	}
	for parent := filepath.Dir(move.Source); in(parent, move.SourceRoot) && parent != move.SourceRoot; parent = filepath.Dir(parent) {
		if b.protectedAncestor(parent, false) {
			b.keep(row, "来源属于完整工程，保留在工程内，不单独拆出文件")
			return
		}
	}
	for parent := filepath.Dir(move.Target); in(parent, b.plan.Target); parent = filepath.Dir(parent) {
		if b.protectedAncestor(parent, true) {
			b.keep(row, "目标属于完整工程，不把外部文件合入工程内部")
			return
		}
		if parent == b.plan.Target {
			break
		}
	}
	fi, err := os.Lstat(move.Source)
	if err != nil {
		b.keep(row, "来源无法读取，保留原处："+err.Error())
		return
	}
	name := filepath.Base(move.Source)
	if strings.HasPrefix(name, ".") || scan.IsSensitiveName(name) || fi.Mode()&os.ModeSymlink != 0 || (!fi.IsDir() && !fi.Mode().IsRegular()) {
		b.keep(row, "隐藏配置、凭据、链接或特殊文件保留原处")
		return
	}
	row.Directory = fi.IsDir()
	if fi.IsDir() {
		if reason := protected(move.Source); reason != "" {
			b.keep(row, reason)
			return
		}
		if mergeBundleKind(move.Source) == "" {
			b.directory(row, move)
			return
		}
	}
	b.file(row, move)
}

func (b *mergePlanner) protectedAncestor(path string, target bool) bool {
	key := fmt.Sprintf("%t:%s", target, path)
	if value, ok := b.ancestorCache[key]; ok {
		return value
	}
	value := protected(path) != "" || bundleKind(path) != ""
	if target {
		value = value || mergeBundleKind(path) != ""
	}
	b.ancestorCache[key] = value
	return value
}

func (b *mergePlanner) directory(row Row, move home.ExactMove) {
	want := move.Target
	key := b.key(want)
	if protected(want) != "" || mergeBundleKind(want) != "" {
		b.keep(row, "目标是完整工程，不能把外部目录合入工程内部")
		return
	}
	fi, err := os.Lstat(want)
	existing := err == nil && fi.IsDir()
	if err != nil && !os.IsNotExist(err) {
		b.keep(row, "目标无法读取："+err.Error())
		return
	}
	if (err == nil && !fi.IsDir()) || (b.reserved[key] && b.dirs[key] == nil) {
		if known := b.dirs[key]; known != nil {
			want = known.path
		} else {
			originalKey := key
			want, err = b.unique(move.Source, want, true)
			if err != nil {
				b.keep(row, err.Error())
				return
			}
			key = b.key(want)
			existing = false
			b.dirs[originalKey] = &mergeDirectory{path: want}
			b.dirs[key] = b.dirs[originalKey]
			b.reserved[key] = true
		}
	}
	dir := b.dirs[key]
	if dir == nil {
		dir = &mergeDirectory{path: want, existing: existing}
		b.dirs[key] = dir
		b.reserved[key] = true
	}
	dir.count++
	if (dir.count == 2 && !dir.existing) || (dir.count == 1 && dir.existing) {
		b.plan.MergedFolders++
	}
	f, err := os.Open(move.Source)
	if err != nil {
		b.keep(row, "文件夹无法读取："+err.Error())
		return
	}
	entries, readErr := f.ReadDir(5001)
	f.Close()
	if readErr != nil && readErr != io.EOF {
		b.keep(row, "文件夹无法完整读取，保留原处")
		return
	}
	if len(entries) > 5000 {
		b.incomplete()
		b.keep(row, "文件夹超过 5000 个直接条目，请缩小来源范围")
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) == 0 {
		b.keep(row, "空文件夹保留原处")
		return
	}
	for _, entry := range entries {
		child := row
		child.Source = filepath.Join(move.Source, entry.Name())
		child.ID = mergeID(child.Source)
		if entry.Name() == b.cfg.Names.Readme || entry.Name() == "AGENTS.md" || entry.Name() == "CLAUDE.md" {
			b.keep(child, "项目说明与工具配置保留原处")
			continue
		}
		next := home.ExactMove{Source: child.Source, SourceRoot: move.SourceRoot, Target: filepath.Join(dir.path, entry.Name())}
		b.visit(child, next)
	}
}

func (b *mergePlanner) file(row Row, move home.ExactMove) {
	want := move.Target
	key := b.key(want)
	if move.Source == want {
		row.Existing = true
		row.Target = want
		row.Selected = false
		row.Blocked = true
		b.plan.Rows = append(b.plan.Rows, row)
		return
	}
	if _, known := b.slots[key]; !known {
		b.slots[key] = nil
		if fi, err := os.Lstat(want); err == nil {
			b.slots[key] = append(b.slots[key], &mergeSlot{source: want, target: want, row: -1, existing: true, directory: fi.IsDir()})
			b.reserved[key] = true
		} else if !os.IsNotExist(err) {
			b.keep(row, "目标无法读取："+err.Error())
			return
		}
	}
	var hash string
	for _, other := range b.slots[key] {
		if row.Directory || other.directory {
			continue
		}
		a, err := os.Stat(move.Source)
		if err != nil {
			b.keep(row, err.Error())
			return
		}
		z, err := os.Stat(other.source)
		if err != nil {
			b.keep(row, "需要比较的文件已变化，请重新预览")
			return
		}
		if a.Size() != z.Size() {
			continue
		}
		if hash == "" {
			hash, err = b.hash(move.Source)
			if err != nil {
				b.keep(row, err.Error())
				return
			}
		}
		if other.hash == "" {
			other.hash, err = b.hash(other.source)
			if err != nil {
				b.keep(row, err.Error())
				return
			}
		}
		if !other.existing {
			m := b.plan.moves[b.plan.Rows[other.row].ID]
			m.ContentSHA256 = other.hash
			b.plan.moves[b.plan.Rows[other.row].ID] = m
		}
		if hash == other.hash {
			move.Target = other.target
			move.ContentSHA256 = hash
			move.Duplicate = true
			move.Snapshot, err = home.Snapshot(move.Source)
			if err != nil {
				b.keep(row, err.Error())
				return
			}
			move.CrossDevice = home.IsCrossDevice(move.Source, move.Target)
			row.ID = mergeID(move.Source)
			row.Target = move.Target
			row.Operation = "duplicate"
			row.Representative = other.source
			row.Blocked = true
			row.Selected = false
			row.KeepOriginal = true
			row.CrossDevice = false
			row.Folder = b.folder(move.Target)
			row.Reason = "完整内容一致，不重复搬入；这份来源保留原处"
			b.plan.Rows = append(b.plan.Rows, row)
			b.plan.duplicates[row.ID] = move
			return
		}
	}
	conflict := b.reserved[key] || len(b.slots[key]) > 0
	if conflict {
		var err error
		move.Target, err = b.unique(move.Source, want, row.Directory)
		if err != nil {
			b.keep(row, err.Error())
			return
		}
		row.Operation = "version"
		row.VersionGroup = mergeID(want)
		row.Reason += "；同名内容或类型不同，使用来源后缀保留两份"
		for _, other := range b.slots[key] {
			if other.row >= 0 {
				b.plan.Rows[other.row].VersionGroup = row.VersionGroup
			}
			if other.existing {
				found := false
				for i := range b.plan.Rows {
					if b.plan.Rows[i].Source == other.source {
						b.plan.Rows[i].VersionGroup = row.VersionGroup
						b.plan.Rows[i].Existing = true
						found = true
						break
					}
				}
				if !found {
					b.plan.Rows = append(b.plan.Rows, Row{ID: mergeID(other.source), Source: other.source, Target: other.target, Folder: b.folder(other.target), Blocked: true, Existing: true, Directory: other.directory, VersionGroup: row.VersionGroup, Reason: "已有版本保持原名和内容"})
				}
			}
		}
	} else {
		row.Operation = "add"
	}
	var err error
	move.Snapshot, err = home.Snapshot(move.Source)
	if err != nil {
		b.keep(row, err.Error())
		return
	}
	move.ContentSHA256 = hash
	move.CrossDevice = home.IsCrossDevice(move.Source, move.Target)
	row.ID = mergeID(move.Source)
	row.Target = move.Target
	row.Folder = b.folder(move.Target)
	row.CrossDevice = move.CrossDevice
	if move.CrossDevice {
		row.Reason += "；跨磁盘搬迁"
	}
	if row.Directory {
		row.Reason += "；完整工程保持内部结构"
	}
	i := len(b.plan.Rows)
	b.plan.Rows = append(b.plan.Rows, row)
	b.plan.moves[row.ID] = move
	b.slots[key] = append(b.slots[key], &mergeSlot{source: move.Source, target: move.Target, hash: hash, row: i, directory: row.Directory})
	b.reserved[b.key(move.Target)] = true
}

// Linked editable assets must stay with their project, not be renamed inside a
// merged folder. This is specific to merge planning; legacy layouts are retained.
func mergeBundleKind(dir string) string {
	if kind := bundleKind(dir); kind != "" {
		return kind
	}
	f, err := os.Open(dir)
	if err != nil {
		return ""
	}
	defer f.Close()
	names, _ := f.Readdirnames(5001)
	for _, name := range names {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".psd", ".psb", ".ai", ".indd", ".blend", ".c4d", ".max":
			return "code-bundle"
		}
	}
	return ""
}

func (b *mergePlanner) folder(target string) string {
	rel, _ := filepath.Rel(b.plan.Target, filepath.Dir(target))
	return filepath.ToSlash(rel)
}
func (b *mergePlanner) unique(source, want string, directory bool) (string, error) {
	label := filepath.Base(filepath.Dir(source))
	for _, root := range b.sourceOrder {
		if !in(source, root) {
			continue
		}
		label = filepath.Base(root)
		for _, other := range b.sourceOrder {
			if other != root && filepath.Base(other) == label {
				label = filepath.Base(filepath.Dir(root))
				break
			}
		}
		if root == source {
			label = filepath.Base(filepath.Dir(source))
		}
		break
	}
	runes := []rune(label)
	if len(runes) > 20 {
		label = string(runes[:20])
	}
	ext := ""
	if !directory {
		ext = filepath.Ext(want)
	}
	stem := strings.TrimSuffix(want, ext)
	for i := 1; i <= 9999; i++ {
		suffix := fmt.Sprintf("（来自%s）", label)
		if i > 1 {
			suffix = fmt.Sprintf("（来自%s-%d）", label, i)
		}
		candidate := stem + suffix + ext
		if b.reserved[b.key(candidate)] {
			continue
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("同名版本过多，请调整目标目录")
}

func (b *mergePlanner) hash(path string) (string, error) {
	if hash := b.hashes[path]; hash != "" {
		return hash, nil
	}
	if err := b.limit(); err != nil {
		return "", err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("只能比较普通文件")
	}
	if fi.Size() > b.bytes {
		b.incomplete()
		return "", errors.New("内容比较达到 1 GiB 读取上限，这组保留原处；可缩小来源重新比较")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(fi, opened) {
		return "", errors.New("文件在比较前已变化")
	}
	h := sha256.New()
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := b.limit(); err != nil {
			return "", err
		}
		n, e := f.Read(buffer)
		total += int64(n)
		b.bytes -= int64(n)
		if b.bytes < 0 || total > fi.Size() {
			return "", errors.New("文件在比较中增长，保留原处")
		}
		if n > 0 {
			h.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	after, err := f.Stat()
	if err != nil || total != fi.Size() || after.Size() != fi.Size() || !after.ModTime().Equal(fi.ModTime()) {
		return "", errors.New("文件在比较中已变化，请重新预览")
	}
	sum := hex.EncodeToString(h.Sum(nil))
	b.hashes[path] = sum
	return sum, nil
}

func (b *mergePlanner) groupNamedVersions() {
	groups := map[string][]int{}
	for i, row := range b.plan.Rows {
		if row.Directory || row.Blocked || row.Target == "" {
			continue
		}
		base := filepath.Base(row.Source)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		key := scan.StripVersion(stem)
		if key == "" {
			continue
		}
		key = b.key(filepath.Join(filepath.Dir(row.Target), key+filepath.Ext(base)))
		groups[key] = append(groups[key], i)
	}
	for key, indices := range groups {
		if len(indices) < 2 {
			continue
		}
		for _, i := range indices {
			if b.plan.Rows[i].VersionGroup == "" {
				b.plan.Rows[i].VersionGroup = mergeID(key)
				b.plan.Rows[i].Reason += "；名称像不同版本，原文件全部保留"
			}
		}
	}
}
