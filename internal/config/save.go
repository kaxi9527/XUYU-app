package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RelHome 把主目录下的绝对路径收成 ~/…，方便写进配置。
func RelHome(abs, home string) string {
	abs = filepath.Clean(abs)
	home = filepath.Clean(home)
	if abs == home {
		return "~"
	}
	if strings.HasPrefix(abs, home+string(filepath.Separator)) {
		return "~" + abs[len(home):]
	}
	return abs
}

// NestedOrEqual 判断 a、b 是否相同，或一方落在另一方里面（会造成总文件夹嵌套混乱）。
func NestedOrEqual(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(a+sep, b+sep) || strings.HasPrefix(b+sep, a+sep)
}

// CheckNewRoot 检查新总文件夹能不能用：绝对路径、已存在的目录、不和旧 root 互相嵌套（相同可以，算没改）。
func CheckNewRoot(newRoot, oldRoot, home string) (abs string, err error) {
	abs = filepath.Clean(Expand(strings.TrimSpace(newRoot), home))
	if abs == "" || !filepath.IsAbs(abs) {
		return "", fmt.Errorf("总文件夹必须是完整路径（用 / 或 ~/ 开头），现在是 %q", newRoot)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("这个文件夹还不存在：%s（请先建好再选，或换一个已有的）", abs)
		}
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("总文件夹必须是文件夹，不是文件：%s", abs)
	}
	old := filepath.Clean(Expand(strings.TrimSpace(oldRoot), home))
	if old != "" && abs != old && NestedOrEqual(abs, old) {
		return "", fmt.Errorf("新总文件夹和现在的（%s）互相套在一起，会乱，请换一个不嵌套的位置", old)
	}
	return abs, nil
}

// PatchJSONFile 读已有 JSON 对象（没有就新建空对象），用 patch 覆盖同名键，其他键原样保留；
// 写之前先另存 .bak-时间戳，再 tmp+rename 原子写。返回备份路径（没有旧文件时为空）。
func PatchJSONFile(path string, patch map[string]any) (bak string, err error) {
	if path == "" {
		return "", errors.New("配置文件路径为空")
	}
	m := map[string]any{}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(old, &m); err != nil {
			return "", fmt.Errorf("现有配置文件不是合法 JSON，没改：%w", err)
		}
		bak = fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(bak, old, 0o600); err != nil {
			return "", err
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	for k, v := range patch {
		m[k] = v
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return bak, err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return bak, err
	}
	tmp := fmt.Sprintf("%s.tmp-yy-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return bak, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return bak, err
	}
	return bak, nil
}
