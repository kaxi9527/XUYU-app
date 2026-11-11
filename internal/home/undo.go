package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

type undoEntry struct {
	Snapshot      string `json:"snapshot,omitempty"`
	CrossSnapshot string `json:"crossSnapshot,omitempty"`
	SourceRoot    string `json:"sourceRoot,omitempty"`
	TargetRoot    string `json:"targetRoot,omitempty"`
	Mode          string `json:"mode"` // move | copy
	From          string `json:"from"` // 归位前的源路径
	To            string `json:"to"`   // 归位后的目标路径
	SHA256        string `json:"sha256,omitempty"`
	CrossDevice   bool   `json:"crossDevice,omitempty"`
}

type undoLog struct {
	Version   int         `json:"version,omitempty"`
	ID        string      `json:"id"`
	CreatedAt string      `json:"createdAt"`
	Root      string      `json:"root"`
	Agent     string      `json:"agent"`
	Entries   []undoEntry `json:"entries"`
}

func logsDir(cfg config.Config) string {
	return filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.Logs)
}

func undoDir(cfg config.Config) string {
	return filepath.Join(cfg.Root, cfg.Names.Control, "整理撤销")
}

func writeLogs(cfg config.Config, s *Summary, u *undoLog, now time.Time) error {
	dir := logsDir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 人类可读摘要
	var b []byte
	b, _ = json.MarshalIndent(map[string]any{
		"id": s.LogID, "at": now.Format(time.RFC3339), "root": s.Root,
		"moved": s.Moved, "copied": s.Copied, "skipped": s.Skipped, "failed": s.Failed,
		"crossHint": s.CrossHint, "items": s.Items,
	}, "", "  ")
	logPath, err := writeNewNumbered(filepath.Join(dir, "归位-"+s.LogID+".json"), append(b, '\n'))
	if err != nil {
		return err
	}
	s.LogPath = logPath

	if u == nil || len(u.Entries) == 0 {
		return nil
	}
	ud := undoDir(cfg)
	if err := os.MkdirAll(ud, 0o755); err != nil {
		return err
	}
	ub, _ := json.MarshalIndent(u, "", "  ")
	up, err := writeNewNumbered(filepath.Join(ud, "undo-"+s.LogID+".json"), append(ub, '\n'))
	if err != nil {
		return err
	}
	s.UndoPath = up
	return nil
}

func writeNewNumbered(path string, data []byte) (string, error) {
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	for i := 1; i < 1000; i++ {
		p := path
		if i > 1 {
			p = fmt.Sprintf("%s-%02d%s", stem, i, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return p, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return p, err
		}
		return p, f.Close()
	}
	return "", fmt.Errorf("同名日志太多：%s", path)
}

// UndoResult 是撤销结果。
type UndoResult struct {
	LogID         string   `json:"logId"`
	Restored      int      `json:"restored"`    // 同盘搬回去的条数
	Quarantined   int      `json:"quarantined"` // 跨盘副本挪进整理撤销的条数
	Skipped       int      `json:"skipped"`
	Unchanged     int      `json:"unchanged,omitempty"` // 已在原处且与恢复记录一致，无需再次移动
	Failed        int      `json:"failed"`
	Notes         []string `json:"notes,omitempty"`
	RestoredPaths []string `json:"restoredPaths"`
}

// Undo 按撤销清单恢复：
//   - 搬走：目标搬回源路径（源处必须空着）
//   - 历史跨盘复制：把目标副本挪进 <总控>/整理撤销/<id>/
//
// undoPath 是 writeLogs 写下的 undo-*.json。
func Undo(cfg config.Config, undoPath string) (*UndoResult, error) {
	b, err := os.ReadFile(undoPath)
	if err != nil {
		return nil, err
	}
	var u undoLog
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("撤销清单读不了：%w", err)
	}
	if u.Version == 2 {
		return undoExact(cfg, undoPath, u)
	}
	res := &UndoResult{LogID: u.ID, RestoredPaths: []string{}}
	qroot := filepath.Join(undoDir(cfg), u.ID)
	for _, e := range u.Entries {
		switch e.Mode {
		case "move":
			if _, err := os.Stat(e.From); err == nil {
				res.Skipped++
				res.Notes = append(res.Notes, "源处已有东西，没把 "+e.To+" 搬回去："+e.From)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(e.From), 0o755); err != nil {
				res.Failed++
				res.Notes = append(res.Notes, err.Error())
				continue
			}
			var err error
			if e.CrossDevice {
				err = moveCrossDevice(e.To, e.From)
			} else {
				err = os.Rename(e.To, e.From)
			}
			if err != nil {
				res.Failed++
				res.Notes = append(res.Notes, "搬回去失败："+err.Error())
				continue
			}
			res.Restored++
			res.RestoredPaths = append(res.RestoredPaths, e.From)
		case "copy":
			// 不删目标；挪进整理撤销目录
			if _, err := os.Stat(e.To); err != nil {
				res.Skipped++
				res.Notes = append(res.Notes, "副本已不在："+e.To)
				continue
			}
			base := filepath.Base(e.To)
			dest := filepath.Join(qroot, base)
			if err := os.MkdirAll(qroot, 0o755); err != nil {
				res.Failed++
				res.Notes = append(res.Notes, err.Error())
				continue
			}
			if _, err := os.Stat(dest); err == nil {
				dest = filepath.Join(qroot, fmt.Sprintf("%s-%d", base, time.Now().UnixNano()))
			}
			if err := os.Rename(e.To, dest); err != nil {
				// 跨盘到撤销目录也可能失败：再试复制到撤销目录，仍不删原目标（留下两份副本）
				fi, e2 := os.Stat(e.To)
				if e2 != nil {
					res.Failed++
					res.Notes = append(res.Notes, "副本挪进撤销目录失败："+err.Error())
					continue
				}
				if _, _, e3 := copyTreeOrFile(e.To, dest, fi); e3 != nil {
					res.Failed++
					res.Notes = append(res.Notes, "副本挪进撤销目录失败："+err.Error()+"；改复制也失败："+e3.Error())
					continue
				}
				res.Notes = append(res.Notes, "副本已复制进撤销目录（原目标还在，需你自己处理）："+dest)
				res.Quarantined++
				continue
			}
			res.Quarantined++
		default:
			res.Skipped++
		}
	}
	return res, nil
}
