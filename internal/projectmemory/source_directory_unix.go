//go:build go1.24 && unix

package projectmemory

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func discoverSourceDirectory(ctx context.Context, path string) ([]sourceCandidate, []string, error) {
	root, err := openDirectory(path)
	if err != nil {
		return nil, nil, errors.New("会话目录不可读或包含链接；已有历史保留")
	}
	defer root.Close()
	initial, err := root.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	var out []sourceCandidate
	var gaps []string
	entriesSeen := 0
	var walk func(*os.Root, string, int)
	walk = func(dir *os.Root, current string, depth int) {
		if ctx.Err() != nil || entriesSeen >= 20000 || len(out) >= maxSourceDocuments {
			gaps = append(gaps, "目录发现达到本轮预算，部分位置尚未覆盖")
			return
		}
		if depth > 8 {
			gaps = append(gaps, "超过 8 层的会话目录尚未覆盖")
			return
		}
		f, e := dir.Open(".")
		if e != nil {
			gaps = append(gaps, "部分目录不可读取")
			return
		}
		defer f.Close()
		for {
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				if ctx.Err() != nil || entriesSeen >= 20000 || len(out) >= maxSourceDocuments {
					gaps = append(gaps, "目录发现达到本轮预算，部分位置尚未覆盖")
					return
				}
				entriesSeen++
				name := entry.Name()
				info, e := dir.Lstat(name)
				if e != nil {
					gaps = append(gaps, "部分记录在发现过程中变化或不可读")
					continue
				}
				child := filepath.Join(current, name)
				if info.Mode()&os.ModeSymlink != 0 {
					gaps = append(gaps, "链接及非普通记录文件未纳入")
					continue
				}
				if info.IsDir() {
					r, e := childDirectory(dir, name, info)
					if e != nil {
						gaps = append(gaps, "目录发生变化或包含链接")
						continue
					}
					walk(r, child, depth+1)
					r.Close()
					continue
				}
				if strings.ToLower(filepath.Ext(name)) != ".jsonl" {
					continue
				}
				if !info.Mode().IsRegular() || objectKey(info) == "" {
					gaps = append(gaps, "链接及非普通记录文件未纳入")
					continue
				}
				out = append(out, sourceCandidate{Path: child, Stamp: digestJSON([]any{objectKey(info), info.Size(), info.ModTime().UnixNano()}), Modified: info.ModTime().UnixNano()})
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					gaps = append(gaps, "部分目录枚举未完成")
				}
				return
			}
		}
	}
	walk(root, path, 0)
	reopened, err := openDirectory(path)
	if err != nil {
		return nil, nil, errors.New("会话目录在发现时变化，保留原进度")
	}
	defer reopened.Close()
	current, err := reopened.Stat(".")
	if err != nil || !os.SameFile(initial, current) {
		return nil, nil, errors.New("会话目录在发现时变化，保留原进度")
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return out, uniqueSourceGaps(gaps), nil
}
