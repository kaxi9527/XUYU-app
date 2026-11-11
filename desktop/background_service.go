package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

const backgroundLabel = "com.kaxi9527.workshelf.background"

type backgroundPreferences struct {
	Version int  `json:"version"`
	Enabled bool `json:"enabled"`
}

type backgroundConnection struct {
	mu      sync.Mutex
	changed func(bool)
}

type BackgroundStatus struct {
	Supported      bool   `json:"supported"`
	Enabled        bool   `json:"enabled"`
	StartAtLogin   bool   `json:"startAtLogin"`
	LoginAvailable bool   `json:"loginAvailable"`
	LoginProblem   string `json:"loginProblem,omitempty"`
	Message        string `json:"message"`
}

type backgroundFiles struct{ home, config, app string }

func currentBackgroundFiles() (backgroundFiles, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return backgroundFiles{}, err
	}
	path, err := filepath.Abs(config.DefaultPath(home))
	if err != nil {
		return backgroundFiles{}, err
	}
	return backgroundFiles{home: home, config: path, app: currentAppBundle()}, nil
}

func (f backgroundFiles) preferencesPath() string {
	return filepath.Join(filepath.Dir(f.config), "background.json")
}
func (f backgroundFiles) loginPath() string {
	return filepath.Join(f.home, "Library", "LaunchAgents", backgroundLabel+".plist")
}
func (f backgroundFiles) fixedApplication() bool {
	return f.app == filepath.Join(f.home, "Applications", "WorkShelf.app") || f.app == "/Applications/WorkShelf.app"
}
func (f backgroundFiles) loginDocument() []byte {
	escape := func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + backgroundLabel + `</string>
<key>ProgramArguments</key><array><string>` + escape(filepath.Join(f.app, "Contents", "MacOS", "WorkShelf")) + `</string><string>--background</string></array>
<key>EnvironmentVariables</key><dict><key>WORKSHELF_CONFIG</key><string>` + escape(f.config) + `</string></dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><false/>
<key>LimitLoadToSessionType</key><string>Aqua</string>
<key>WorkShelfManaged</key><string>v1</string>
</dict></plist>
`)
}

func (f backgroundFiles) read() (BackgroundStatus, error) {
	status := BackgroundStatus{Supported: backgroundPlatformSupported(), LoginAvailable: f.fixedApplication(), Message: "仅检查已授权的项目；文件变化后合并检查并定期补扫。监听状态可在项目文件记录中查看；退出期间不记录，重新运行后补扫。"}
	b, err := readBackgroundFile(f.preferencesPath())
	if err != nil {
		return status, err
	}
	if len(b) > 0 {
		var p backgroundPreferences
		if err = json.Unmarshal(b, &p); err != nil || p.Version != 1 {
			return status, errors.New("后台设置无法读取，暂不保持后台运行")
		}
		status.Enabled = p.Enabled
	}
	b, err = readBackgroundFile(f.loginPath())
	if err != nil {
		status.LoginProblem = err.Error()
		return status, nil
	}
	if len(b) > 0 {
		if bytes.Equal(b, f.loginDocument()) {
			status.StartAtLogin = true
		} else {
			status.LoginProblem = "登录启动项由其他配置创建或已被修改，序屿不会覆盖。"
		}
	}
	if !status.LoginAvailable && status.LoginProblem == "" {
		status.LoginProblem = "安装到应用程序文件夹后，可开启登录启动。"
	}
	return status, nil
}

func (f backgroundFiles) set(enabled, login bool) (BackgroundStatus, error) {
	if !backgroundPlatformSupported() {
		return BackgroundStatus{}, errors.New("当前系统暂不支持后台运行设置")
	}
	if login && !enabled {
		return BackgroundStatus{}, errors.New("登录启动需要同时开启后台运行")
	}
	root, unlock, err := lockBackgroundDirectory(filepath.Dir(f.config))
	if err != nil {
		return BackgroundStatus{}, err
	}
	defer unlock()
	previous, err := readBackgroundFile(f.preferencesPath())
	if err != nil {
		return BackgroundStatus{}, err
	}
	state, err := f.read()
	if err != nil {
		return state, err
	}
	if login && !state.LoginAvailable {
		return state, errors.New(state.LoginProblem)
	}
	// Only touch a startup item after exact ownership readback. Other profiles
	// and user-edited launch files remain untouched, including when disabling.
	if login != state.StartAtLogin {
		if state.LoginProblem != "" && state.LoginAvailable {
			return state, errors.New(state.LoginProblem)
		}
		if err = f.setLogin(login); err != nil {
			return state, err
		}
	}
	b, _ := json.MarshalIndent(backgroundPreferences{Version: 1, Enabled: enabled}, "", "  ")
	if err = writeBackgroundPreferences(root, f.preferencesPath(), previous, append(b, '\n')); err != nil {
		return state, err
	}
	return f.read()
}

func (s *ShelfService) BackgroundStatus() (BackgroundStatus, error) {
	s.background.mu.Lock()
	defer s.background.mu.Unlock()
	f, err := currentBackgroundFiles()
	if err != nil {
		return BackgroundStatus{}, err
	}
	return f.read()
}

func (s *ShelfService) SetBackgroundSettings(enabled, login bool) (BackgroundStatus, error) {
	s.background.mu.Lock()
	f, err := currentBackgroundFiles()
	var state BackgroundStatus
	if err == nil {
		state, err = f.set(enabled, login)
	}
	changed := s.background.changed
	s.background.mu.Unlock()
	if err == nil && changed != nil {
		changed(state.Enabled)
	}
	return state, err
}

func backgroundInstanceID() string {
	f, err := currentBackgroundFiles()
	if err != nil {
		return backgroundLabel
	}
	sum := sha256.Sum256([]byte(filepath.Clean(f.config)))
	if runtime.GOOS == "linux" {
		return fmt.Sprintf("%s.i%x", backgroundLabel, sum[:12])
	}
	return fmt.Sprintf("%s.%x", backgroundLabel, sum[:12])
}
