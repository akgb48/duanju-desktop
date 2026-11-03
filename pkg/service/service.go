// Package service 编排业务用例。
//
// 职责边界：
//   - 缓存的读穿、失效
//   - 登录态失效后的自动降级与重新登录提示
//   - 多源并发拉取与结果合并
//   - 把 hgapi 的领域对象喂给 store / media / 前端
//
// 不负责：HTTP 细节（在 pkg/api）、字段映射（在 pkg/hgapi）、持久化 SQL（在 pkg/store）。
package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/model"
)

// ErrNotLoggedIn 表示当前无登录态，需要引导用户登录。
var ErrNotLoggedIn = errors.New("service: 尚未登录")

// Cache 是服务层用的极简读穿缓存。
//
// 只用于幂等、容忍短暂陈旧的数据（信息流、剧集详情）。
// 进度、评论、账号一律不走缓存。
type Cache struct {
	mu    sync.RWMutex
	items map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	val  any
	born time.Time
}

// NewCache 构造带 ttl 的缓存。
func NewCache(ttl time.Duration) *Cache {
	return &Cache{items: make(map[string]cacheEntry, 128), ttl: ttl}
}

// Get 取缓存；命中且未过期返回 true。
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Since(e.born) > c.ttl {
		return nil, false
	}
	return e.val, true
}

// Put 写缓存。
func (c *Cache) Put(key string, v any) {
	c.mu.Lock()
	c.items[key] = cacheEntry{val: v, born: time.Now()}
	c.mu.Unlock()
}

// Invalidate 按键前缀清空。
func (c *Cache) Invalidate(prefix string) {
	c.mu.Lock()
	for k := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}

// Clear 清空全部缓存。
func (c *Cache) Clear() {
	c.mu.Lock()
	c.items = make(map[string]cacheEntry, 128)
	c.mu.Unlock()
}

// Service 是业务门面。所有方法都可由前端并发调用。
type Service struct {
	hg    *hgapi.Client
	cache *Cache

	mu       sync.RWMutex
	loggedIn bool
	user     *model.User

	// refreshMu 串行化播放地址刷新，避免同一视频并发刷爆上游。
	refreshMu     sync.Mutex
	discoveryGate chan struct{}
}

// New 构造业务门面。
func New(hg *hgapi.Client) *Service {
	return &Service{hg: hg, cache: NewCache(3 * time.Minute), discoveryGate: make(chan struct{}, 1)}
}

// Client 暴露底层 hgapi 客户端（供 app 层做登录等底层操作）。
func (s *Service) Client() *hgapi.Client { return s.hg }

// Cache 暴露缓存（供设置页清理）。
func (s *Service) Cache() *Cache { return s.cache }

// SetLoggedIn 记录登录态。
func (s *Service) SetLoggedIn(v bool, u *model.User) {
	s.mu.Lock()
	s.loggedIn = v
	s.user = u
	s.mu.Unlock()
	if !v {
		s.cache.Clear()
		s.hg.ClearPlaybackModels()
	}
}

// IsLoggedIn 报告是否有登录态。
func (s *Service) IsLoggedIn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loggedIn
}

// CurrentUser 返回缓存的用户资料。
func (s *Service) CurrentUser() *model.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.user == nil {
		return nil
	}
	cp := *s.user
	return &cp
}

// requireLogin 在需要登录态的用例前做门禁。
func (s *Service) requireLogin() error {
	if !s.IsLoggedIn() {
		return ErrNotLoggedIn
	}
	return nil
}

// Feed 首页信息流（带缓存）。
func (s *Service) Feed(ctx context.Context, tabType int, cursor string) (*model.FeedPage, error) {
	key := "feed:" + itoa(tabType) + ":" + cursor
	if v, ok := s.cache.Get(key); ok {
		if p, ok := v.(*model.FeedPage); ok {
			return p, nil
		}
	}
	p, err := s.hg.Feed(ctx, tabType, cursor)
	if err != nil {
		return nil, err
	}
	s.cache.Put(key, p)
	return p, nil
}

// FeedTabs 返回 feed 的真实页签列表（带缓存）。
func (s *Service) FeedTabs(ctx context.Context) ([]hgapi.Tab, error) {
	const key = "feed:tabs"
	if v, ok := s.cache.Get(key); ok {
		if t, ok := v.([]hgapi.Tab); ok {
			return t, nil
		}
	}
	t, err := s.hg.FeedTabs(ctx)
	if err != nil {
		return nil, err
	}
	s.cache.Put(key, t)
	return t, nil
}

// Rank 拉榜单（rank_change，带缓存）。
//
// cellID 默认用服务端推荐榜的区块 id；不同 rankType 换不同区块。
func (s *Service) Rank(ctx context.Context, rankType int, cursor string) (*model.FeedPage, error) {
	key := "rank:" + itoa(rankType) + ":" + cursor
	if v, ok := s.cache.Get(key); ok {
		if p, ok := v.(*model.FeedPage); ok {
			return p, nil
		}
	}
	filters := map[string]string{}
	if rankType > 0 {
		filters["rank"] = strconv.Itoa(rankType)
	}
	p, err := s.hg.RankSelected(ctx, filters, cursor)
	if err != nil {
		return nil, err
	}
	s.cache.Put(key, p)
	return p, nil
}

// NewSeries 拉新剧（new_series，带缓存）。
func (s *Service) NewSeries(ctx context.Context, cursor string) (*model.FeedPage, error) {
	return s.NewSeriesForGender(ctx, "2", cursor)
}

func (s *Service) NewSeriesForGender(ctx context.Context, gender, cursor string) (*model.FeedPage, error) {
	key := "new:" + gender + ":" + cursor
	if v, ok := s.cache.Get(key); ok {
		if p, ok := v.(*model.FeedPage); ok {
			return p, nil
		}
	}
	p, err := s.hg.NewSeriesPage(ctx, gender, cursor)
	if err != nil {
		return nil, err
	}
	s.cache.Put(key, p)
	return p, nil
}

// Search 搜索（带缓存）。
//
// Cache each keyword/continuation independently. Empty results remain distinct
// from errors and never fall back to filtering a recommendation page.
func (s *Service) Search(ctx context.Context, keyword, cursor string) (*model.SearchResult, error) {
	key := "search:" + keyword + ":" + cursor
	if v, ok := s.cache.Get(key); ok {
		if r, ok := v.(*model.SearchResult); ok {
			return r, nil
		}
	}
	r, err := s.hg.Search(ctx, keyword, cursor)
	if err != nil {
		return nil, err
	}
	if len(r.Series) > 0 {
		s.cache.Put(key, r)
	}
	return r, nil
}

// VideoDetail 单集详情（带缓存）。
func (s *Service) VideoDetail(ctx context.Context, videoID string) (*model.VideoDetail, error) {
	key := "vd:" + videoID
	if v, ok := s.cache.Get(key); ok {
		if d, ok := v.(*model.VideoDetail); ok && !expired(d.ExpireAt) {
			return d, nil
		}
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	d, err := s.hg.VideoDetail(ctx, videoID)
	if err != nil {
		return nil, err
	}
	s.cache.Put(key, d)
	return d, nil
}

// Comments 评论/弹幕列表。
// RefreshVideoDetail bypasses a still-unexpired CDN address after all routes fail.
func (s *Service) RefreshVideoDetail(ctx context.Context, videoID string) (*model.VideoDetail, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	d, err := s.hg.RefreshVideoDetail(ctx, videoID)
	if err != nil {
		return nil, err
	}
	s.cache.Put("vd:"+videoID, d)
	return d, nil
}

// Comments 评论/弹幕列表。
func (s *Service) Comments(ctx context.Context, seriesID, videoID string, dataType int, cursor string) (*model.CommentPage, error) {
	return s.hg.Comments(ctx, seriesID, videoID, dataType, cursor)
}

// AddComment 发评论或弹幕。
func (s *Service) AddComment(ctx context.Context, seriesID, videoID, text string, dataType, offsetMs int) (string, error) {
	if err := s.requireLogin(); err != nil {
		return "", err
	}
	id, err := s.hg.AddComment(ctx, seriesID, videoID, text, dataType, offsetMs)
	if err != nil {
		return "", err
	}
	s.cache.Invalidate("cm:" + videoID)
	return id, nil
}

// RefreshUser 拉取并缓存用户资料。
func (s *Service) RefreshUser(ctx context.Context) (*model.User, error) {
	if err := s.requireLogin(); err != nil {
		return nil, err
	}
	u, err := s.hg.UserInfo(ctx)
	if err != nil {
		return nil, err
	}
	s.SetLoggedIn(true, u)
	return u, nil
}

// UploadPicture 上传评论配图。入参是 base64 编码的图片数据。
func (s *Service) UploadPicture(ctx context.Context, base64Data string) (string, error) {
	if err := s.requireLogin(); err != nil {
		return "", err
	}
	if base64Data == "" {
		return "", fmt.Errorf("图片数据为空")
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("图片数据不是合法 base64: %w", err)
	}
	if len(data) > maxUploadBytes {
		return "", fmt.Errorf("图片超过 %d MB 上限", maxUploadBytes>>20)
	}
	return s.hg.UploadPicture(ctx, "comment.jpg", data)
}

// maxUploadBytes 是评论配图的大小上限。
const maxUploadBytes = 5 << 20

// expired 判断播放地址是否过期（提前 60 秒判定，留出刷新窗口）。
func expired(expireAt int64) bool {
	if expireAt <= 0 {
		return false
	}
	return time.Now().Unix() > expireAt-60
}

// itoa 避免为单个整数引入 strconv 到热路径。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
