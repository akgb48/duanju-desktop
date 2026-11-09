package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"duanju-desktop/internal/nativedec"
)

type HEVCDecodeStatus struct {
	Supported     bool   `json:"supported"`
	Backend       string `json:"backend"`
	CPUFallback   bool   `json:"cpuFallback"`
	InstallerPath string `json:"installerPath"`
}

func (a *App) HEVCStatus() (*HEVCDecodeStatus, error) {
	status, _ := a.NativeDecStatus()
	return &HEVCDecodeStatus{Supported: status.Available, Backend: "native-or-wasm", CPUFallback: true}, nil
}
func (a *App) HEVCDiag() (any, error) { return a.NativeDecStatus() }
func (a *App) RevealHEVCInstaller() error {
	return errors.New("原生解码组件由程序自动准备，无需安装系统扩展")
}
func (a *App) nativeDecoder() *nativedec.Manager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.native
}
func (a *App) NativeDecStatus() (*nativedec.Status, error) {
	manager := a.nativeDecoder()
	if manager == nil {
		return &nativedec.Status{FFmpeg: "missing", Reason: "桌面解码器正在初始化"}, nil
	}
	status := manager.Status()
	return &status, nil
}
func (a *App) NativeDecPrepare() error {
	manager := a.nativeDecoder()
	if manager == nil {
		return errors.New("桌面解码器尚未初始化")
	}
	return manager.Prepare()
}
func (a *App) NativeDecOpen(rawURL string, position float64) (int, error) {
	a.mu.RLock()
	manager, base := a.native, a.proxyBase
	a.mu.RUnlock()
	if manager == nil {
		return 0, errors.New("原生解码仅在桌面模式可用")
	}
	u, err := url.Parse(rawURL)
	local, _ := url.Parse(base)
	if err != nil || local == nil || u.Scheme != "http" || u.Host != local.Host || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/media/") {
		return 0, errors.New("原生解码只能读取本机媒体代理")
	}
	return manager.Open(rawURL, position)
}
func (a *App) NativeDecSeek(id int, position float64) (int, error) {
	manager := a.nativeDecoder()
	if manager == nil {
		return 0, errors.New("原生解码器未就绪")
	}
	return manager.Seek(id, position)
}
func (a *App) NativeDecClose(id int) error {
	manager := a.nativeDecoder()
	if manager != nil {
		manager.Close(id)
	}
	return nil
}
func (a *App) NativeDecSetCPUOnly(enabled bool) error {
	manager := a.nativeDecoder()
	if manager != nil {
		manager.SetCPUOnly(enabled)
	}
	return nil
}

// Neighbour preparation uses cached models only and never creates devices or
// competes with foreground model recovery.
func (a *App) NativeDecPreload(seriesID, videoID string) error {
	svc, err := a.service()
	if err != nil {
		return nil
	}
	d, ok := svc.Client().CachedVideoDetail(videoID)
	if !ok || len(d.Sources) == 0 {
		return nil
	}
	a.mu.RLock()
	proxy, lifetime, manager := a.proxy, a.ctx, a.native
	a.mu.RUnlock()
	if proxy == nil {
		return nil
	}
	a.nativeWarmMu.Lock()
	if a.nativeWarmCancel != nil {
		a.nativeWarmCancel()
	}
	if lifetime == nil {
		lifetime = context.Background()
	}
	ctx, cancel := context.WithTimeout(lifetime, 2*time.Second)
	a.nativeWarmCancel = cancel
	a.nativeWarmMu.Unlock()
	go func() {
		defer cancel()
		if err := proxy.WarmVideo(ctx, videoID, d.Sources[0].Quality); err == nil && ctx.Err() == nil && manager != nil {
			manager.Preload(proxy.URL(videoID, d.Sources[0].Quality))
		}
	}()
	return nil
}

func (a *App) cancelNativeWarm() {
	a.nativeWarmMu.Lock()
	defer a.nativeWarmMu.Unlock()
	if a.nativeWarmCancel != nil {
		a.nativeWarmCancel()
		a.nativeWarmCancel = nil
	}
}
func (a *App) nativeEvent(event nativedec.Event) {
	a.emitDesktopEvent("nativedec", event)
}
