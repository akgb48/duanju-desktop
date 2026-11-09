package app

import (
	"context"
	"duanju-desktop/pkg/hgapi"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"duanju-desktop/pkg/model"
)

// 本文件是内容侧绑定方法：信息流、详情、搜索、评论。
// 每个方法对应前端 bridge.js 里的一个包装函数。

// feedPage 拉取首页信息流。tabType: 0=推荐 1=追剧 2=榜单。
func (a *App) feedPage(tabType int, cursor string) (*model.FeedPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Recommend(ctx, tabType, cursor)
}

// rankPage 拉取榜单。
//
// 走真实接口 rank_change（/reading/bookapi/bookmall/cell/change/v），
// 早期版本把它映射成 feed 的伪 tab_type 100+rankType，那是占位实现，
// 拿到的其实是推荐流。
func (a *App) rankPage(rankType int, cursor string) (*model.FeedPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Rank(ctx, rankType, cursor)
}

// GetCategory 拉取分类落地页。
func (a *App) categoryPage(genre string, offset int) (*model.FeedPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Feed(ctx, 200, fmt.Sprintf("%s:%d", genre, offset))
}

// GetVideoDetail 拉取单集播放详情。
func (a *App) videoDetail(videoID string) (*model.VideoDetail, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.VideoDetail(ctx, videoID)
}

// GetSeriesDetail 拉取剧集详情（含分集列表）。
func (a *App) seriesDetail(seriesID string) (*model.Series, error) {
	ctx, cancel := a.callCtx()
	defer cancel()
	return a.seriesDetailWithContext(ctx, seriesID)
}

func (a *App) seriesDetailWithContext(ctx context.Context, seriesID string) (*model.Series, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	s, err := svc.Client().SeriesDetail(ctx, seriesID)
	if err == nil && s != nil {
		if len(s.Episodes) > 0 {
			svc.Cache().Put(playbackEpisodesPrefix+seriesID, append([]model.Episode(nil), s.Episodes...))
		}
		a.rememberSeries(*s)
	}
	return s, err
}

// GetEpisodes 拉取分集列表。
func (a *App) episodes(seriesID string) ([]model.Episode, error) {
	detail, err := a.seriesDetail(seriesID)
	if err != nil {
		return nil, err
	}
	return detail.Episodes, nil
}

// Search preserves the upstream continuation and distinguishes an empty
// result from a network failure. A recommendation filter is not a search page.
func (a *App) Search(keyword, cursor string) (*model.SearchResult, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()

	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return &model.SearchResult{Keyword: "", Series: []model.Series{}, Source: "server"}, nil
	}
	return svc.Search(ctx, keyword, cursor)
}

// SearchSuggest 返回搜索联想词。
func (a *App) SearchSuggest(keyword string) ([]model.SearchSuggestion, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().SearchSuggest(ctx, strings.TrimSpace(keyword))
}

// commentsPage 拉取评论列表（内部，dataType 固定 4）。
func (a *App) commentsPage(seriesID, videoID string, cursor string) (*model.CommentPage, error) {
	return a.getComments(seriesID, videoID, 4, cursor)
}

// danmakuPage 拉取弹幕列表。
func (a *App) danmakuPage(seriesID, videoID string) (*model.CommentPage, error) {
	return a.getComments(seriesID, videoID, 20, "")
}

// getComments 是评论族的底层实现，按 dataType 区分评论与弹幕。
func (a *App) getComments(seriesID, videoID string, dataType int, cursor string) (*model.CommentPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Comments(ctx, seriesID, videoID, dataType, cursor)
}

// PostComment 发评论。
func (a *App) postComment(seriesID, videoID, text string) (string, error) {
	svc, err := a.service()
	if err != nil {
		return "", err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.AddComment(ctx, seriesID, videoID, text, 4, 0)
}

// PostDanmaku 发弹幕，offsetMs 为出现时间。
func (a *App) postDanmaku(seriesID, videoID, text string, offsetMs int) (string, error) {
	svc, err := a.service()
	if err != nil {
		return "", err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.AddComment(ctx, seriesID, videoID, text, 20, offsetMs)
}

// RecommendTab 返回首页推荐页签。
func (a *App) RecommendTab(tabType int, cursor string) (*model.FeedPage, error) {
	return a.feedPage(tabType, cursor)
}

// FeedCategory 是一个内容分类。
type FeedCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FeedCategories 返回全部分类。
// FeedCategories 返回 feed 里真实存在的页签。
//
// 早期版本返回硬编码的中文分类名（甜宠/复仇/悬疑…），那是编的。
// 真实页签由服务端在 tab_item[].title + tab_type 给出，这里直接读上游。
// 上游不可用时退回一个可用的「推荐」页签，保证界面不空。
func (a *App) FeedCategories() ([]FeedCategory, error) {
	svc, err := a.service()
	if err != nil {
		return []FeedCategory{{ID: "0", Name: "推荐"}}, nil
	}
	ctx, cancel := a.callCtx()
	defer cancel()

	tabs, err := svc.FeedTabs(ctx)
	if err != nil || len(tabs) == 0 {
		return []FeedCategory{{ID: "0", Name: "推荐"}}, nil
	}
	out := make([]FeedCategory, 0, len(tabs))
	for _, t := range tabs {
		name := t.Title
		if name == "" {
			name = "推荐"
		}
		out = append(out, FeedCategory{ID: strconv.Itoa(t.Type), Name: name})
	}
	return out, nil
}

// Category 拉取分类内容。
//
// genre 接受 JSON 筛选条件或单个体裁；cursor 是带会话的服务端续接信息。
func (a *App) Category(genre, cursor string) (*model.FeedPage, error) {
	filters := map[string]any{}
	if strings.HasPrefix(strings.TrimSpace(genre), "{") {
		if err := json.Unmarshal([]byte(genre), &filters); err != nil {
			return nil, fmt.Errorf("筛选条件格式错误: %w", err)
		}
	} else if genre != "" {
		filters["genre"] = []string{genre}
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Category(ctx, filters, cursor)
}

// SelectorPanel 返回分类筛选面板配置。
func (a *App) SelectorPanel(genre string) (any, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().SelectorPanel(ctx, genre)
}

// Rank 拉取榜单。
func (a *App) Rank(selection string, cursor string) (*model.FeedPage, error) {
	filters := map[string]string{}
	if strings.HasPrefix(strings.TrimSpace(selection), "{") {
		if err := json.Unmarshal([]byte(selection), &filters); err != nil {
			return nil, fmt.Errorf("榜单筛选条件错误: %w", err)
		}
	} else if selection != "" && selection != "0" {
		filters["rank"] = selection
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().RankSelected(ctx, filters, cursor)
}

// RankPanel 返回榜单页签配置。
func (a *App) RankPanel() (any, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().RankPanel(ctx)
}

// NewSeries 拉取新剧。
// NewSeries 拉新剧。
//
// 走真实接口 new_series（/reading/bookapi/bookmall/cell/change/v1/）。
// genre 在这里当游标用（前端传空串表示第一页）。
func (a *App) NewSeries(genre string) (*model.FeedPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	gender, cursor := "2", genre
	if strings.HasPrefix(strings.TrimSpace(genre), "{") {
		var query struct {
			Gender string `json:"gender"`
			Cursor string `json:"cursor"`
		}
		if err = json.Unmarshal([]byte(genre), &query); err != nil {
			return nil, fmt.Errorf("新剧筛选条件错误: %w", err)
		}
		gender, cursor = query.Gender, query.Cursor
	}
	return svc.NewSeriesForGender(ctx, gender, cursor)
}

// Search 与 SearchSuggest 实现在 api_content.go。

// SeriesDetail 返回剧集详情。
func (a *App) SeriesDetail(seriesID string) (*model.Series, error) {
	detail, err := a.seriesDetail(seriesID)
	return detail, playbackRecoveryError(err)
}

// SeriesSeasons 返回季列表。
func (a *App) SeriesSeasons(seriesID string) ([]hgapi.SeriesSeason, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().SeriesSeasons(ctx, seriesID)
}

// NextSeason 返回下一季。
func (a *App) NextSeason(seriesID string, index int) (*hgapi.SeasonRef, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().NextSeason(ctx, seriesID, index)
}

// Related 返回相关推荐。
func (a *App) Related(seriesID string) (*model.RelatedSeries, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Related(ctx, seriesID)
}
