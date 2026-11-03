package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"duanju-desktop/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	web := flag.Bool("web", false, "在浏览器中运行相同界面")
	port := flag.Int("port", 34116, "浏览器模式本地端口；0 为随机端口")
	dataDir := flag.String("data-dir", "", "独立数据目录")
	flag.Parse()
	if *dataDir != "" {
		os.Setenv("HG_DATA_DIR", *dataDir)
	}
	a := app.NewInitialized()
	ui, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	if *web {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("短剧电脑版客户端：http://%s\n", listener.Addr())
		log.Fatal(http.Serve(listener, app.WebHandler(a, ui)))
		return
	}

	messages := windows.DefaultMessages()
	messages.Error = "无法启动短剧电脑版"
	messages.ContactAdmin = "此电脑没有可用的系统网页组件，当前版本无法启动。"
	messages.Webview2NotInstalled = "没有可用的系统 WebView2 组件"
	err = wails.Run(&options.App{
		Title:     "短剧电脑版",
		Width:     1480,
		Height:    760,
		MinWidth:  720,
		MinHeight: 480,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: ui,
		},
		OnStartup:  a.OnStartup,
		OnDomReady: func(ctx context.Context) { wruntime.WindowShow(ctx) },
		OnShutdown: a.OnShutdown,
		Bind: []any{
			a,
		},
		Windows: &windows.Options{
			Messages:             messages,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			Theme:                windows.Dark,
			BackdropType:         windows.Mica,
		},
		Debug: options.Debug{
			OpenInspectorOnStartup: os.Getenv("HG_DEBUG") == "1",
		},
	})
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}
