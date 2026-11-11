package autoorganize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

func ReadPolicy(cfg config.Config) (Policy, error) {
	var p Policy
	b, err := os.ReadFile(filepath.Join(dir(cfg), "设置.json"))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	return p, err
}
func SavePolicy(cfg config.Config, p Policy) error {
	for _, root := range p.Roots {
		if !filepath.IsAbs(root) {
			return errors.New("自动整理范围需要完整路径")
		}
	}
	for _, source := range p.Sources {
		if !sourceevidence.Supported(source.Adapter) || !filepath.IsAbs(source.Path) {
			return errors.New("请选择有效的会话目录")
		}
	}
	_, err := locked(cfg, func() (Operation, error) {
		return Operation{}, writePolicy(cfg, p)
	})
	return err
}
func writePolicy(cfg config.Config, p Policy) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeModelFile(cfg, "设置.json", b)
}
func List(cfg config.Config) ([]Operation, error) {
	entries, err := os.ReadDir(dir(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return []Operation{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Operation
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) != 37 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		o, e := read(cfg, entry.Name()[:32])
		if e == nil {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func ReceiptPath(cfg config.Config, id string) string { return receiptPath(cfg, id) }
func (p Policy) ScanSince() time.Time                 { return time.Time{} }
