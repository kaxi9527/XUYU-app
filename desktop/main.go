package main

import (
	"embed"
	"log"
	"os"
	"runtime"
	"slices"
	"sync"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	svc := &ShelfService{}
	background, _ := svc.BackgroundStatus()
	var window *application.WebviewWindow
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			svc.closeAutoOrganize()
			svc.closePlacements()
			svc.closeProjectMemoryObservation()
			svc.closeAgentDiscovery()
			svc.closeModels()
			svc.connections.accounts.close()
		})
	}
	defer shutdown()
	services := []application.Service{application.NewService(svc)}
	var notifier *notifications.NotificationService
	if runtime.GOOS == "darwin" && currentAppBundle() != "" && os.Getenv("WORKSHELF_DISABLE_NOTIFICATIONS") != "1" {
		notifier = notifications.New()
		services = append(services, application.NewService(notifier))
	}
	app := application.New(application.Options{
		Name:        "序屿 XUYU",
		Description: "序屿：归拢项目文件，按工种模板与自己的规则整理",
		OnShutdown:  shutdown,
		SingleInstance: &application.SingleInstanceOptions{UniqueID: backgroundInstanceID(), OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if window != nil {
				window.Show()
				window.Focus()
			}
		}},
		Services: services,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "序屿 XUYU",
		Width:     1180,
		Height:    760,
		MinWidth:  860,
		MinHeight: 560,
		Hidden:    background.Enabled && slices.Contains(os.Args[1:], "--background"),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 40,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(250, 250, 249),
		URL:              "/",
	})
	if notifier != nil {
		ready := make(chan struct{})
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { close(ready) })
		notifier.OnNotificationResponse(func(result notifications.NotificationResult) {
			if result.Error == nil {
				window.Show()
				window.Focus()
			}
		})
		svc.autoOrganize.report = func(operation autoorganize.Operation, restored bool) {
			go func() {
				<-ready
				allowed, err := notifier.RequestNotificationAuthorization()
				if err != nil || !allowed {
					return
				}
				title, body := autoReportText(operation, restored)
				kind := "organize"
				if restored {
					kind = "restore"
				}
				_ = notifier.SendNotification(notifications.NotificationOptions{ID: operation.ID + "-" + kind, Title: title, Body: body})
			}()
		}
	}
	svc.bindBackgroundRuntime(app, window, background.Enabled)

	if background.Enabled && slices.Contains(os.Args[1:], "--background") {
		autoRefreshRegistry(svc)
	}
	svc.startAgentDiscovery()
	svc.startProjectMemoryObservation()
	svc.startAutoOrganize()
	err := app.Run()
	shutdown()
	if err != nil {
		log.Fatal(err)
	}
}
