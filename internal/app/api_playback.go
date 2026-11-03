package app

import (
	"fmt"

	"hongguo-desktop/pkg/hgapi"
	"hongguo-desktop/pkg/model"
)

// PlayInfo 返回某一集的播放信息，优先走本地代理地址。
func (a *App) PlayInfo(seriesID string, episode int) (*model.VideoDetail, error) {
	a.startPlaybackPool()
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()

	eps, err := a.episodes(seriesID)
	if err != nil {
		return nil, playbackRecoveryError(fmt.Errorf("读取剧集 %s 的分集失败: %w", seriesID, err))
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("未找到剧集 %s 的分集", seriesID)
	}
	if episode < 1 || episode > len(eps) {
		episode = 1
	}
	ep := eps[episode-1]
	// Load an aligned window, so changing episodes reuses one model request
	// instead of fetching a new sliding batch for every episode.
	start := ((episode - 1) / hgapi.PlaybackBatchSize) * hgapi.PlaybackBatchSize
	if start+hgapi.PlaybackBatchSize > len(eps) {
		start = len(eps) - hgapi.PlaybackBatchSize
		if start < 0 {
			start = 0
		}
	}
	end := start + hgapi.PlaybackBatchSize
	if end > len(eps) {
		end = len(eps)
	}
	ids := make([]string, 0, end-start)
	for _, item := range eps[start:end] {
		ids = append(ids, item.VideoID)
	}
	if err := svc.Client().EnsurePlaybackWindow(ctx, ep.VideoID, ids); err != nil {
		return nil, playbackRecoveryError(err)
	}

	d, err := svc.VideoDetail(ctx, ep.VideoID)
	if err != nil {
		return nil, playbackRecoveryError(err)
	}
	// Service owns the cached remote source list. Rewrite only this view copy;
	// mutating it would make the proxy resolve its own URL recursively.
	view := *d
	view.Sources = append([]model.PlaySource(nil), d.Sources...)
	d = &view
	d.SeriesID = seriesID
	d.Episode = episode

	// 若本地代理已就绪，把源地址换成代理地址，绕开防盗链与跨域。
	a.mu.RLock()
	proxyURL := a.proxyBase
	a.mu.RUnlock()
	if proxyURL != "" {
		for i := range d.Sources {
			d.Sources[i].URL = proxyURL + "/media/" + ep.VideoID + "/" + d.Sources[i].Quality
		}
	}
	return d, nil
}

func (a *App) prefetchVideoModels(videoIDs []string) error {
	if len(videoIDs) < 2 {
		return nil
	}
	a.startPlaybackPool()
	svc, err := a.service()
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().PrefetchVideoModels(ctx, videoIDs)
}

func (a *App) PrefetchPlay(videoIDs []string) error {
	return a.prefetchVideoModels(videoIDs)
}

// PrefetchPlayItems 批量预取。
func (a *App) PrefetchPlayItems(seriesID string, episode int) error {
	_, err := a.PlayInfo(seriesID, episode)
	return err
}

// SaveProgress 保存播放进度。
func (a *App) SaveProgress(seriesID, videoID string, episode int, positionMs, durationMs int64) error {
	return a.reportProgress(PlayState{
		SeriesID: seriesID, VideoID: videoID, Episode: episode,
		PositionMs: positionMs, DurationMs: durationMs,
	})
}

// History 返回观看历史。
func (a *App) History(cursor string) ([]model.HistoryEntry, error) {
	return a.playHistory(0)
}

// DeleteHistory 删除一条或全部历史。seriesID 为空表示全清。
func (a *App) DeleteHistory(seriesID string) error {
	if seriesID == "" {
		return a.clearPlayHistory()
	}
	memMu.Lock()
	delete(memHistory, seriesID)
	delete(memProgress, seriesID)
	memMu.Unlock()
	return a.persistLocal()
}

// LocalPending 返回本地待同步的进度条目。
func (a *App) LocalPending() ([]PlayState, error) {
	memMu.RLock()
	defer memMu.RUnlock()
	out := make([]PlayState, 0, len(memProgress))
	for _, v := range memProgress {
		out = append(out, v)
	}
	return out, nil
}

// LocalPlayDiagnosis 是本地播放诊断结果。
type LocalPlayDiagnosis struct {
	ProxyReady bool     `json:"proxyReady"`
	ProxyAddr  string   `json:"proxyAddr"`
	Issues     []string `json:"issues"`
}

// DiagnoseLocalPlay 诊断本地播放链路。
func (a *App) DiagnoseLocalPlay() (*LocalPlayDiagnosis, error) {
	a.mu.RLock()
	addr := a.proxyBase
	a.mu.RUnlock()
	d := &LocalPlayDiagnosis{ProxyReady: addr != "", ProxyAddr: addr}
	if addr == "" {
		d.Issues = append(d.Issues, "本地媒体代理未启动")
	}
	return d, nil
}
