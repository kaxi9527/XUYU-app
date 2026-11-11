package main

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/organize"
)

type organizeRun struct {
	cancel    context.CancelFunc
	config    config.Config
	ids       []string
	started   time.Time
	done      chan struct{}
	completed bool
	summary   *home.Summary
	err       error
}

func normalizedSelection(ids []string) []string {
	copyIDs := append([]string{}, ids...)
	sort.Strings(copyIDs)
	unique := copyIDs[:0]
	for _, id := range copyIDs {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique
}

// apply binds one selection to one preview. Retries return its original receipt,
// including failures; changing the selection requires a new preview.
func (store *organizePlans) apply(cfg config.Config, token string, ids []string, execute func(context.Context, *organize.Plan, []string) (*home.Summary, error)) (*home.Summary, error) {
	selected := normalizedSelection(ids)
	store.mu.Lock()
	if run := store.runs[token]; run != nil {
		if !reflect.DeepEqual(run.config, cfg) || !reflect.DeepEqual(run.ids, selected) {
			store.mu.Unlock()
			return nil, errors.New("整理范围或配置已变化，请重新预览；旧请求不会再次执行")
		}
		done := run.done
		store.mu.Unlock()
		<-done
		return run.summary, run.err
	}
	plan := store.plans[token]
	original, known := store.configs[token]
	if plan == nil || !known {
		store.mu.Unlock()
		return nil, errors.New("预览已失效或应用已重启，请重新预览")
	}
	if !reflect.DeepEqual(original, cfg) {
		store.mu.Unlock()
		return nil, errors.New("整理配置已变化，请重新预览")
	}
	if store.runs == nil {
		store.runs = map[string]*organizeRun{}
	}
	for key, run := range store.runs {
		if run.completed && time.Since(run.started) > 30*time.Minute {
			delete(store.runs, key)
		}
	}
	// Receipts are bounded in memory; durable recovery logs remain on disk.
	if len(store.runs) >= 32 {
		oldest := ""
		for key, run := range store.runs {
			if run.completed && (oldest == "" || run.started.Before(store.runs[oldest].started)) {
				oldest = key
			}
		}
		if oldest == "" {
			store.mu.Unlock()
			return nil, errors.New("已有多个整理任务进行中，请稍后重试")
		}
		delete(store.runs, oldest)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &organizeRun{config: cfg, ids: selected, started: time.Now(), done: make(chan struct{}), cancel: cancel}
	store.runs[token] = run
	delete(store.plans, token)
	delete(store.configs, token)
	store.mu.Unlock()
	var summary *home.Summary
	err := errors.New("整理意外中断，请检查操作记录；可能已有部分文件完成，旧请求不会重复执行")
	defer func() {
		cancel()
		store.mu.Lock()
		run.summary, run.err, run.completed = summary, err, true
		close(run.done)
		store.mu.Unlock()
	}()
	summary, err = execute(ctx, plan, selected)
	return summary, err
}

func (store *organizePlans) stop(token string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	run := store.runs[token]
	if run == nil || run.completed {
		return false
	}
	run.cancel()
	return true
}
