// Package projectcreate creates a new, empty project from a reviewed template.
// Existing projects are never merged or overwritten by this operation.
package projectcreate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/home"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

type Input struct {
	OperationID string           `json:"operationID"`
	Name        string           `json:"name"`
	WorkType    string           `json:"workType"`
	Root        string           `json:"root"`
	Profile     worktype.Profile `json:"profile"`
}
type Result struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Folders []string `json:"folders"`
}
type receipt struct {
	OperationID string `json:"operationID"`
	Digest      string `json:"digest"`
	CreatedAt   string `json:"createdAt"`
}

func Create(root, readmeName string, in Input) (Result, error) {
	var zero Result
	if in.Root != root {
		return zero, errors.New("保存位置已变化，请重新预览")
	}
	if err := put.CheckName("项目", in.Name); err != nil {
		return zero, err
	}
	if strings.HasPrefix(in.Name, ".") || strings.HasPrefix(in.Name, "_") || strings.TrimSpace(in.Name) != in.Name {
		return zero, errors.New("请使用普通项目名称，不要以点或下划线开头")
	}
	if len(in.OperationID) < 16 || len(in.OperationID) > 100 {
		return zero, errors.New("创建标识无效，请重新打开新建项目")
	}
	known := false
	for _, w := range worktype.All() {
		if w.Label() == in.WorkType {
			known = true
			break
		}
	}
	if !known {
		return zero, errors.New("请从工种列表选择一项")
	}
	if err := in.Profile.Validate(); err != nil {
		return zero, err
	}
	path := filepath.Join(root, in.Name)
	if err := home.CheckPath(root, path); err != nil {
		return zero, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return zero, errors.New("保存位置无法访问，请先在设置中选择已有文件夹")
	}
	encoded, _ := json.Marshal(in)
	hash := sha256.Sum256(encoded)
	digest := hex.EncodeToString(hash[:])
	base, err := os.OpenRoot(root)
	if err != nil {
		return zero, err
	}
	defer base.Close()
	receiptPath := ".workshelf-created.json"
	r := receipt{in.OperationID, digest, time.Now().Format(time.RFC3339)}
	created := false
	if err := base.Mkdir(in.Name, 0755); err != nil {
		if !os.IsExist(err) {
			return zero, errors.New("无法建立项目文件夹，请检查保存位置权限")
		}
	} else {
		created = true
	}
	fi, err := base.Lstat(in.Name)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return zero, errors.New("项目位置已变化，请重新预览")
	}
	project, err := base.OpenRoot(in.Name)
	if err != nil {
		return zero, err
	}
	defer project.Close()
	if !created {
		data, e := readRegular(project, receiptPath)
		var prior receipt
		if e != nil || json.Unmarshal(data, &prior) != nil || prior.OperationID != in.OperationID || prior.Digest != digest {
			return zero, errors.New("这个位置已有同名项目，请换一个名称；原文件未改动")
		}
		r = prior
	} else {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := writeExclusive(project, receiptPath, append(data, '\n')); err != nil {
			return zero, errors.New("项目目录已建立，但创建记录未保存；请检查该目录后使用其他名称")
		}
	}
	// Retry only finishes this exact operation. It never overwrites changed files.
	for _, folder := range in.Profile.Folders {
		p := filepath.Join(path, folder)
		if err := home.CheckPath(root, p); err != nil {
			return zero, err
		}
		if err := project.MkdirAll(folder, 0755); err != nil {
			return zero, fmt.Errorf("项目已建立，目录 %s 尚未完成；修正后可重试", folder)
		}
	}
	profile, _ := json.MarshalIndent(in.Profile, "", "  ")
	readme := fmt.Sprintf("# %s\n\n- 工种：%s\n- 建立：%s（WorkShelf）\n\n## 文件目录\n\n", in.Name, in.WorkType, r.CreatedAt)
	for _, folder := range in.Profile.Folders {
		readme += "- " + folder + "/\n"
	}
	readme += "\n目录可在整理时自定义；文件追踪需在「文件记录」中开启。\n"
	for name, data := range map[string][]byte{worktype.ProfileFile: append(profile, '\n'), readmeName: []byte(readme)} {
		p := filepath.Join(path, name)
		if err := home.CheckPath(root, p); err != nil {
			return zero, err
		}
		if err := writeExclusive(project, name, data); err != nil {
			prior, e := readRegular(project, name)
			if !os.IsExist(err) || e != nil || string(prior) != string(data) {
				return zero, fmt.Errorf("项目已建立，%s 未完成或已被修改；现有内容已保留", name)
			}
		}
	}
	return Result{in.Name, path, append([]string{}, in.Profile.Folders...)}, nil
}

func writeExclusive(root *os.Root, path string, data []byte) error {
	f, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func readRegular(root *os.Root, path string) ([]byte, error) {
	fi, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("创建记录不是普通文件")
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if len(data) > 1<<20 {
		return nil, errors.New("创建记录超出大小限制")
	}
	return data, err
}
