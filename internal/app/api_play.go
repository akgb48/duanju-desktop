package app

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"duanju-desktop/pkg/model"
)

// 本文件是播放、收藏、历史、设置的绑定方法。
//
// 状态优先使用本地持久化存储；不可用时保留内存回退。

// PlayState 是上报给前端的播放态。
type PlayState struct {
	SeriesID   string `json:"seriesId"`
	VideoID    string `json:"videoId"`
	Episode    int    `json:"episode"`
	PositionMs int64  `json:"positionMs"`
	DurationMs int64  `json:"durationMs"`
}

// 内存态兜底存储。
var (
	memMu           sync.RWMutex
	memProgress     = map[string]PlayState{}
	memFavorites    = map[string]model.Series{}
	memSettings     = model.DefaultSettings()
	memHistory      = map[string]model.HistoryEntry{}
	memReservations = map[string]model.Series{}
)

// reportProgress 上报播放进度。
//
// 双写：先落本地库（保证断网也不丢），再上报服务端（失败只提示不阻断）。
func (a *App) reportProgress(st PlayState) error {
	if st.SeriesID == "" {
		return errors.New("缺少剧集 ID")
	}
	memMu.Lock()
	memProgress[st.SeriesID] = st
	memHistory[st.SeriesID] = model.HistoryEntry{
		SeriesID:   st.SeriesID,
		Episode:    st.Episode,
		PositionMs: st.PositionMs,
		DurationMs: st.DurationMs,
		UpdatedAt:  time.Now().Unix(),
	}
	if s, ok := memSeries[st.SeriesID]; ok {
		h := memHistory[st.SeriesID]
		h.Title = s.Title
		h.Cover = s.Cover
		memHistory[st.SeriesID] = h
	}
	memMu.Unlock()

	// 本地库是权威来源；内存态只用于界面即时反馈。
	if err := a.saveProgress(st); err != nil {
		return err
	}
	if err := a.persistLocal(); err != nil {
		return err
	}
	if svc, err := a.service(); err != nil || !svc.IsLoggedIn() {
		return nil
	}

	sess, err := a.session()
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	_, err = sess.CallJSON(ctx, "read_progress_upload", map[string]any{
		"book_id":     st.SeriesID,
		"video_id":    st.VideoID,
		"episode":     st.Episode,
		"offset":      st.PositionMs,
		"duration":    st.DurationMs,
		"is_finished": finished(st.PositionMs, st.DurationMs),
	})
	// 上报失败不阻断播放，前端只提示。
	return err
}

func finished(pos, dur int64) int {
	if dur > 0 && pos*100/dur >= 95 {
		return 1
	}
	return 0
}

// GetPlayHistory 返回观看历史，按更新时间倒序。
func (a *App) playHistory(limit int) ([]model.HistoryEntry, error) {
	memMu.RLock()
	defer memMu.RUnlock()
	out := make([]model.HistoryEntry, 0, len(memHistory))
	for _, v := range memHistory {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClearPlayHistory 清空观看历史。
func (a *App) clearPlayHistory() error {
	memMu.Lock()
	memHistory = map[string]model.HistoryEntry{}
	memProgress = map[string]PlayState{}
	memMu.Unlock()
	return a.persistLocal()
}

// GetFavorites 返回追剧书架。
func (a *App) favoritesList() ([]model.Series, error) {
	memMu.RLock()
	defer memMu.RUnlock()
	out := make([]model.Series, 0, len(memFavorites))
	for _, v := range memFavorites {
		out = append(out, v)
	}
	return out, nil
}

// IsFavorite 查询是否已追。
func (a *App) isFavorite(seriesID string) bool {
	memMu.RLock()
	defer memMu.RUnlock()
	_, ok := memFavorites[seriesID]
	return ok
}

// GetSettings 返回当前设置。
func (a *App) currentSettings() model.Settings {
	memMu.RLock()
	defer memMu.RUnlock()
	return memSettings
}

// SaveSettings 保存设置。
func (a *App) saveSettings(s model.Settings) error {
	memMu.Lock()
	memSettings = s
	memMu.Unlock()
	if err := a.persistLocal(); err != nil {
		return err
	}

	sess, err := a.session()
	if err != nil {
		return err
	}
	sess.SetProxy(s.ProxyURL)
	return nil
}

// GetDownloadDir 返回默认下载目录。
func (a *App) downloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads", "hgplayer")
}

// PickDownloadDir 让用户选目录（Wails 运行时对话框由前端触发，这里只做回落）。
func (a *App) pickDownloadDir() (string, error) {
	return a.downloadDir(), nil
}

// ClearCache 清空服务层缓存。
func (a *App) ClearCache() error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	svc.Cache().Clear()
	svc.Client().ClearPlaybackModels()
	return nil
}
