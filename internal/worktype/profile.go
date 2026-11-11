package worktype

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ProfileFile = ".workshelf-layout.json"

// Profile is an editable folder template. Rules are evaluated in display order.
type Profile struct {
	Name    string            `json:"name"`
	Folders []string          `json:"folders"`
	Roles   map[string]string `json:"roles"`
	Rules   []Rule            `json:"rules"`
}
type Rule struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Folder string `json:"folder"`
}

func ValidFolder(name string) bool {
	if name == "" || len(name) > 240 || strings.ContainsAny(name, "\\\x00\r\n:") || filepath.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.TrimSpace(part) != part || part == "_总控" {
			return false
		}
	}
	return true
}
func (p Profile) Validate() error {
	if len(p.Folders) == 0 || len(p.Folders) > 32 {
		return errors.New("模板需要 1–32 个文件夹")
	}
	seen := map[string]bool{}
	for _, f := range p.Folders {
		key := strings.ToLower(f)
		if !ValidFolder(f) || seen[key] {
			return fmt.Errorf("目录名称无效或重复：%s", f)
		}
		seen[key] = true
	}
	valid := func(f string) bool {
		for _, n := range p.Folders {
			if f == n {
				return true
			}
		}
		return false
	}
	for role, f := range p.Roles {
		switch role {
		case "brief", "reference", "source", "draft", "delivery", "subtitle", "audio":
		default:
			return fmt.Errorf("未知目录用途：%s", role)
		}
		if !valid(f) {
			return fmt.Errorf("目录用途指向了不存在的目录：%s", f)
		}
	}
	if len(p.Rules) > 100 {
		return errors.New("最多保存 100 条规则")
	}
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Value) == "" || len(r.Value) > 200 || !valid(r.Folder) {
			return fmt.Errorf("第 %d 条规则的条件或目标目录无效", i+1)
		}
		switch r.Kind {
		case "extension":
			for _, x := range strings.Split(r.Value, ",") {
				x = strings.TrimPrefix(strings.TrimSpace(x), ".")
				if x == "" || strings.ContainsAny(x, "/\\ *?\r\n") {
					return fmt.Errorf("第 %d 条规则请输入扩展名，例如 png,jpg", i+1)
				}
			}
		case "name", "path":
		default:
			return fmt.Errorf("第 %d 条规则类型无效", i+1)
		}
	}
	return nil
}
func (p Profile) Match(path string) (string, string) {
	for i, r := range p.Rules {
		value := strings.ToLower(strings.TrimSpace(r.Value))
		hit := false
		switch r.Kind {
		case "extension":
			for _, x := range strings.Split(value, ",") {
				if "."+strings.TrimPrefix(strings.TrimSpace(x), ".") == strings.ToLower(filepath.Ext(path)) {
					hit = true
				}
			}
		case "name":
			hit = strings.Contains(strings.ToLower(filepath.Base(path)), value)
		case "path":
			hit = strings.Contains(strings.ToLower(path), value)
		}
		if hit {
			return r.Folder, fmt.Sprintf("匹配自定义规则 %d：%s", i+1, r.Value)
		}
	}
	return "", ""
}
func ReadProfile(path string) (*Profile, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 128*1024 {
		return nil, errors.New("模板不是普通文件或过大")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
