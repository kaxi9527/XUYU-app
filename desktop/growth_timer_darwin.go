package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kaxi9527/WorkShelf/desktop/diskgrowth"
)

func growthTimer(p diskgrowth.Preferences) error {
	f, e := currentBackgroundFiles()
	if e != nil {
		return e
	}
	if !p.Enabled && f.app == "" {
		return nil
	}
	if !f.fixedApplication() && p.Enabled {
		return fmt.Errorf("请先将序屿安装到固定的应用程序文件夹")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	at, _ := time.Parse("15:04", p.At)
	escape := func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	label := "com.kaxi9527.workshelf.disk-growth"
	doc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>--disk-growth-daily</string></array><key>EnvironmentVariables</key><dict><key>WORKSHELF_CONFIG</key><string>%s</string></dict><key>RunAtLoad</key><true/><key>StartCalendarInterval</key><dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict><key>ProcessType</key><string>Background</string></dict></plist>`, label, escape(exe), escape(f.config), at.Hour(), at.Minute())
	dir := filepath.Join(f.home, "Library", "LaunchAgents")
	path := filepath.Join(dir, label+".plist")
	domain := "gui/" + strconv.Itoa(os.Getuid())
	service := domain + "/" + label
	loaded := exec.Command("/bin/launchctl", "print", service).Run() == nil
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.Enabled && loaded && string(old) == doc {
		return nil
	}
	if loaded {
		if out, e := exec.Command("/bin/launchctl", "bootout", service).CombinedOutput(); e != nil {
			return fmt.Errorf("卸载旧采样时间：%s: %w", out, e)
		}
	}
	if !p.Enabled {
		if len(old) > 0 {
			return os.Remove(path)
		}
		return nil
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = os.WriteFile(path, []byte(doc), 0600); e != nil {
		return e
	}
	if out, e := exec.Command("/bin/launchctl", "bootstrap", domain, path).CombinedOutput(); e != nil {
		return fmt.Errorf("注册每日采样：%s: %w", out, e)
	}
	return nil
}
