package main

import (
	"fmt"
	"github.com/kaxi9527/WorkShelf/desktop/diskgrowth"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func growthTimer(p diskgrowth.Preferences) error {
	dir, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	dir = filepath.Join(dir, "systemd", "user")
	name := "xuyu-disk-growth"
	timer := filepath.Join(dir, name+".timer")
	service := filepath.Join(dir, name+".service")
	systemctl := func(args ...string) error {
		out, e := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
		if e != nil {
			return fmt.Errorf("systemd 每日采样：%s: %w", out, e)
		}
		return nil
	}
	if !p.Enabled {
		if _, e := os.Stat(timer); os.IsNotExist(e) {
			return nil
		}
		if e := systemctl("disable", "--now", name+".timer"); e != nil {
			return e
		}
		if e := os.Remove(timer); e != nil {
			return e
		}
		if e := os.Remove(service); e != nil {
			return e
		}
		return systemctl("daemon-reload")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	f, e := currentBackgroundFiles()
	if e != nil {
		return e
	}
	quote := func(s string) string {
		return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$").Replace(s) + "\""
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	unit := "[Unit]\nDescription=XUYU disk growth sample\n[Service]\nType=oneshot\nEnvironment=" + quote("WORKSHELF_CONFIG="+f.config) + "\nExecStart=" + quote(exe) + " --disk-growth-daily\n"
	if e = os.WriteFile(service, []byte(unit), 0600); e != nil {
		return e
	}
	calendar := "[Unit]\nDescription=XUYU daily disk growth\n[Timer]\nOnCalendar=*-*-* " + p.At + ":00\nPersistent=true\nAccuracySec=1s\n[Install]\nWantedBy=timers.target\n"
	if e = os.WriteFile(timer, []byte(calendar), 0600); e != nil {
		return e
	}
	if e = systemctl("daemon-reload"); e != nil {
		return e
	}
	if e = systemctl("enable", name+".timer"); e != nil {
		return e
	}
	return systemctl("restart", name+".timer")
}
