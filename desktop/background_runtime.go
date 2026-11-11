package main

import (
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func (s *ShelfService) bindBackgroundRuntime(app *application.App, window *application.WebviewWindow, enabled bool) {
	if runtime.GOOS != "darwin" {
		return
	}
	var keepRunning atomic.Bool
	keepRunning.Store(enabled)
	tray := app.SystemTray.New()
	tray.SetLabel("序屿")
	open := func() { window.Show(); window.Focus() }
	menu := app.NewMenu()
	menu.Add("打开 序屿").OnClick(func(*application.Context) { open() })
	menu.AddSeparator()
	menu.Add("退出 序屿（停止后台检查）").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu).OnClick(func() { tray.ShowMenu() })
	closing := func(event *application.WindowEvent) {
		event.Cancel()
		if keepRunning.Load() {
			window.Hide()
		} else {
			app.Quit()
		}
	}
	window.RegisterHook(events.Mac.WindowShouldClose, closing)
	window.RegisterHook(events.Common.WindowClosing, closing)
	apply := func(on bool) {
		keepRunning.Store(on)
		if on {
			tray.Show()
		} else {
			open()
			tray.Hide()
		}
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if enabled {
			tray.Show()
		} else {
			tray.Hide()
		}
	})
	s.background.mu.Lock()
	s.background.changed = apply
	s.background.mu.Unlock()
}
