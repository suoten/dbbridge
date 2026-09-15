package main

import (
	"embed"
	"flag"
	"fmt"

	"dbbridge/internal/webserver"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// 构建信息（由 make-release.ps1 / CI 通过 -ldflags "-X main.Version=..." 注入；
// 未注入时保持 dev，避免发布包出现空版本号）
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	showVersion := flag.Bool("version", false, "打印版本信息后退出")
	webMode := flag.Bool("web", false, "以无界面 Web 服务模式运行（服务器部署用，配合 --port）")
	port := flag.Int("port", 8989, "Web 模式监听端口")

	flag.Parse()

	if *showVersion {
		fmt.Printf("DBBridge %s (built %s, commit %s)\n", Version, BuildTime, GitCommit)
		return
	}

	if *webMode {
		// headless 模式：Linux 服务器没有显示服务，Wails GUI 无法启动，
		// 以标准 HTTP 服务提供迁移 API（dbbridge.service 使用此模式）
		webserver.Run(*port, assets, Version)
		return
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "DBBridge - 数据库迁移工具",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 248, G: 249, B: 250, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
