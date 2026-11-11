package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallStatus 说明当前是不是从固定应用程序路径跑的。
type InstallStatus struct {
	OS              string `json:"os"`
	RunningPath     string `json:"runningPath"`     // 当前 .app 路径（能认出来时）
	PreferredPath   string `json:"preferredPath"`   // ~/Applications/WorkShelf.app
	InApplications  bool   `json:"inApplications"`  // 是否在 ~/Applications 或 /Applications
	NeedsInstall    bool   `json:"needsInstall"`    // 该提示用户装到固定位置
	PreferredExists bool   `json:"preferredExists"` // 固定位置已经有一份
	Message         string `json:"message"`
}

func preferredAppPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", "WorkShelf.app"), nil
}

func currentAppBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	// .../WorkShelf.app/Contents/MacOS/WorkShelf
	dir := filepath.Dir(exe) // MacOS
	dir = filepath.Dir(dir)  // Contents
	dir = filepath.Dir(dir)  // WorkShelf.app
	if strings.HasSuffix(dir, ".app") {
		return dir
	}
	return ""
}

func pathInApplications(app string) bool {
	if app == "" {
		return false
	}
	home, _ := os.UserHomeDir()
	clean := filepath.Clean(app)
	cands := []string{
		filepath.Join(home, "Applications"),
		"/Applications",
	}
	for _, c := range cands {
		if clean == filepath.Join(c, "WorkShelf.app") || strings.HasPrefix(clean, c+string(filepath.Separator)) {
			// 只认 WorkShelf.app 本身或其固定名
			if filepath.Base(clean) == "WorkShelf.app" {
				return true
			}
		}
	}
	return false
}

// InstallStatus 给设置页/横幅用。
func (s *ShelfService) InstallStatus() (InstallStatus, error) {
	st := InstallStatus{OS: runtime.GOOS}
	pref, err := preferredAppPath()
	if err != nil {
		return st, err
	}
	st.PreferredPath = pref
	if fi, err := os.Stat(pref); err == nil && fi.IsDir() {
		st.PreferredExists = true
	}
	st.RunningPath = currentAppBundle()
	st.InApplications = pathInApplications(st.RunningPath)
	if runtime.GOOS != "darwin" {
		st.NeedsInstall = false
		st.Message = "只有 Mac 需要装到固定应用程序路径来保持授权。"
		return st, nil
	}
	st.NeedsInstall = !st.InApplications
	if st.NeedsInstall {
		st.Message = "当前不在固定的应用程序位置。安装到应用程序文件夹有助于系统识别应用，权限仍可能需要重新确认。"
	} else {
		st.Message = "已从固定位置运行。系统通常会记住授权，但权限可能发生变化。"
	}
	return st, nil
}

// InstallToApplications 把当前 .app 同步到 ~/Applications/WorkShelf.app，保留原有签名。
// 不打开、不 activate。
func (s *ShelfService) InstallToApplications() (InstallStatus, error) {
	if runtime.GOOS != "darwin" {
		return s.InstallStatus()
	}
	src := currentAppBundle()
	if src == "" {
		return InstallStatus{}, fmt.Errorf("认不出当前 .app 路径，请用 scripts/install-mac.sh 手动装")
	}
	pref, err := preferredAppPath()
	if err != nil {
		return InstallStatus{}, err
	}
	if filepath.Clean(src) == filepath.Clean(pref) {
		return s.InstallStatus()
	}
	if err := installAppBundle(src, pref); err != nil {
		return InstallStatus{}, err
	}
	return s.InstallStatus()
}

func installAppBundle(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	ts := time.Now().Format("20060102150405")
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		old := dest + ".old-" + ts
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("挪走旧版失败：%w", err)
		}
	}
	if err := exec.Command("ditto", src, dest).Run(); err != nil {
		return fmt.Errorf("ditto 复制失败：%w", err)
	}
	_ = exec.Command("xattr", "-cr", dest).Run()
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", dest).Run()
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", dest).CombinedOutput(); err != nil {
		return fmt.Errorf("签名校验失败：%v\n%s", err, string(out))
	}
	return nil
}
