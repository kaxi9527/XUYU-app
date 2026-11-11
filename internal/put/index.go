package put

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Header 是资产索引的列（v4，对齐工种表）。路径、说明路径都相对总文件夹，用 / 分隔。
var Header = []string{"日期", "项目", "工种", "模板", "任务", "类型", "版本标识", "状态", "路径", "说明路径",
	"负责智能体", "敏感", "一句话说明", "扩展字段", "时间带偏移", "原路径", "大小(字节)", "sha256"}

// OldHeader 是 v1 的表头（只读兼容）。
var OldHeader = []string{"时间", "项目", "任务", "文件名", "存放路径", "原路径", "大小(字节)", "sha256", "登记者"}

var bom = []byte{0xEF, 0xBB, 0xBF}

// Row 是资产索引的一行。
type Row struct {
	Date, Project, WorkType, Template, Task, Kind, Version, Status string
	Path, NotePath, Agent, Sensitive, Note, Extra, Time, Source    string
	Size                                                           int64
	SHA256                                                         string
}

func (r Row) record() []string {
	return []string{r.Date, r.Project, r.WorkType, r.Template, r.Task, r.Kind, r.Version, r.Status, r.Path, r.NotePath,
		r.Agent, r.Sensitive, r.Note, r.Extra, r.Time, r.Source, strconv.FormatInt(r.Size, 10), r.SHA256}
}

// LegacyDir 是旧表头分片挪去的子目录名。
const LegacyDir = "_旧格式"

// AppendRows 在加锁的情况下往 CSV 末尾追加行（新文件先写 UTF-8 BOM 和表头），返回第一行新数据的行号（表头不算，从 1 开始）。
// 已有分片如果还是旧表头，先整份挪到同目录的 _旧格式/<原名>.<时间>.csv（不删、不改内容），再新开一份。
func AppendRows(path string, rows []Row) (int, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	if err := moveAsideIfOld(path); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return 0, err
	}
	defer unlockFile(f)

	existing, err := countRows(f)
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if existing < 0 {
		buf.Write(bom)
		w.Write(Header)
		existing = 0
	}
	for _, r := range rows {
		w.Write(r.record())
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return 0, err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return existing + 1, nil
}

func moveAsideIfOld(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	br := bufio.NewReader(f)
	head, _ := br.Peek(3)
	if bytes.Equal(head, bom) {
		br.Discard(3)
	}
	r := csv.NewReader(br)
	r.FieldsPerRecord = -1
	first, rerr := r.Read()
	f.Close()
	if rerr == io.EOF {
		return nil
	}
	if rerr == nil && len(first) == len(Header) && first[0] == Header[0] && first[len(first)-1] == Header[len(Header)-1] {
		return nil
	}
	dir := filepath.Join(filepath.Dir(path), LegacyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(path), ".csv")
	ts := time.Now().Format("20060102-150405")
	for i := 1; i < 100; i++ {
		dst := filepath.Join(dir, fmt.Sprintf("%s.%s.csv", base, ts))
		if i > 1 {
			dst = filepath.Join(dir, fmt.Sprintf("%s.%s-%02d.csv", base, ts, i))
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		return os.Rename(path, dst)
	}
	return fmt.Errorf("旧格式分片挪不开：%s", path)
}

// countRows 返回已有数据行数；文件是空的返回 -1。
func countRows(f *os.File) (int, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	br := bufio.NewReader(f)
	head, _ := br.Peek(3)
	if len(head) == 0 {
		return -1, nil
	}
	if bytes.Equal(head, bom) {
		br.Discard(3)
	}
	r := csv.NewReader(br)
	r.FieldsPerRecord = -1
	n := -1 // 表头
	for {
		_, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		n++
	}
	if n < 0 {
		n = 0
	}
	return n, nil
}

// ReadRows 读出整个索引（测试和查看用）。
func ReadRows(path string) ([][]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, bom)
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	return r.ReadAll()
}
