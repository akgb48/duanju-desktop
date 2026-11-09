package app

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Wails 窗口操作和桌面事件集中在这里；浏览器模式使用本地 RPC。

func (a *App) emitDesktopEvent(name string, payload any) {
	a.mu.RLock()
	ctx, web := a.ctx, a.webMode
	a.mu.RUnlock()
	if ctx != nil && !web && ctx.Value("events") != nil {
		wruntime.EventsEmit(ctx, name, payload)
	}
}

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

func runtimePickDataDir(ctx context.Context, dir string) (string, error) {
	return wruntime.OpenDirectoryDialog(ctx, wruntime.OpenDialogOptions{
		Title: "选择空的数据目录", DefaultDirectory: dir, CanCreateDirectories: true,
	})
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
