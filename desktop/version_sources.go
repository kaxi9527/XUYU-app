package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	filehome "github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

type versionSourceReader struct {
	ctx       context.Context
	remaining int64
	warnings  []string
}

func (r *versionSourceReader) warn(message string) {
	for _, old := range r.warnings {
		if old == message {
			return
		}
	}
	if len(r.warnings) < 8 {
		r.warnings = append(r.warnings, message)
	}
}
func (r *versionSourceReader) read(root, path string, limit int64) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if err := filehome.CheckPath(root, path); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("来源记录不是普通文件")
	}
	if fi.Size() > limit || fi.Size() > r.remaining {
		return nil, errors.New("达到本次来源读取上限")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(fi, opened) {
		return nil, errors.New("来源记录已变化")
	}
	data, err := io.ReadAll(io.LimitReader(f, fi.Size()+1))
	r.remaining -= int64(len(data))
	if err != nil {
		return nil, err
	}
	now, err := os.Lstat(path)
	if err != nil || !now.Mode().IsRegular() || !os.SameFile(fi, now) || fi.Size() != int64(len(data)) || !fi.ModTime().Equal(now.ModTime()) {
		return nil, errors.New("来源记录在读取中变化")
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
func claimTime(raw string) string {
	if _, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return raw
	}
	return ""
}
func sourceFieldsOK(c versions.SourceClaim) bool {
	return len(c.Tool) <= 2000 && len(c.Agent) <= 2000 && len(c.Recorder) <= 2000 && len(c.Task) <= 2000 && len(c.Source) <= 4000 && len(c.Parameters) <= 16000
}
func (r *versionSourceReader) add(items []versions.RegisterItem, index int, claim versions.SourceClaim, hash string) {
	claim.Binding = "path_only"
	if hash != "" {
		if !strings.EqualFold(hash, items[index].Content.SHA256) {
			r.warn("部分来源属于旧内容，未套用到本次登记的版本。")
			return
		}
		claim.Binding = "content_match"
	}
	if !sourceFieldsOK(claim) {
		r.warn("部分来源字段过长，本次未导入。")
		return
	}
	for _, existing := range items[index].Provenance {
		if existing == claim {
			return
		}
	}
	items[index].Provenance = append(items[index].Provenance, claim)
	if len(items[index].Provenance) > 20 {
		items[index].Provenance = items[index].Provenance[len(items[index].Provenance)-20:]
		r.warn("部分文件来源记录较多，本次只导入最近 20 条。")
	}
}

// Sources come only from the selected files' own sidecars and the configured
// local asset index. No activity-directory, chat, .codex, or network scan occurs.
func loadVersionSources(ctx context.Context, scope versionScope, items []versions.RegisterItem) []string {
	r := &versionSourceReader{ctx: ctx, remaining: 8 << 20, warnings: []string{}}
	selected := map[string]int{}
	dirs := map[string]string{}
	for i, item := range items {
		selected[item.Path] = i
		dirs[filepath.Dir(item.Path)] = item.Root
	}
	directories := []string{}
	for dir := range dirs {
		directories = append(directories, dir)
	}
	sort.Strings(directories)
	for _, dir := range directories {
		file := filepath.Join(dir, activity.SidecarName)
		data, err := r.read(dirs[dir], file, 256<<10)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			r.warn("部分文件旁的来源记录未能读取；仍可登记文件，未知来源留空。")
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var record struct {
				activity.Record
				Tool       string          `json:"tool"`
				Parameters json.RawMessage `json:"parameters"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				r.warn("部分来源记录格式有误，已跳过。")
				continue
			}
			path := record.Path
			if record.File != "" {
				if record.File != filepath.Base(record.File) || record.File == "." || record.File == ".." {
					continue
				}
				path = filepath.Join(dir, record.File)
				if record.Path != "" && record.Path != path {
					r.warn("部分来源记录的文件位置互相矛盾，已跳过。")
					continue
				}
			}
			index, ok := selected[path]
			if !ok {
				continue
			}
			parameters := ""
			if len(record.Parameters) > 0 && string(record.Parameters) != "null" {
				if json.Unmarshal(record.Parameters, &parameters) != nil {
					parameters = string(record.Parameters)
				}
			}
			source := record.Source
			switch source {
			case activity.SourceSelf, activity.SourcePut, activity.SourceApprove, activity.SourceSidecar:
				source = ""
			}
			r.add(items, index, versions.SourceClaim{Kind: "tool_declaration", Tool: record.Tool, Agent: record.Agent, Task: record.Task, Source: source, Parameters: parameters, ClaimedAt: claimTime(record.Time), Evidence: file}, record.SHA256)
		}
		if scanner.Err() != nil {
			r.warn("部分来源记录超出单条读取上限，只保留已读到的声明。")
		}
	}
	indexDir := filepath.Join(scope.config.Root, scope.config.Names.Control, scope.config.Names.IndexShards)
	if err := filehome.CheckPath(scope.config.Root, indexDir); err != nil {
		r.warn("资产索引位置无法安全读取，未导入索引来源。")
		return r.warnings
	}
	dir, err := os.Open(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return r.warnings
	}
	if err != nil {
		r.warn("资产索引暂时不可读，未知来源留空。")
		return r.warnings
	}
	entries, err := dir.ReadDir(129)
	_ = dir.Close()
	if err != nil && err != io.EOF {
		r.warn("资产索引未能完整列出。")
		return r.warnings
	}
	if len(entries) > 128 {
		entries = entries[:128]
		r.warn("索引文件较多，本次只检查部分来源记录。")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), scope.config.Names.IndexFile+".") || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}
		file := filepath.Join(indexDir, entry.Name())
		data, err := r.read(scope.config.Root, file, 2<<20)
		if err != nil {
			r.warn("部分资产索引未能读取或超出限额，来源信息可能不完整。")
			continue
		}
		reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
		reader.FieldsPerRecord = -1
		rows, err := reader.ReadAll()
		if err != nil || len(rows) == 0 || len(rows) > 10001 {
			r.warn("部分资产索引格式有误或记录过多，本次未导入。")
			continue
		}
		columns := map[string]int{}
		valid := true
		for i, name := range rows[0] {
			if _, exists := columns[name]; exists {
				valid = false
			}
			columns[name] = i
		}
		if !valid {
			r.warn("资产索引表头重复，未导入这份索引。")
			continue
		}
		value := func(row []string, key string) string {
			index, ok := columns[key]
			if !ok || index >= len(row) {
				return ""
			}
			return row[index]
		}
		for _, row := range rows[1:] {
			if len(row) != len(rows[0]) {
				r.warn("部分资产索引行不完整，已跳过。")
				continue
			}
			path := value(row, "路径")
			if path == "" {
				path = value(row, "存放路径")
			}
			if path != "" && !filepath.IsAbs(path) {
				path = filepath.Join(scope.config.Root, filepath.FromSlash(path))
			}
			index, ok := selected[path]
			if !ok {
				index, ok = selected[value(row, "原路径")]
			}
			if !ok {
				continue
			}
			when := value(row, "时间带偏移")
			if when == "" {
				when = value(row, "时间")
			}
			r.add(items, index, versions.SourceClaim{Kind: "index_record", Agent: value(row, "负责智能体"), Recorder: value(row, "登记者"), Task: value(row, "任务"), Source: value(row, "原路径"), ClaimedAt: claimTime(when), Evidence: file}, value(row, "sha256"))
		}
	}
	return r.warnings
}
