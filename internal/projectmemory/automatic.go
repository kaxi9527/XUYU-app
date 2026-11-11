package projectmemory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const automaticRecordLimit = 20000
const automaticByteLimit int64 = 128 << 20

var ErrAutomaticBudget = errors.New("自动检查的记录容量已达上限；已停止自动追加，历史记录保留")

// Automatic is a separate durable grant. Manual tracking never implies it.
// Zero values are omitted from State so pre-existing state digests stay valid.
func (s *Store) SetAutomatic(ctx context.Context, revision uint64, operationID, scopeSHA string, enabled bool, now time.Time) (Result, error) {
	if now.IsZero() {
		return Result{}, errors.New("记录时间不能为空")
	}
	intent := struct {
		ScopeSHA string
		Enabled  bool
	}{scopeSHA, enabled}
	return s.update(ctx, revision, operationID, "automatic", intent, func(previous State) ([]Event, error) {
		if previous.Revision == 0 || enabled && !previous.Enabled {
			return nil, errors.New("请先开启该项目的文件记录")
		}
		if enabled && previous.Scope.Digest() != scopeSHA {
			return nil, ErrConflict
		}
		reason := "用户关闭自动检查；历史记录保留，仍可手动检查"
		if enabled {
			reason = "用户授权应用运行时自动检查已确认的项目范围；退出期间不记录，再次运行补扫"
		}
		scope := previous.Scope
		return []Event{{Kind: "automatic_changed", ObservedAt: stamp(now), Source: "local_user", Actor: "local_user", Reason: reason, Scope: &scope, Automatic: &enabled}}, nil
	})
}

// The grant and revision are checked under the same cross-process lock as
// scanning and committing. Cancellation before publication leaves no event.
func (s *Store) ObserveAutomatic(ctx context.Context, revision uint64, operationID, scopeSHA string) (Result, error) {
	return s.update(ctx, revision, operationID, "automatic_scan", scopeSHA, func(previous State) ([]Event, error) {
		if !previous.Enabled || !previous.Automatic || previous.Revision == 0 {
			return nil, errors.New("该项目未授权自动检查或已经暂停")
		}
		if previous.Scope.Digest() != scopeSHA {
			return nil, ErrConflict
		}
		if err := s.automaticBudget(ctx, 0); err != nil {
			return nil, err
		}
		observation, err := Scan(ctx, previous.Scope, ScanOptions{Exclude: []string{s.directory}})
		if err != nil {
			return nil, err
		}
		events, err := s.observationEvents(previous, observation)
		if err != nil {
			return nil, err
		}
		events, err = s.sourceEvents(ctx, previous, events)
		if err != nil {
			return nil, err
		}
		// Reserve enough for record fields, event IDs and JSON indentation.
		// The store still enforces its exact per-record and state size limits.
		encoded, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = s.automaticBudget(ctx, int64(len(encoded))*2+65536); err != nil {
			return nil, err
		}
		return events, nil
	})
}

func (s *Store) automaticBudget(ctx context.Context, reserve int64) error {
	f, err := os.Open(filepath.Join(s.directory, "events"))
	if err != nil {
		return err
	}
	defer f.Close()
	var total int64
	count := 0
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrCorrupt
			}
			count++
			total += info.Size()
			if count >= automaticRecordLimit || total+reserve >= automaticByteLimit {
				return ErrAutomaticBudget
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
