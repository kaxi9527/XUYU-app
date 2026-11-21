package main

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/desktop/diskgrowth"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type growthWorker struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	busy      bool
	lastError string
}
type GrowthView struct {
	Prefs             diskgrowth.Preferences `json:"prefs"`
	Records           []diskgrowth.Record    `json:"records"`
	Current, Previous *diskgrowth.Entry
	Free              uint64 `json:"free"`
	Root              string `json:"root"`
	Running           bool   `json:"running"`
	Error             string `json:"error,omitempty"`
}

func growthHistory() (diskgrowth.History, error) {
	h, err := os.UserHomeDir()
	return diskgrowth.History{Dir: filepath.Join(filepath.Dir(config.DefaultPath(h)), "disk-growth")}, err
}
func (s *ShelfService) DiskGrowth(id string) (GrowthView, error) {
	w := &s.growth
	w.mu.Lock()
	defer w.mu.Unlock()
	var v GrowthView
	h, e := growthHistory()
	if e != nil {
		return v, e
	}
	v.Prefs, e = h.Preferences()
	if e != nil {
		return v, e
	}
	v.Records, e = h.Records(time.Now(), v.Prefs.Days)
	if e != nil {
		return v, e
	}
	v.Running = w.busy
	v.Error = w.lastError
	records := v.Records
	if len(records) > 0 {
		first, e := h.Read(records[0])
		if e != nil {
			return v, e
		}
		if first.Error == interrupted {
			unlock, err := h.TryLock()
			if err != nil {
				return v, err
			}
			if unlock == nil {
				v.Running = true
				records = records[1:]
				v.Records = records
			} else {
				unlock()
			}
		}
	}
	v.Current, v.Previous, e = h.Pair(records, id)
	if e != nil {
		return v, e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return v, e
	}
	v.Root = diskgrowth.Volume(home)
	v.Free, _, e = diskgrowth.Capacity(v.Root)
	return v, e
}
func (s *ShelfService) SetDiskGrowth(p diskgrowth.Preferences) error {
	w := &s.growth
	w.mu.Lock()
	defer w.mu.Unlock()
	h, e := growthHistory()
	if e != nil {
		return e
	}
	old, e := h.Preferences()
	if e != nil {
		return e
	}
	if e = h.Save(p); e != nil {
		return e
	}
	if e = growthTimer(p); e != nil {
		_ = h.Save(old)
		if rollback := growthTimer(old); rollback != nil {
			return errors.Join(e, rollback)
		}
		return e
	}
	_, e = h.Records(time.Now(), p.Days)
	return e
}
func (s *ShelfService) SampleDiskGrowth() error { return s.beginGrowth(true, time.Now()) }

type GrowthHeadline struct {
	Free     uint64 `json:"free"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

func (s *ShelfService) DiskGrowthHeadline() (GrowthHeadline, error) {
	v, e := s.DiskGrowth("")
	h := GrowthHeadline{Free: v.Free}
	if v.Current != nil && v.Current.Error == "" {
		h.Current = v.Current.Finished.Format(time.RFC3339)
	}
	if v.Previous != nil {
		h.Previous = v.Previous.Finished.Format(time.RFC3339)
	}
	return h, e
}

const interrupted = "采样中断，未形成可比较的数据"

func (s *ShelfService) runGrowthCommand(arg string) error {
	if arg == "--enable-disk-growth" {
		h, e := growthHistory()
		if e != nil {
			return e
		}
		p, e := h.Preferences()
		if e != nil {
			return e
		}
		p.Enabled = true
		return s.SetDiskGrowth(p)
	}
	w := &s.growth
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()
	if err := s.beginGrowth(arg == "--disk-growth-sample", time.Now()); err != nil {
		return err
	}
	w.wg.Wait()
	if w.lastError != "" {
		return errors.New(w.lastError)
	}
	return nil
}

func (s *ShelfService) beginGrowth(manual bool, now time.Time) error {
	w := &s.growth
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx == nil || w.ctx.Err() != nil {
		return errors.New("采样服务尚未就绪")
	}
	if w.busy {
		return nil
	}
	h, e := growthHistory()
	if e != nil {
		return e
	}
	p, e := h.Preferences()
	if e != nil {
		return e
	}
	records, e := h.Records(now, p.Days)
	if e != nil {
		return e
	}
	trigger, planned := "manual", ""
	if !manual {
		trigger, planned = diskgrowth.Due(now, p, records)
		if trigger == "" {
			return nil
		}
	}
	entry := diskgrowth.Entry{Sample: diskgrowth.Sample{Started: now, Trigger: trigger, Planned: planned}, Error: interrupted}
	unlock, e := h.TryLock()
	if e != nil {
		return e
	}
	if unlock == nil {
		return nil
	}
	if !manual {
		records, e = h.Records(now, p.Days)
		if e != nil {
			unlock()
			return e
		}
		if due, _ := diskgrowth.Due(now, p, records); due == "" {
			unlock()
			return nil
		}
	}
	if e = h.Write(entry); e != nil {
		unlock()
		return e
	}
	w.busy = true
	w.lastError = ""
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer unlock()
		jobErr := s.collectGrowth(w.ctx, h, &entry)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.busy = false
		if jobErr != nil {
			entry.Error = jobErr.Error()
			w.lastError = entry.Error
			entry.Finished = time.Now()
		} else {
			entry.Error = ""
		}
		if err := h.Write(entry); err != nil {
			w.lastError = err.Error()
		}
	}()
	return nil
}
func (s *ShelfService) collectGrowth(ctx context.Context, h diskgrowth.History, entry *diskgrowth.Entry) error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	root := diskgrowth.Volume(home)
	physical := func(p string) string {
		if runtime.GOOS == "darwin" && root == "/System/Volumes/Data" && filepath.IsAbs(p) && !stringsWithin(p, root) {
			return filepath.Join(root, p)
		}
		return p
	}
	scope := diskgrowth.Scope{Root: root, Home: physical(home), HistoryDir: physical(h.Dir), Projects: map[string]string{}}
	cfg, _, e := loadConfig()
	if e != nil {
		return e
	}
	reg, e := registry.Load(cfg)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if reg != nil {
		for _, p := range reg.Projects {
			for _, path := range p.Dirs() {
				scope.Projects[physical(path)] = p.Name
			}
		}
	}
	sample, e := diskgrowth.Scan(ctx, scope, entry.Started, entry.Trigger, entry.Planned)
	entry.Sample = sample
	return e
}
func stringsWithin(path, root string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && (rel == "." || filepath.IsLocal(rel))
}
func (s *ShelfService) startGrowth() {
	w := &s.growth
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := s.beginGrowth(false, time.Now()); err != nil {
				w.mu.Lock()
				w.lastError = err.Error()
				w.mu.Unlock()
			}
			select {
			case <-w.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *ShelfService) closeGrowth() {
	w := &s.growth
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	w.wg.Wait()
}
