package main

import (
	"fmt"
	"github.com/kaxi9527/WorkShelf/desktop/diskgrowth"
	"os"
	"os/exec"
)

func growthTimer(p diskgrowth.Preferences) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	name := "XUYU Disk Growth"
	args := []string{"/Create", "/TN", name, "/TR", "\"" + exe + "\" --disk-growth-daily", "/SC", "DAILY", "/ST", p.At, "/IT", "/F"}
	if !p.Enabled {
		if exec.Command("schtasks", "/Query", "/TN", name).Run() != nil {
			return nil
		}
		args = []string{"/Delete", "/TN", name, "/F"}
	}
	out, e := exec.Command("schtasks", args...).CombinedOutput()
	if e != nil {
		return fmt.Errorf("注册或移除每日采样计划失败：%s: %w", out, e)
	}
	return nil
}
