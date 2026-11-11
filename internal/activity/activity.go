// Package activity 记录「谁写了哪个文件」：总控里的每日 JSONL 活动记录，以及文件旁边的侧边记录。
// 只追加，不改不删；文件本身从不写入标记。
package activity

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/flock"
)

// SidecarName 是侧边记录的文件名：AI 没有 workshelf 命令时，在文件所在目录的这个文件末尾追加一行 JSON。
const SidecarName = ".workshelf-origin.jsonl"

// 来源（可信度从高到低）。
const (
	SourceSelf    = "自报"
	SourcePut     = "入库"
	SourceApprove = "批准"
	SourceSidecar = "侧边记录"
)

// Record 是一条活动记录。
type Record struct {
	Time     string `json:"time"`               // RFC3339，带时区偏移
	Path     string `json:"path,omitempty"`     // 绝对路径（总记录里用）
	File     string `json:"file,omitempty"`     // 侧边记录里只写文件名
	SHA256   string `json:"sha256,omitempty"`   // 记录时的内容哈希；对不上说明写后被改过
	Agent    string `json:"agent"`              // 智能体名
	WorkType string `json:"worktype,omitempty"` // 行业·子工种
	Project  string `json:"project,omitempty"`
	Task     string `json:"task,omitempty"`
	Source   string `json:"source,omitempty"`
	Action   string `json:"action,omitempty"` // 新建 / 修改 / 入库 / 批准
	Status   string `json:"status,omitempty"` // 待批准 / 已交
	Note     string `json:"note,omitempty"`
}

// When 解析记录时间，解析不了返回零值。
func (r Record) When() time.Time {
	t, err := time.Parse(time.RFC3339, r.Time)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Dir 返回活动记录目录：<总文件夹>/_总控/活动记录。
func Dir(cfg config.Config) string {
	return filepath.Join(cfg.Root, cfg.Names.Control, cfg.Names.Activity)
}

// Append 把记录按（记录时间在 loc 时区的）日期追加到 dir/YYYY-MM-DD.jsonl，加锁、只追加。
func Append(dir string, loc *time.Location, recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	if loc == nil {
		loc = time.Local
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	byDay := map[string][]Record{}
	var days []string
	for _, r := range recs {
		t := r.When()
		if t.IsZero() {
			t = time.Now()
			r.Time = t.In(loc).Format(time.RFC3339)
		}
		d := t.In(loc).Format("2006-01-02")
		if _, ok := byDay[d]; !ok {
			days = append(days, d)
		}
		byDay[d] = append(byDay[d], r)
	}
	for _, d := range days {
		if err := appendLines(filepath.Join(dir, d+".jsonl"), byDay[d]); err != nil {
			return err
		}
	}
	return nil
}

func appendLines(path string, recs []Record) error {
	var buf bytes.Buffer
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := flock.Lock(f); err != nil {
		return err
	}
	defer flock.Unlock(f)
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

// ReadSince 读 dir 里 since 之后（含）的记录，按时间从旧到新。坏行跳过。
func ReadSince(dir string, since time.Time) ([]Record, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// 文件名日期比 since 早两天以上的直接跳过（留余量给时区差）。
	cut := since.AddDate(0, 0, -2).Format("2006-01-02")
	var out []Record
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".jsonl") || len(n) < 16 {
			continue
		}
		if !since.IsZero() && n[:10] < cut {
			continue
		}
		recs, err := readJSONL(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		for _, r := range recs {
			if since.IsZero() || !r.When().Before(since) {
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When().Before(out[j].When()) })
	return out, nil
}

// ReadSidecar 读某个目录里的侧边记录，把 file 换成绝对路径放进 Path。没有就返回空。
func ReadSidecar(dir string) []Record {
	recs, err := readJSONL(filepath.Join(dir, SidecarName))
	if err != nil {
		return nil
	}
	out := recs[:0]
	for _, r := range recs {
		name := r.File
		if name == "" && r.Path != "" {
			if filepath.IsAbs(r.Path) {
				r.Source = nz(r.Source, SourceSidecar)
				out = append(out, r)
				continue
			}
			name = r.Path
		}
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		r.Path = filepath.Join(dir, name)
		r.Source = SourceSidecar
		out = append(out, r)
	}
	return out
}

func readJSONL(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r Record
		if json.Unmarshal(line, &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

func nz(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// FileSHA256 算文件的 sha256。
func FileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// MarkOptions 是 workshelf mark 的参数。
type MarkOptions struct {
	Config   config.Config
	Files    []string
	Agent    string
	WorkType string
	Project  string
	Task     string
	Note     string
	Action   string
	Now      time.Time
}

// Mark 给文件登记「谁写的」（来源=自报），记在总控活动记录里，不改文件本身。
func Mark(opt MarkOptions) ([]Record, error) {
	agent := strings.TrimSpace(opt.Agent)
	if agent == "" {
		agent = strings.TrimSpace(os.Getenv("WORKSHELF_AGENT"))
	}
	if agent == "" {
		return nil, errors.New("要写明是哪个智能体：--agent 名字（或设 WORKSHELF_AGENT）")
	}
	if len(opt.Files) == 0 {
		return nil, errors.New("没给要登记的文件")
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	loc := opt.Config.Location()
	var recs []Record
	for _, f := range opt.Files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("读不到文件：%w", err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("只登记普通文件：%s", abs)
		}
		sum, err := FileSHA256(abs)
		if err != nil {
			return nil, err
		}
		recs = append(recs, Record{
			Time: now.In(loc).Format(time.RFC3339), Path: abs, SHA256: sum, Agent: agent,
			WorkType: opt.WorkType, Project: opt.Project, Task: opt.Task, Note: opt.Note,
			Source: SourceSelf, Action: nz(opt.Action, "写入"),
		})
	}
	return recs, Append(Dir(opt.Config), loc, recs...)
}
