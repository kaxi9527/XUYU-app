package home

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/flock"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

func undoExact(cfg config.Config, path string, u undoLog) (*UndoResult, error) {
	if filepath.Clean(u.Root) != filepath.Clean(cfg.Root) {
		return nil, errors.New("恢复清单属于另一个总文件夹")
	}
	if err := CheckPath(undoDir(cfg), path); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.Root, cfg.Names.Control, "整理执行.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = flock.Lock(lock); err != nil {
		return nil, err
	}
	defer flock.Unlock(lock)
	res := &UndoResult{LogID: u.ID, RestoredPaths: []string{}}
	for i := len(u.Entries) - 1; i >= 0; i-- {
		e := u.Entries[i]
		if e.Mode == "copy-kept" {
			res.Skipped++
			res.Notes = append(res.Notes, "跨盘原件未改变，目标副本保留供检查："+e.To)
			continue
		}
		fail := func(err error) { res.Failed++; res.Notes = append(res.Notes, err.Error()) }
		targetRoot := e.TargetRoot
		if targetRoot == "" {
			targetRoot = cfg.Root
		}
		if err := CheckPath(targetRoot, e.To); err != nil {
			fail(err)
			continue
		}
		if err := CheckPath(e.SourceRoot, e.From); err != nil {
			fail(err)
			continue
		}
		if withinExact(e.To, filepath.Join(cfg.Root, cfg.Names.Control)) {
			fail(errors.New("恢复目标不能在总控中"))
			continue
		}
		if _, err := os.Lstat(e.To); os.IsNotExist(err) {
			if e.Mode == "move" {
				expected := e.Snapshot
				var original string
				var checkErr error
				if e.CrossDevice && e.CrossSnapshot != "" {
					expected = e.CrossSnapshot
					original, checkErr = crossSnapshot(e.From)
				} else {
					original, checkErr = Snapshot(e.From)
				}
				matches := checkErr == nil && original == expected
				if matches && e.SHA256 != "" {
					hash, hashErr := put.FileSHA256(e.From)
					matches = hashErr == nil && hash == e.SHA256
				}
				if matches {
					res.Unchanged++
					res.RestoredPaths = append(res.RestoredPaths, e.From)
					continue
				}
			}
			res.Skipped++
			res.Notes = append(res.Notes, "目标位置不存在，无法确认原处状态，请核对："+e.From)
			continue
		}
		if e.Mode == "move" {
			if _, err := os.Lstat(e.From); !os.IsNotExist(err) {
				res.Skipped++
				res.Notes = append(res.Notes, "原位置已有文件，未覆盖："+e.From)
				continue
			}
		} else if e.Mode != "copy" {
			fail(errors.New("恢复动作无效"))
			continue
		}
		expected := e.Snapshot
		var got string
		if e.CrossDevice && e.CrossSnapshot != "" {
			expected = e.CrossSnapshot
			got, err = crossSnapshot(e.To)
		} else {
			got, err = Snapshot(e.To)
		}
		if err != nil {
			fail(err)
			continue
		}
		if got != expected {
			fail(errors.New("整理后的文件已变化或复制不完整，保留现场：" + e.To))
			continue
		}
		if e.SHA256 != "" {
			hash, err := put.FileSHA256(e.To)
			if err != nil || hash != e.SHA256 {
				fail(errors.New("整理后的文件内容已变化，保留现场：" + e.To))
				continue
			}
		}
		dest := e.From
		if e.Mode == "copy" {
			dest = filepath.Join(undoDir(cfg), u.ID, filepath.Base(e.To))
			var err error
			dest, err = pickFileTarget(dest)
			if err != nil {
				fail(err)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			fail(err)
			continue
		}
		var moveErr error
		if e.Mode == "move" && e.CrossDevice {
			moveErr = moveCrossDevice(e.To, dest)
		} else {
			moveErr = renameExclusive(e.To, dest)
		}
		if moveErr != nil {
			fail(moveErr)
			continue
		}
		if e.Mode == "copy" {
			res.Quarantined++
		} else {
			res.Restored++
			res.RestoredPaths = append(res.RestoredPaths, e.From)
		}
	}
	return res, nil
}
