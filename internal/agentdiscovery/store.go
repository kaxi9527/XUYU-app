package agentdiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxReceiptBytes = 4 << 20

var ErrCorrupt = errors.New("Agent 检查记录损坏或格式不支持；原记录未修改")
var ErrClock = errors.New("系统时间早于上次检查，请核实时间；原记录未修改")

type Change struct {
	ID   string `json:"id"`
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}

// Each immutable receipt is both a checkpoint and an audit record. The most
// recent file is authoritative; no separately rewritten index can drift from it.
type Receipt struct {
	Schema      int               `json:"schema"`
	Revision    uint64            `json:"revision"`
	PreviousSHA string            `json:"previousSHA,omitempty"`
	NextDue     time.Time         `json:"nextDue"`
	Scan        Scan              `json:"scan"`
	Records     []Candidate       `json:"records"`
	Changes     []Change          `json:"changes"`
	Event       string            `json:"event,omitempty"`
	RecordedAt  time.Time         `json:"recordedAt,omitempty"`
	Policy      *EnrollmentPolicy `json:"policy,omitempty"`
	Enrollments []EnrollmentEvent `json:"enrollments,omitempty"`
}

type Result struct {
	Ran     bool    `json:"ran"`
	Receipt Receipt `json:"receipt"`
}

type Store struct{ directory string }

func NewStore(directory string) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("检查记录目录需要完整路径")
	}
	clean := filepath.Clean(directory)
	ancestor := clean
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return nil, err
			}
			// Resolve an existing parent (macOS /var), never an explicit store link.
			if ancestor == clean && resolved != clean {
				return nil, errors.New("检查记录目录不能是链接")
			}
			rel, err := filepath.Rel(ancestor, clean)
			if err != nil {
				return nil, err
			}
			return &Store{directory: filepath.Join(resolved, rel)}, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil, errors.New("无法定位检查记录目录")
		}
		ancestor = parent
	}
}

func (s *Store) ready(create bool) (bool, error) {
	var dirs []string
	for p := s.directory; ; p = filepath.Dir(p) {
		dirs = append(dirs, p)
		if p == filepath.Dir(p) {
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
			if err = os.Mkdir(p, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return false, err
			}
			fi, err = os.Lstat(p)
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("检查记录路径含链接或不是目录")
		}
	}
	return true, nil
}

func receiptName(revision uint64) string { return fmt.Sprintf("%020d.json", revision) }

func (s *Store) latest(ctx context.Context) (Receipt, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, nil, err
	}
	ready, err := s.ready(false)
	if err != nil || !ready {
		return Receipt{}, nil, err
	}
	f, err := os.Open(s.directory)
	if err != nil {
		return Receipt{}, nil, err
	}
	entries, err := f.ReadDir(100001)
	f.Close()
	if err != nil && err != io.EOF {
		return Receipt{}, nil, err
	}
	if len(entries) > 100000 {
		return Receipt{}, nil, errors.New("检查历史超过当前读取预算；历史已保留")
	}
	var latest uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Receipt{}, nil, err
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil || n == 0 || receiptName(n) != name {
			return Receipt{}, nil, ErrCorrupt
		}
		if n > latest {
			latest = n
		}
	}
	if latest == 0 {
		return Receipt{}, nil, nil
	}
	r, b, err := s.readReceipt(latest)
	if err != nil {
		return Receipt{}, nil, err
	}
	if latest > 1 {
		_, previous, err := s.readReceipt(latest - 1)
		if err != nil || hash(previous) != r.PreviousSHA {
			return Receipt{}, nil, ErrCorrupt
		}
	} else if r.PreviousSHA != "" {
		return Receipt{}, nil, ErrCorrupt
	}
	return r, b, nil
}

func (s *Store) readReceipt(revision uint64) (Receipt, []byte, error) {
	p := filepath.Join(s.directory, receiptName(revision))
	fi, err := noLinkStat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxReceiptBytes {
		return Receipt{}, nil, ErrCorrupt
	}
	f, err := os.Open(p)
	if err != nil {
		return Receipt{}, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !os.SameFile(fi, st) {
		return Receipt{}, nil, ErrCorrupt
	}
	b, err := io.ReadAll(io.LimitReader(f, maxReceiptBytes+1))
	if err != nil || len(b) > maxReceiptBytes {
		return Receipt{}, nil, ErrCorrupt
	}
	var r Receipt
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Schema != 1 || r.Revision != revision || !filepath.IsAbs(r.Scan.Home) || r.Scan.StartedAt.IsZero() || !r.NextDue.Equal(r.Scan.StartedAt.Add(24*time.Hour)) || len(r.Records) > 4096 {
		return Receipt{}, nil, ErrCorrupt
	}
	seen := map[string]bool{}
	for _, c := range r.Records {
		if c.ID == "" || seen[c.ID] || c.FirstSeen.IsZero() || c.LastSeen.Before(c.FirstSeen) {
			return Receipt{}, nil, ErrCorrupt
		}
		seen[c.ID] = true
	}
	if err := validateEnrollmentReceipt(r); err != nil {
		return Receipt{}, nil, err
	}
	return r, b, nil
}

func (s *Store) Read(ctx context.Context) (Receipt, error) {
	r, _, err := s.latest(ctx)
	return r, err
}

// Check holds a cross-process lock through the due check and observation. It
// never writes to scanned clients. Force is a user-requested refresh, not a retry.
func (s *Store) Check(ctx context.Context, scanner Scanner, now time.Time, force bool) (Result, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return s.checkLocked(ctx, scanner, now, force)
}

// Automatic checks require a current persisted grant. Read once without
// allocating a store for the default-off case, then check again under the same
// cross-process lock used by SetEnrollmentPolicy before starting the scan.
func (s *Store) CheckAutomatic(ctx context.Context, scanner Scanner, now time.Time) (Result, error) {
	r, err := s.Read(ctx)
	if err != nil || r.Policy == nil || !r.Policy.Enabled {
		return Result{Receipt: r}, err
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return s.checkAutomaticLocked(ctx, scanner, now)
}

func (s *Store) checkAutomaticLocked(ctx context.Context, scanner Scanner, now time.Time) (Result, error) {
	r, _, err := s.latest(ctx)
	if err != nil || r.Policy == nil || !r.Policy.Enabled {
		return Result{Receipt: r}, err
	}
	return s.checkLocked(ctx, scanner, now, false)
}

func (s *Store) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !writesSupported() {
		return nil, errors.New("此平台尚未支持检查记录文件锁")
	}
	if _, err := s.ready(true); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(s.directory, "check.lock")
	if fi, err := noLinkStat(lockPath); err == nil && !fi.Mode().IsRegular() {
		return nil, errors.New("检查锁不是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	st, statErr := f.Stat()
	pathStat, pathErr := noLinkStat(lockPath)
	if statErr != nil || pathErr != nil || !os.SameFile(st, pathStat) {
		f.Close()
		return nil, errors.New("检查锁已变化")
	}
	if err := lockContext(ctx, f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlock(f); f.Close() }, nil
}

func (s *Store) checkLocked(ctx context.Context, scanner Scanner, now time.Time, force bool) (Result, error) {
	previous, b, err := s.latest(ctx)
	if err != nil {
		return Result{}, err
	}
	home, err := filepath.EvalSymlinks(scanner.Home)
	if err != nil {
		return Result{}, err
	}
	if previous.Revision > 0 && previous.Scan.Home != home {
		return Result{}, errors.New("此检查记录属于另一个主目录")
	}
	if now.IsZero() || now.Before(previous.Scan.StartedAt) {
		return Result{}, ErrClock
	}
	if previous.Revision > 0 && !force && now.Before(previous.NextDue) {
		return Result{Receipt: previous}, nil
	}
	scan, err := scanner.Run(ctx, now)
	if err != nil {
		return Result{}, err
	}
	r := Receipt{Schema: 1, Revision: previous.Revision + 1, NextDue: now.UTC().Add(24 * time.Hour), Scan: scan, Changes: []Change{}, Event: "scan", RecordedAt: now.UTC(), Policy: previous.Policy, Enrollments: previous.Enrollments}
	if len(b) > 0 {
		r.PreviousSHA = hash(b)
	}
	old := map[string]Candidate{}
	for _, c := range previous.Records {
		old[c.ID] = c
	}
	for _, c := range scan.Candidates {
		before := old[c.ID]
		if before.ID != "" {
			c.FirstSeen = before.FirstSeen
		}
		if before.Status != c.Status {
			r.Changes = append(r.Changes, Change{c.ID, before.Status, c.Status})
		}
		r.Records = append(r.Records, c)
		delete(old, c.ID)
	}
	for _, c := range old {
		next := "not_observed"
		if len(scan.Gaps) > 0 {
			next = "unknown"
		}
		if c.Status != next {
			r.Changes = append(r.Changes, Change{c.ID, c.Status, next})
		}
		c.Status = next
		r.Records = append(r.Records, c)
	}
	if len(r.Records) > 4096 {
		return Result{}, errors.New("Agent 登记候选超过当前预算；历史已保留")
	}
	sort.Slice(r.Records, func(i, j int) bool { return r.Records[i].ID < r.Records[j].ID })
	sort.Slice(r.Changes, func(i, j int) bool { return r.Changes[i].ID < r.Changes[j].ID })
	if err := s.append(ctx, r); err != nil {
		return Result{}, err
	}
	return Result{Ran: true, Receipt: r}, nil
}

func (s *Store) append(ctx context.Context, r Receipt) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > maxReceiptBytes {
		return errors.New("本次检查记录超过保存预算；未覆盖历史")
	}
	target := filepath.Join(s.directory, receiptName(r.Revision))
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("检查回执序号冲突")
	}
	f, err := os.CreateTemp(s.directory, ".check-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.ready(false); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), target); err != nil {
		return err
	}
	d, err := os.Open(s.directory)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Watch is foreground and cancellable. Login/startup installation belongs to
// the desktop lifecycle; calling this does not register a system startup item.
func (s *Store) Watch(ctx context.Context, scanner Scanner, report func(Result) error) error {
	for {
		result, err := s.Check(ctx, scanner, time.Now(), false)
		if err != nil {
			return err
		}
		beforeRevision := result.Receipt.Revision
		if result.Ran {
			result.Receipt, err = s.ReconcileEnrollment(ctx)
		} else {
			result.Receipt, err = s.RecoverEnrollment(ctx)
		}
		if err != nil {
			return err
		}
		if (result.Ran || result.Receipt.Revision != beforeRevision) && report != nil {
			if err := report(result); err != nil {
				return err
			}
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
