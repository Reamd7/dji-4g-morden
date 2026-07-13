package main

import (
	"embed"
	"log"

	"dji-modem-research/desktop/services"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails embeds frontend/dist into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	deviceSvc := &services.DeviceService{}
	app := application.New(application.Options{
		Name:        "DJI 4G Desktop",
		Description: "DJI 4G 模组用户态驱动 — 桌面客户端",
		Services: []application.Service{
			application.NewService(deviceSvc),
			application.NewService(&services.SMSService{Device: deviceSvc}),
			application.NewService(&services.DialerService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "DJI 4G Desktop",
		Width:  1000,
		Height: 618,
		// 去掉 MacTitleBarHiddenInset:它让整个 webview 可拖动,导致无法选中文字。
		// 改用标准标题栏(原生拖动 + close/minimize 按钮,内容区可正常交互)。
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
