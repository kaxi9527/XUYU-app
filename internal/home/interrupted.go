package home

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
)

// InterruptedExactItems reconstructs completed moves from a durable receipt.
// An absent source and a matching destination are required; other cases are
// left for the user to inspect rather than reported as completed moves.
func InterruptedExactItems(cfg config.Config, path string) ([]ResultItem, int, error) {
	if err := CheckPath(undoDir(cfg), path); err != nil {
		return nil, 0, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var u undoLog
	if err = json.Unmarshal(b, &u); err != nil {
		return nil, 0, err
	}
	if u.Version != 2 || filepath.Clean(u.Root) != filepath.Clean(cfg.Root) {
		return nil, 0, errors.New("恢复清单不属于当前整理目录")
	}
	var items []ResultItem
	uncertain := 0
	for _, e := range u.Entries {
		if e.Mode != "move" {
			continue
		}
		_, sourceErr := os.Lstat(e.From)
		_, targetErr := os.Lstat(e.To)
		if sourceErr == nil && os.IsNotExist(targetErr) {
			continue // This entry had not yet moved.
		}
		targetRoot := e.TargetRoot
		if targetRoot == "" {
			targetRoot = cfg.Root
		}
		if !os.IsNotExist(sourceErr) || targetErr != nil || CheckPath(e.SourceRoot, e.From) != nil || CheckPath(targetRoot, e.To) != nil {
			uncertain++
			continue
		}
		snapshot, err := Snapshot(e.To)
		if err != nil || snapshot != e.Snapshot {
			uncertain++
			continue
		}
		if e.SHA256 != "" {
			hash, err := put.FileSHA256(e.To)
			if err != nil || hash != e.SHA256 {
				uncertain++
				continue
			}
		}
		items = append(items, ResultItem{Source: e.From, Target: e.To, Action: "搬走", Undoable: true, SHA256: e.SHA256})
	}
	return items, uncertain, nil
}
