package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DownloadItem 是一条下载任务的前端视图。
type DownloadItem struct {
	ID         string `json:"id"`
	SeriesID   string `json:"seriesId"`
	VideoID    string `json:"videoId"`
	Title      string `json:"title"`
	Cover      string `json:"cover"`
	Episode    int    `json:"episode"`
	Quality    string `json:"quality"`
	State      string `json:"state"`    // pending|running|paused|done|failed
	Progress   int    `json:"progress"` // 0-100
	TotalBytes int64  `json:"totalBytes"`
	DoneBytes  int64  `json:"doneBytes"`
	Speed      int64  `json:"speed"`
	FilePath   string `json:"filePath"`
	Error      string `json:"error"`
	CreatedAt  int64  `json:"createdAt"`
}

// ---------------------------------------------------------------- 下载（7）

// Downloads 返回全部下载任务。
func (a *App) Downloads() ([]DownloadItem, error) {
	m, err := a.downloadManager()
	if err != nil {
		return nil, err
	}
	return m.list(), nil
}

// AddDownloads 把某剧的一批分集加入下载队列。
func (a *App) AddDownloads(seriesID string, from, to int) (int, error) {
	if seriesID == "" {
		return 0, errors.New("缺少剧集 ID")
	}
	if from < 1 {
		from = 1
	}
	eps, err := a.episodes(seriesID)
	if err != nil {
		return 0, err
	}
	if to <= 0 || to > len(eps) {
		to = len(eps)
	}
	if from > to {
		return 0, fmt.Errorf("分集范围无效：%d-%d", from, to)
	}

	s := a.currentSettings()
	m, err := a.downloadManager()
	if err != nil {
		return 0, err
	}
	dir := s.DownloadDir
	if dir == "" {
		dir = a.downloadDir()
	}
	var tasks []DownloadItem
	for i := from; i <= to; i++ {
		id := fmt.Sprintf("%s#%d", seriesID, i)
		if eps[i-1].VideoID == "" {
			return 0, fmt.Errorf("第 %d 集没有可用的视频 ID", i)
		}
		tasks = append(tasks, DownloadItem{
			ID:        id,
			SeriesID:  seriesID,
			VideoID:   eps[i-1].VideoID,
			Title:     eps[i-1].Title,
			Cover:     eps[i-1].Cover,
			Episode:   i,
			Quality:   s.DefaultQuality,
			State:     "pending",
			FilePath:  filepath.Join(dir, safeDownloadName(seriesID), fmt.Sprintf("%03d-%s.mp4", i, safeDownloadName(eps[i-1].VideoID))),
			CreatedAt: time.Now().Unix(),
		})
	}
	return m.add(tasks)
}

// DeleteDownloads 删除下载任务及其文件。
func (a *App) DeleteDownloads(ids []string) error {
	m, err := a.downloadManager()
	if err != nil {
		return err
	}
	return m.remove(ids)
}

// PauseDownloads 暂停一批任务。
func (a *App) PauseDownloads(ids []string) error {
	m, err := a.downloadManager()
	if err != nil {
		return err
	}
	return m.pause(ids)
}

// ResumeDownloads 恢复一批任务。
func (a *App) ResumeDownloads(ids []string) error {
	m, err := a.downloadManager()
	if err != nil {
		return err
	}
	return m.resume(ids)
}

// DownloadSeries 下载整部剧。
func (a *App) DownloadSeries(seriesID string) (int, error) {
	return a.AddDownloads(seriesID, 1, 0)
}

// OpenDownloadDir 在资源管理器中打开下载目录。
func (a *App) OpenDownloadDir(id string) error {
	dir := a.downloadDir()
	if id != "" {
		m, err := a.downloadManager()
		if err != nil {
			return err
		}
		for _, it := range m.list() {
			if it.ID == id && it.FilePath != "" {
				dir = filepath.Dir(it.FilePath)
				break
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openPath(dir)
}
