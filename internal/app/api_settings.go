package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"duanju-desktop/pkg/model"
)

// BossToggle 切换老板键隐身状态。
func (a *App) BossToggle() (bool, error) {
	a.mu.Lock()
	a.stealth = !a.stealth
	on := a.stealth
	a.mu.Unlock()
	return on, nil
}

// DismissDataDirNotice 标记数据目录提示已读。
func (a *App) DismissDataDirNotice() error {
	a.mu.Lock()
	a.dataDirNoticeDone = true
	a.mu.Unlock()
	return nil
}

// DataDirInfo 是数据目录信息。
type DataDirInfo struct {
	Dir        string `json:"dir"`
	DefaultDir string `json:"defaultDir"`
	IsDefault  bool   `json:"isDefault"`
	SizeBytes  int64  `json:"sizeBytes"`
	FileCount  int    `json:"fileCount"`
	Writable   bool   `json:"writable"`
	NoticeRead bool   `json:"noticeRead"`
}

// GetDataDirInfo 返回数据目录信息。前端同时读 dir/dataDir，所以两个字段都给。
func (a *App) GetDataDirInfo() (*DataDirInfo, error) {
	a.mu.RLock()
	dir := a.dataDir
	defaultDir := a.defaultDataDir
	notice := a.dataDirNoticeDone
	a.mu.RUnlock()

	relative, _ := filepath.Rel(defaultDir, dir)
	info := &DataDirInfo{Dir: dir, DefaultDir: defaultDir, IsDefault: relative == ".", NoticeRead: notice, Writable: true}
	var size int64
	var count int
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		size += fi.Size()
		count++
		return nil
	})
	info.SizeBytes = size
	info.FileCount = count
	return info, nil
}

// KeepAwake 阻止系统休眠。
func (a *App) KeepAwake(on bool) error {
	a.mu.Lock()
	a.keepAwake = on
	a.mu.Unlock()
	return nil
}

// PickDataDir 在桌面端打开目录选择框，浏览器模式返回默认候选目录。
func (a *App) PickDataDir() (string, error) {
	a.mu.RLock()
	ctx, dir, webMode := a.ctx, a.dataDir, a.webMode
	a.mu.RUnlock()
	if ctx != nil && !webMode {
		return runtimePickDataDir(ctx, dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hgplayer-data"), nil
}

// RemoteConfig 返回服务端下发的远端配置。
func (a *App) RemoteConfig() (map[string]any, error) {
	return map[string]any{
		"updateHost":      UpdateHost,
		"mediaProxyStyle": "same-origin",
		"features": map[string]bool{
			"danmaku": true, "download": true, "hevc": false,
		},
	}, nil
}

// RestoreMiniWindow 从迷你模式恢复。
func (a *App) RestoreMiniWindow() error {
	a.mu.Lock()
	a.windowMode = "normal"
	a.mu.Unlock()
	return nil
}

// SetBossKey 设置老板键。
func (a *App) SetBossKey(key string) error {
	if key == "" {
		return errors.New("快捷键不能为空")
	}
	a.mu.Lock()
	a.bossKey = key
	a.mu.Unlock()
	return nil
}

// SetFullscreenLook 设置全屏观感。
func (a *App) SetFullscreenLook(look string) error {
	a.mu.Lock()
	a.settings.FullscreenLook = look
	a.mu.Unlock()
	return nil
}

// SetPlaybackPrefs 保存播放偏好（JSON 字符串）。
func (a *App) SetPlaybackPrefs(jsonPrefs string) error {
	var prefs model.Settings
	if err := json.Unmarshal([]byte(jsonPrefs), &prefs); err != nil {
		return fmt.Errorf("播放偏好格式错误: %w", err)
	}
	return a.saveSettings(prefs)
}

// SetWindowButtonsVisible 切换标题栏按钮可见性。
func (a *App) SetWindowButtonsVisible(on bool) error {
	a.mu.Lock()
	a.buttonsVisible = on
	a.mu.Unlock()
	return nil
}

// SetWindowMode 切换窗口模式：normal | mini | fullscreen。
func (a *App) SetWindowMode(mode string) error {
	switch mode {
	case "normal", "mini", "fullscreen":
	default:
		return fmt.Errorf("未知窗口模式: %s", mode)
	}
	a.mu.Lock()
	a.windowMode = mode
	a.mu.Unlock()
	return nil
}

// StealthStart 开始隐身（隐藏窗口与托盘）。
func (a *App) StealthStart() error {
	a.mu.Lock()
	a.stealth = true
	a.mu.Unlock()
	return nil
}

// StealthStop 退出隐身。
func (a *App) StealthStop() error {
	a.mu.Lock()
	a.stealth = false
	a.mu.Unlock()
	return nil
}
