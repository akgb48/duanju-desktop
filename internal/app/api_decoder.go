package app

import "errors"

// HEVCDecodeStatus 是 HEVC 解码能力快照。
type HEVCDecodeStatus struct {
	Supported     bool   `json:"supported"`
	Backend       string `json:"backend"`
	CPUFallback   bool   `json:"cpuFallback"`
	InstallerPath string `json:"installerPath"`
}

// HEVCStatus 返回 HEVC 解码能力。
func (a *App) HEVCStatus() (*HEVCDecodeStatus, error) {
	return &HEVCDecodeStatus{
		Supported:   false,
		Backend:     "software",
		CPUFallback: true,
	}, nil
}

// HEVCDiag 返回 HEVC 诊断详情。
func (a *App) HEVCDiag() (any, error) {
	return map[string]any{
		"wasmAvailable": false,
		"reason":        "本构建未内嵌 WASM 解码器",
	}, nil
}

// RevealHEVCInstaller 在资源管理器中定位解码器安装包。
func (a *App) RevealHEVCInstaller() error {
	return errors.New("本构建未内嵌解码器安装包")
}

// NativeDecOpen 打开一个原生解码会话，返回会话 ID。
func (a *App) NativeDecOpen(videoID string) (string, error) {
	return "", errors.New("原生解码未启用")
}

// NativeDecPrepare 预解析关键帧。
func (a *App) NativeDecPrepare(sessionID string, positionMs int64) error {
	return errors.New("原生解码未启用")
}

// NativeDecSeek 定位到指定时间。
func (a *App) NativeDecSeek(sessionID string, positionMs int64) error {
	return errors.New("原生解码未启用")
}

// NativeDecPreload 预加载下一段。
func (a *App) NativeDecPreload(sessionID string) error {
	return errors.New("原生解码未启用")
}

// NativeDecClose 关闭会话。
func (a *App) NativeDecClose(sessionID string) error {
	return nil
}

// NativeDecSetCPUOnly 切换纯软解。
func (a *App) NativeDecSetCPUOnly(enabled bool) error {
	return nil
}

// NativeDecStatus 返回解码会话状态。
func (a *App) NativeDecStatus(sessionID string) (any, error) {
	return map[string]any{"session": sessionID, "state": "closed"}, nil
}
