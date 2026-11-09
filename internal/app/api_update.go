package app

import (
	"context"
	"errors"
	"os"
	"time"

	appupdate "duanju-desktop/internal/update"
)

// UpdateStateView 是更新状态快照。
type UpdateStateView = appupdate.State

func (a *App) updateManager() (*appupdate.Manager, error) {
	a.mu.RLock()
	manager := a.updater
	a.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("更新器尚未初始化")
	}
	return manager, nil
}

// UpdateState 返回当前更新状态。
func (a *App) UpdateState() (*UpdateStateView, error) {
	manager, err := a.updateManager()
	if err != nil {
		return nil, err
	}
	st := manager.Snapshot()
	return &st, nil
}

// CheckUpdate 检查更新。不自动下载。
func (a *App) CheckUpdate() (*UpdateStateView, error) {
	manager, err := a.updateManager()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	state, err := manager.Check(ctx)
	return &state, err
}

// DownloadUpdate 下载更新包。
func (a *App) DownloadUpdate() error {
	manager, err := a.updateManager()
	if err != nil {
		return err
	}
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return manager.Download(ctx)
}

// ApplyUpdate 应用已下载的更新并重启。
func (a *App) ApplyUpdate() error {
	a.mu.RLock()
	web, ctx := a.webMode, a.ctx
	a.mu.RUnlock()
	if web || ctx == nil {
		return errors.New("请在桌面程序中重启安装更新")
	}
	manager, err := a.updateManager()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err = manager.Apply(executable, os.Args[1:]); err != nil {
		return err
	}
	go func() { time.Sleep(250 * time.Millisecond); runtimeQuit(ctx) }()
	return nil
}

func (a *App) updateEvent(state appupdate.State) {
	a.emitDesktopEvent("update:state", state)
}

func (a *App) watchUpdates() {
	manager, err := a.updateManager()
	if err != nil {
		return
	}
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-manager.Done():
			return
		case <-timer.C:
		}
		for {
			ctx, cancel := a.callCtx()
			_, _ = manager.Check(ctx)
			cancel()
			timer.Reset(6 * time.Hour)
			select {
			case <-manager.Done():
				return
			case <-timer.C:
			}
		}
	}()
}
