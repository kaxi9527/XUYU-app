package put

import (
	"os"
	"path/filepath"
	"strings"
)

// Entry 是读出来的一行索引，键是表头列名；另外补了两个统一键：
// "_abs"（文件绝对路径）和 "_time"（登记时间）。新旧两种表头都能读。
type Entry map[string]string

// ReadIndexDir 读分片目录里所有 .csv（外加 _旧格式/ 里挪开的旧分片，先读旧的），坏文件跳过。root 用来把相对路径变成绝对路径。
func ReadIndexDir(dir, root string) []Entry {
	out := readIndexFlat(filepath.Join(dir, LegacyDir), root)
	return append(out, readIndexFlat(dir, root)...)
}

func readIndexFlat(dir, root string) []Entry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".csv") {
			continue
		}
		rows, err := ReadRows(filepath.Join(dir, e.Name()))
		if err != nil || len(rows) < 2 {
			continue
		}
		head := rows[0]
		for _, r := range rows[1:] {
			m := Entry{}
			for i, h := range head {
				if i < len(r) {
					m[strings.TrimSpace(h)] = r[i]
				}
			}
			abs := m["存放路径"]
			if abs == "" && m["路径"] != "" {
				abs = m["路径"]
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(root, filepath.FromSlash(abs))
				}
			}
			m["_abs"] = abs
			m["_time"] = firstNonEmpty(m["时间带偏移"], m["时间"], m["日期"])
			if m["负责智能体"] == "" {
				m["负责智能体"] = m["登记者"]
			}
			out = append(out, m)
		}
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
