package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"duanju-desktop/pkg/media"
	"duanju-desktop/pkg/model"
	"duanju-desktop/pkg/store"
)

// 本文件把 pkg/store 与 pkg/media 接进 App 的生命周期。
//
// 设计要点：
//   - 存储与代理都是「能起就起，起不来降级」的可选组件，
//     不让它们的失败阻断 UI 启动
//   - 一旦起来，api_play.go 里的内存兜底就让位给真实实现

// bootstrapStore 打开 SQLite 并跑迁移。
func (a *App) bootstrapStore(dir string) error {
	path := filepath.Join(dir, dataDatabase)
	st, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	a.mu.Lock()
	a.st = st
	a.mu.Unlock()
	return nil
}

// bootstrapMedia 启动本地媒体代理。
//
// 代理地址绑定在 127.0.0.1 的随机端口，起来后把基址写进 a.proxyBase，
// PlayInfo 会把上游地址改写成代理地址。
func (a *App) bootstrapMedia(dir string) error {
	res := &mediaResolver{app: a}
	p := media.NewProxy(filepath.Join(dir, "media"), res)
	if err := p.Start("127.0.0.1:0"); err != nil {
		return fmt.Errorf("启动媒体代理失败: %w", err)
	}
	a.mu.Lock()
	a.proxy = p
	a.proxyBase = "http://" + p.Addr()
	a.mu.Unlock()
	return nil
}

// shutdown 逆序释放资源。
func (a *App) shutdown() {
	a.cancelNativeWarm()
	a.mu.Lock()
	native := a.native
	a.native = nil
	a.mu.Unlock()
	if native != nil {
		native.Shutdown()
	}
	if a.updater != nil {
		a.updater.Close()
	}
	a.playbackMu.Lock()
	if a.playbackCancel != nil {
		a.playbackCancel()
		a.playbackCancel = nil
	}
	a.playbackMu.Unlock()
	if a.playbackPool != nil {
		a.playbackPool.stop()
	}
	a.mu.Lock()
	p := a.proxy
	st := a.st
	a.proxy, a.st = nil, nil
	a.proxyBase = ""
	a.mu.Unlock()

	if p != nil {
		_ = p.Close()
	}
	if st != nil {
		_ = st.Close()
	}
}

// ---------------------------------------------------------------- Resolver

// mediaResolver 把 media.Resolver 接到 hgapi 上。
type mediaResolver struct {
	app *App
}

// ResolveImage 返回图片直链。key 本身是 URL 时直接透传。
func (r *mediaResolver) ResolveImage(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", errors.New("图片 key 为空")
	}
	if hasScheme(key) {
		return key, nil
	}
	// 业务侧传的是 web_uri，拼成 CDN 地址。
	return "https://p3-sign.douyinpic.com/" + key, nil
}

// ResolveVideo 返回视频直链与 Referer。
func (r *mediaResolver) ResolveVideo(ctx context.Context, videoID, quality string) (string, string, error) {
	url, referer, _, err := r.ResolveProtectedVideo(ctx, videoID, quality)
	return url, referer, err
}

func (r *mediaResolver) ResolveProtectedVideo(ctx context.Context, videoID, quality string) (string, string, []byte, error) {
	svc, err := r.app.service()
	if err != nil {
		return "", "", nil, err
	}
	d, err := svc.VideoDetail(ctx, videoID)
	if err != nil {
		return "", "", nil, err
	}
	for _, s := range d.Sources {
		if s.Quality == quality {
			return s.URL, "https://novel.snssdk.com/", s.ContentKey, nil
		}
	}
	if len(d.Sources) > 0 {
		return d.Sources[0].URL, "https://novel.snssdk.com/", d.Sources[0].ContentKey, nil
	}
	return "", "", nil, errors.New("没有可用播放源")
}

func (r *mediaResolver) ResolveVideoCandidates(ctx context.Context, videoID, quality string, refresh bool) ([]media.VideoCandidate, error) {
	svc, err := r.app.service()
	if err != nil {
		return nil, err
	}
	var d *model.VideoDetail
	if refresh {
		d, err = svc.RefreshVideoDetail(ctx, videoID)
	} else {
		d, err = svc.VideoDetail(ctx, videoID)
	}
	if err != nil {
		return nil, err
	}
	return videoCandidates(d, quality), nil
}
func videoCandidates(d *model.VideoDetail, quality string) []media.VideoCandidate {
	if d == nil || len(d.Sources) == 0 {
		return nil
	}
	s := d.Sources[0]
	for _, item := range d.Sources {
		if item.Quality == quality {
			s = item
			break
		}
	}
	list := []media.VideoCandidate{}
	for _, u := range []string{s.URL, s.BackupURL} {
		if u != "" && (len(list) == 0 || list[0].URL != u) {
			list = append(list, media.VideoCandidate{URL: u, Referer: "https://novel.snssdk.com/", Key: s.ContentKey})
		}
	}
	return list
}

// hasScheme 判断字符串是否已带协议头。
func hasScheme(s string) bool {
	for i := 0; i < len(s) && i < 8; i++ {
		if s[i] == ':' {
			return i > 0 && i+2 < len(s) && s[i+1] == '/' && s[i+2] == '/'
		}
	}
	return false
}

// ---------------------------------------------------------------- 存储桥接

// saveProgress 把播放进度写入数据库；未就绪时退回内存。
func (a *App) saveProgress(st PlayState) error {
	localSaveMu.Lock()
	defer localSaveMu.Unlock()
	a.mu.RLock()
	db := a.st
	a.mu.RUnlock()
	if db == nil {
		return errors.New("存储未就绪")
	}
	return db.UpsertProgress(context.Background(), &store.Progress{
		SeriesID:   st.SeriesID,
		Episode:    st.Episode,
		PositionMS: st.PositionMs,
		DurationMS: st.DurationMs,
	})
}
