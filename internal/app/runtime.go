package app

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 本文件封装 Wails 窗口操作。
//
// 单独成文件的意义：让 api_*.go 里的业务代码不直接 import wails 运行时，
// 便于在无 Wails 的环境（单元测试、CI）里编译与测试。

// runtimeWindowMinimise 最小化窗口。
func runtimeWindowMinimise(ctx context.Context) {
	wruntime.WindowMinimise(ctx)
}

// runtimeWindowToggleMaximise 最大化/还原窗口。
func runtimeWindowToggleMaximise(ctx context.Context) {
	wruntime.WindowToggleMaximise(ctx)
}

// runtimeWindowFullscreen 切换全屏。
func runtimeWindowFullscreen(ctx context.Context) {
	wruntime.WindowFullscreen(ctx)
}

// runtimeQuit 退出应用。
func runtimeQuit(ctx context.Context) {
	wruntime.Quit(ctx)
}

// windowCall 串行执行一次窗口/运行时操作，避免并发进入 Wails 运行时。
func (a *App) windowCall(fn func()) {
	winMu.Lock()
	defer winMu.Unlock()
	if a.ctx == nil {
		return
	}
	fn()
}

// winMu 保护窗口运行时调用。
var winMu sync.Mutex
