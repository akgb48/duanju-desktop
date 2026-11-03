// Package hgapi 把上游 JSON 响应映射成 model 领域对象。
//
// 分层约定：
//   - pkg/api    只负责「发请求 + 拆信封」，不认识业务字段
//   - pkg/hgapi  负责「把 data 段解成 model」，是唯一的字段映射层
//   - pkg/service 负责编排（缓存、鉴权重试、降级、并发）
package hgapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"hongguo-desktop/pkg/api"
	"hongguo-desktop/pkg/model"
)

// Client 是业务接口客户端。
type Client struct {
	sess         *api.Session
	contentTypes sync.Map // video ID -> content type from series metadata
	playback     playbackRoutes
	models       playbackModelCache
}

// New 用已有会话构造客户端。
func New(sess *api.Session) *Client {
	return &Client{sess: sess, models: playbackModelCache{fetchGate: make(chan struct{}, 1)}}
}

// Session 返回底层会话。
func (c *Client) Session() *api.Session { return c.sess }

// ---------------------------------------------------------------- 原始响应

// rawFeed 对应 feed 接口的 data 段。
//
// 真实结构（实测 511,894 字节）：
//
//	{
//	  "tab_index": 0,
//	  "cold_start_info": {...},
//	  "tab_item": [
//	    {
//	      "title": "小说", "tab_type": 2, "has_more": true, "next_offset": "...",
//	      "cell_data": [
//	        { "cell_name": "排行榜", "show_type": 156,
//	          "cell_data": [ { "book_data": [ {book}, ... ] }, ... ] }
//	      ]
//	    }
//	  ]
//	}
//
// 关键点：书信息埋在 tab_item[].cell_data[].cell_data[].book_data[] 四层里，
// 且 book_data 是数组。早期把它当成 cells[].books[] 所以永远解析出 0 条。
type rawFeed struct {
	TabIndex int      `json:"tab_index"`
	TabItem  []rawTab `json:"tab_item"`
}

// rawTab 是一个页签。
type rawTab struct {
	Title      string    `json:"title"`
	TabType    int       `json:"tab_type"`
	HasMore    flexBool  `json:"has_more"`
	NextOffset flexStr   `json:"next_offset"`
	SessionID  string    `json:"session_id"`
	Cells      []rawCell `json:"cell_data"`
}

// rawCell 是一个区块。cell_data 在这一层是「子区块列表」，
// 每个子区块里再带 book_data 数组。
type rawCell struct {
	CellID   json.RawMessage `json:"cell_id"`
	CellName string          `json:"cell_name"`
	ShowType int             `json:"show_type"`
	CellType int             `json:"cell_operation_type"`
	Abstract string          `json:"cell_abstract"`
	Books    []rawBook       `json:"book_data"`
	SubCells []rawCell       `json:"cell_data"`
}

// rawBook 是书/剧条目。
type rawBook struct {
	BookID     json.RawMessage `json:"book_id"`
	BookName   string          `json:"book_name"`
	Abstract   string          `json:"abstract"`
	Author     string          `json:"author"`
	Category   string          `json:"category"`
	CoverURL   string          `json:"thumb_url"`
	AudioCover string          `json:"audio_thumb_uri"`
	ExpandCov  string          `json:"expand_thumb_url"`
	HorizCover string          `json:"horiz_thumb_url"`
	ReadCount  flexInt         `json:"read_count"`
	ReadText   string          `json:"read_cnt_text"`
	ListenCnt  flexInt         `json:"listen_count"`
	ChapterNum flexInt         `json:"chapter_number"`
	SerialCnt  flexInt         `json:"serial_count"`
	Score      flexFloat       `json:"score"`
	BookType   flexInt         `json:"book_type"`
	CreationSt flexInt         `json:"creation_status"`
	LastPub    flexInt         `json:"last_publish_time"`
	Tags       string          `json:"pure_category_tags"`
	QualityRat flexInt         `json:"quality_rate"`
	EpisodeCnt flexInt         `json:"episode_cnt"`
}

// rawVideoDetail 对应 video_detail 接口。
type rawVideoDetail struct {
	VideoID   string          `json:"video_id"`
	BookID    json.RawMessage `json:"book_id"`
	Title     string          `json:"title"`
	Episode   flexInt         `json:"episode"`
	Duration  flexInt         `json:"duration"`
	VideoList []rawSource     `json:"video_list"`
	ExpireAt  flexInt         `json:"expire_at"`
}

type rawSource struct {
	Quality  string  `json:"quality"`
	MainURL  string  `json:"main_url"`
	BackURL1 string  `json:"backup_url_1"`
	Width    flexInt `json:"width"`
	Height   flexInt `json:"height"`
	Bitrate  flexInt `json:"bit_rate"`
	Format   string  `json:"format"`
	Size     flexInt `json:"size"`
}

// rawCommentPage 对应 comment_list / reply_list。
type rawCommentPage struct {
	Comments []rawComment `json:"comments"`
	HasMore  flexBool     `json:"has_more"`
	Cursor   string       `json:"cursor"`
	Total    flexInt      `json:"total"`
}

type rawComment struct {
	CommentID  json.RawMessage `json:"comment_id"`
	UserID     json.RawMessage `json:"user_id"`
	UserName   string          `json:"user_name"`
	UserAvatar string          `json:"user_avatar"`
	Text       string          `json:"text"`
	DiggCount  flexInt         `json:"digg_count"`
	ReplyCount flexInt         `json:"reply_count"`
	IsDigged   flexBool        `json:"is_digged"`
	CreateTime flexInt         `json:"create_time"`
	ReplyID    json.RawMessage `json:"reply_id"`
	OffsetMs   flexInt         `json:"offset_ms"`
}

// rawUserInfo 对应用户资料接口。
type rawUserInfo struct {
	UserID    json.RawMessage `json:"user_id"`
	Name      string          `json:"name"`
	AvatarURL string          `json:"avatar_url"`
	Signature string          `json:"signature"`
	IsVIP     flexBool        `json:"is_vip"`
	VIPExpire flexInt         `json:"vip_expire_time"`
}

// ------------------------------------------------------------ flex 基础类型

// flexInt 宽容解析：服务端有时给数字、有时给字符串。
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// 浮点或非法值一律记 0，不让单字段拖垮整条响应。
		*f = 0
		return nil
	}
	*f = flexInt(v)
	return nil
}

// flexFloat 宽容解析小数。
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexFloat(v)
	return nil
}

// flexBool 宽容解析布尔：0/1、"0"/"1"、true/false。
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	switch strings.ToLower(s) {
	case "1", "true", "yes":
		*f = true
	default:
		*f = false
	}
	return nil
}

// flexStr 宽容解析：数字或字符串都转成字符串。
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		s = ""
	}
	*f = flexStr(s)
	return nil
}

// idString 把可能是数字或字符串的 ID 统一成十进制字符串。
func idString(b json.RawMessage) string {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		return ""
	}
	return s
}

// ---------------------------------------------------------------- 业务方法

// rawCellPage 对应 rank_change / new_series / feed_change 的 data 段。
//
// 实测结构：
//
//	{"code":0,"message":"SUCCESS","data":{
//	   "cell_view":{"cell_name":"推荐榜","cell_data":[{...,"book_data":[...]}]},
//	   "has_more":false,"next_offset":"...","session_id":"...","rank_version":"..."}}
type rawCellPage struct {
	CellView   rawCell  `json:"cell_view"`
	HasMore    flexBool `json:"has_more"`
	NextOffset flexStr  `json:"next_offset"`
	SessionID  string   `json:"session_id"`
}

// CellPage 拉取「换一换」类区块（榜单 / 新剧 / 推荐翻页）。
//
// action 由调用方给：rank_change、new_series、feed_change。
// cellID 是区块 id，不同入口用不同值；offset 翻页。
func (c *Client) CellPage(ctx context.Context, action, cellID string, offset int) (*model.FeedPage, error) {
	res, err := c.sess.CallJSON(ctx, action, map[string]any{"cell_id": cellID, "offset": offset, "limit": 20})
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = res.Decode(&raw); err != nil {
		return nil, err
	}
	return c.parseVideoFeed(raw)
}

// Feed 拉取首页信息流。
//
// 递归展开 tab_item[].cell_data[].cell_data[].book_data[]，
// 把每个 book 铺平成一条 FeedItem，并带上所属区块名作为角标。
func (c *Client) Feed(ctx context.Context, tabType int, cursor string) (*model.FeedPage, error) {
	if tabType == 0 {
		tabType = 16
	}
	return c.recommendationPage(ctx, tabType, cursor)
}

// collectCell 递归收集一个区块下的全部书条目。
func (c *Client) collectCell(dst *[]model.FeedItem, cell rawCell, tabTitle string) {
	badge := cell.CellName
	if badge == "" {
		badge = tabTitle
	}
	for _, b := range cell.Books {
		*dst = append(*dst, bookToFeedItem(b, badge))
	}
	for _, sub := range cell.SubCells {
		c.collectCell(dst, sub, tabTitle)
	}
}

// Tab 是 feed 的一个页签。
type Tab struct {
	Type  int    `json:"type"`
	Title string `json:"title"`
}

// FeedTabs 返回 feed 里真实存在的页签列表。
//
// 这是 FeedCategories 的真实数据源：服务端在 tab_item[] 里给出
// title 与 tab_type，客户端不应硬编码分类名。
func (c *Client) FeedTabs(ctx context.Context) ([]Tab, error) {
	params := feedTabParams(16, "")
	params["client_req_type"] = "60"
	res, err := c.sess.CallJSON(ctx, "feed", params)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = res.Decode(&raw); err != nil {
		return nil, err
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	root, _ := data.(map[string]any)
	tabs, _ := root["tab_item"].([]any)
	out := []Tab{}
	seen := map[int]bool{}
	for _, entry := range tabs {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(m, "title", "tab_name") == "" {
			continue
		}
		typ := number(m, "tab_type")
		if seen[typ] {
			continue
		}
		seen[typ] = true
		out = append(out, Tab{Type: typ, Title: text(m, "title", "tab_name")})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("上游未返回短剧内容，设备注册/签名尚未完成")
	}
	return out, nil
}

// bookToFeedItem 把原始书条目映射成前端用的条目。
func bookToFeedItem(b rawBook, badge string) model.FeedItem {
	cover := b.CoverURL
	if cover == "" {
		cover = b.AudioCover
	}
	if cover == "" {
		cover = b.ExpandCov
	}
	sub := b.Abstract
	if sub == "" {
		sub = b.ReadText
	}
	return model.FeedItem{
		SeriesID: idString(b.BookID),
		Title:    b.BookName,
		Cover:    cover,
		Subtitle: sub,
		Badge:    badge,
		PlayCnt:  int64(b.ReadCount),
		Category: b.Category,
		Score:    fmtScore(float64(b.Score)),
	}
}

// VideoDetail 拉取单集播放详情。
func (c *Client) VideoDetail(ctx context.Context, videoID string) (*model.VideoDetail, error) {
	if strings.TrimSpace(videoID) == "" {
		return nil, fmt.Errorf("缺少视频 ID")
	}
	if err := c.PrefetchVideoModels(ctx, []string{videoID}); err != nil {
		return nil, err
	}
	raw, ok := c.models.get(videoID)
	if !ok {
		return nil, fmt.Errorf("播放接口未返回该集模型")
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	return c.playbackWithFallback(ctx, data, videoID)
}

// Comments 拉取评论列表。dataType: 4=评论, 20=弹幕。
func (c *Client) Comments(ctx context.Context, seriesID, videoID string, dataType int, cursor string) (*model.CommentPage, error) {
	if dataType == 20 {
		return c.Danmaku(ctx, seriesID, videoID, 0, 600000)
	}
	return c.socialList(ctx, seriesID, videoID, cursor, 4, 0, 0, "")
}

// UserInfo 拉取当前用户资料。
func (c *Client) UserInfo(ctx context.Context) (*model.User, error) {
	res, err := c.sess.Call(ctx, "account_info", nil)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err = res.Decode(&data); err != nil {
		return nil, err
	}
	text := func(key string) string {
		v := data[key]
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		if string(v) == "null" {
			return ""
		}
		return string(v)
	}
	if code := text("error_code"); code != "" && code != "0" {
		return nil, fmt.Errorf("账号资料接口错误（%s）", code)
	}
	uid := text("user_id_str")
	if uid == "" {
		uid = text("user_id")
	}
	if uid == "" {
		return nil, errors.New("账号会话无效，请重新登录")
	}
	name := text("screen_name")
	if name == "" {
		name = text("name")
		if name == "" {
			name = text("nickname")
		}
	}
	return &model.User{UserID: uid, Nickname: name, Avatar: text("avatar_url"), Signature: text("description")}, nil
}

// AddComment 发评论/弹幕。
func (c *Client) AddComment(ctx context.Context, seriesID, videoID, text string, dataType int, offsetMs int) (string, error) {
	item, err := c.PublishSocial(ctx, SocialPost{SeriesID: seriesID, VideoID: videoID, Text: text, Kind: dataType, OffsetMs: int64(offsetMs)})
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

// UploadPicture 上传评论配图，返回图片 key。
func (c *Client) UploadPicture(ctx context.Context, filename string, data []byte) (string, error) {
	body, contentType, err := buildMultipart("picture", filename, data)
	if err != nil {
		return "", err
	}
	res, err := c.sess.CallRaw(ctx, "upload_picture", contentType, body)
	if err != nil {
		return "", err
	}
	var out struct {
		URI string `json:"uri"`
		URL string `json:"url"`
	}
	if err := res.Decode(&out); err != nil {
		return "", err
	}
	if out.URI != "" {
		return out.URI, nil
	}
	return out.URL, nil
}

// fmtScore 把分数格式化成一位小数。
func fmtScore(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
