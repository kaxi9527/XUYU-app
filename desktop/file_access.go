package main

import (
	"os"
	"path/filepath"
)

type FileAccess struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

// CheckFileAccess is called after the user chooses to check access. On macOS,
// reading a protected folder lets the system show its own authorization prompt.
func (s *ShelfService) CheckFileAccess() ([]FileAccess, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return checkFileAccess(home), nil
}

func checkFileAccess(home string) []FileAccess {
	folders := []struct{ name, dir string }{{"桌面", "Desktop"}, {"文稿", "Documents"}, {"下载", "Downloads"}, {"Projects", "Projects"}, {"主目录", "."}}
	out := make([]FileAccess, 0, len(folders))
	for _, folder := range folders {
		path := filepath.Join(home, folder.dir)
		_, err := os.ReadDir(path)
		status := "可访问"
		if os.IsNotExist(err) {
			status = "文件夹不存在"
		} else if err != nil {
			status = "无法访问"
		}
		out = append(out, FileAccess{Name: folder.name, Path: path, Status: status})
	}
	return out
}
