// Package model 定义跨层共享的领域类型。
//
// 这些结构同时用于：解析上游 JSON、JSON 化后传给前端、写入 SQLite。
// 因此每个字段都带 json tag，且与前端 TS 类型一一对应。
package model

// Series 是一部短剧。
type Series struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Cover        string    `json:"cover"`
	Description  string    `json:"description"`
	Author       string    `json:"author"`
	Category     string    `json:"category"`
	Status       string    `json:"status"` // ongoing / completed
	EpisodeCnt   int       `json:"episodeCount"`
	PlayCount    int64     `json:"playCount"`
	LikeCount    int64     `json:"likeCount"`
	Score        string    `json:"score"`
	Tags         []string  `json:"tags"`
	UpdatedAt    int64     `json:"updatedAt"`
	Episodes     []Episode `json:"episodes"`
	ContentType  int       `json:"contentType"`
	Favorited    bool      `json:"favorited"`
	Liked        bool      `json:"liked"`
	Badge        string    `json:"badge,omitempty"`
	TitleTag     *TitleTag `json:"titleTag,omitempty"`
	RankText     string    `json:"rankText,omitempty"`
	SubInfo      string    `json:"subInfo,omitempty"`
	Cast         []string  `json:"cast,omitempty"`
	Person       bool      `json:"person,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	StartVideoID string    `json:"startVideoId,omitempty"`
	StartEpisode int       `json:"startEpisode,omitempty"`
	SeasonIndex  int       `json:"seasonIndex,omitempty"`
	MultiSeason  bool      `json:"multiSeason,omitempty"`
}

type TitleTag struct {
	Text      string   `json:"text"`
	Colors    []string `json:"colors,omitempty"`
	Icon      string   `json:"icon,omitempty"`
	TextColor string   `json:"textColor,omitempty"`
	Source    string   `json:"source,omitempty"`
}

// Episode 是一集。
type Episode struct {
	SeriesID    string `json:"seriesId"`
	Index       int    `json:"index"`
	Title       string `json:"title"`
	VideoID     string `json:"videoId"`
	Duration    int    `json:"duration"` // 秒
	Cover       string `json:"cover"`
	PlayCount   int64  `json:"playCount"`
	IsFree      bool   `json:"isFree"`
	PublishedAt int64  `json:"publishedAt"`
}

// PlaySource 是一个可播放清晰度。
type PlaySource struct {
	Quality   string `json:"quality"` // 360p / 540p / 720p / 1080p
	URL       string `json:"url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Bitrate   int    `json:"bitrate"`
	Format    string `json:"format"` // mp4 / hls
	Size      int64  `json:"size"`
	BackupURL string `json:"backupUrl,omitempty"`
	Codec     string `json:"codec,omitempty"`
	// ContentKey stays in the backend and is used by the MP4 range transformer.
	ContentKey []byte `json:"-"`
}

// VideoDetail 是播放页需要的全部数据。
type VideoDetail struct {
	SeriesID string       `json:"seriesId"`
	VideoID  string       `json:"videoId"`
	Title    string       `json:"title"`
	Episode  int          `json:"episode"`
	Duration int          `json:"duration"`
	Sources  []PlaySource `json:"sources"`
	// ExpireAt 是播放地址的过期时间戳（秒）。
	ExpireAt int64 `json:"expireAt"`
}

// FeedItem 是信息流里的一张卡。
type FeedItem struct {
	SeriesID        string       `json:"seriesId"`
	Title           string       `json:"title"`
	Cover           string       `json:"cover"`
	Subtitle        string       `json:"subtitle"`
	Badge           string       `json:"badge"`
	PlayCnt         int64        `json:"playCount"`
	Category        string       `json:"category"`
	Score           string       `json:"score"`
	VideoID         string       `json:"videoId,omitempty"`
	Episode         int          `json:"episode,omitempty"`
	Series          *Series      `json:"series,omitempty"`
	RecommendSource string       `json:"recommendSource,omitempty"`
	Reasons         []FeedReason `json:"reasons,omitempty"`
}

type FeedReason struct {
	Text      string `json:"text"`
	Highlight bool   `json:"highlight,omitempty"`
}

// FeedPage 是一页信息流。
type FeedPage struct {
	Items      []FeedItem `json:"items"`
	HasMore    bool       `json:"hasMore"`
	Cursor     string     `json:"cursor"`
	TotalCount int        `json:"totalCount"`
}

// Comment 是一条评论或弹幕。
type Comment struct {
	ID         string `json:"id"`
	SeriesID   string `json:"seriesId"`
	VideoID    string `json:"videoId"`
	ParentID   string `json:"parentId"`
	UserID     string `json:"userId"`
	Nickname   string `json:"nickname"`
	Avatar     string `json:"avatar"`
	Text       string `json:"text"`
	LikeCount  int64  `json:"likeCount"`
	ReplyCount int64  `json:"replyCount"`
	IsLiked    bool   `json:"isLiked"`
	CreatedAt  int64  `json:"createdAt"`
	// DataType 区分普通评论(4)与弹幕(20)。
	DataType int `json:"dataType"`
	// OffsetMs 仅弹幕使用，表示出现时间。
	OffsetMs  int            `json:"offsetMs"`
	ReplyTo   string         `json:"replyTo,omitempty"`
	Score     float64        `json:"score,omitempty"`
	ScoreNote string         `json:"scoreNote,omitempty"`
	Images    []CommentImage `json:"images,omitempty"`
}

type CommentImage struct {
	URL       string `json:"url"`
	Large     string `json:"large"`
	WebURI    string `json:"webUri"`
	WebURL    string `json:"webUrl"`
	ExpandURL string `json:"expandUrl"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}
type RelatedSeries struct {
	Works []Series `json:"works"`
	Guess []Series `json:"guess"`
}

// CommentPage 是一页评论。
type CommentPage struct {
	Items      []Comment `json:"items"`
	HasMore    bool      `json:"hasMore"`
	Cursor     string    `json:"cursor"`
	Total      int       `json:"total"`
	NextMs     int64     `json:"nextMs,omitempty"`
	Score      float64   `json:"score,omitempty"`
	ScoreCount int64     `json:"scoreCount,omitempty"`
}

// User 是当前登录用户。
type User struct {
	UserID    string `json:"userId"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Signature string `json:"signature"`
	IsVIP     bool   `json:"isVip"`
	VIPExpire int64  `json:"vipExpire"`
}

// Account 是本地持久化的账号信息。
type Account struct {
	UserID    string `json:"userId"`
	Token     string `json:"token"`
	Cookie    string `json:"cookie"`
	DeviceID  string `json:"deviceId"`
	InstallID string `json:"installId"`
	CDID      string `json:"cdid"`
	OpenUDID  string `json:"openudid"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	UpdatedAt int64  `json:"updatedAt"`
}

// HistoryEntry 是一条观看记录。
type HistoryEntry struct {
	SeriesID   string `json:"seriesId"`
	Title      string `json:"title"`
	Cover      string `json:"cover"`
	Episode    int    `json:"episode"`
	PositionMs int64  `json:"positionMs"`
	DurationMs int64  `json:"durationMs"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// SearchResult 是搜索结果。
type SearchResult struct {
	Series  []Series `json:"series"`
	HasMore bool     `json:"hasMore"`
	Cursor  string   `json:"cursor"`
	Keyword string   `json:"keyword"`
	Total   int      `json:"total"`
	// Source 标明结果来源：server = 上游搜索接口，local = 本地过滤降级。
	Source string `json:"source"`
}

type SearchHighlight struct {
	Text string `json:"text"`
	HL   bool   `json:"hl"`
}

type SearchSuggestion struct {
	Word     string            `json:"word"`
	SeriesID string            `json:"seriesId,omitempty"`
	Cover    string            `json:"cover,omitempty"`
	Abstract string            `json:"abstract,omitempty"`
	Parts    []SearchHighlight `json:"parts"`
}

// Settings 是应用设置。
type Settings struct {
	Theme          string `json:"theme"`
	AutoPlay       bool   `json:"autoPlay"`
	DefaultQuality string `json:"defaultQuality"`
	DownloadDir    string `json:"downloadDir"`
	MaxConcurrent  int    `json:"maxConcurrent"`
	DanmakuEnabled bool   `json:"danmakuEnabled"`
	DanmakuOpacity int    `json:"danmakuOpacity"`
	ProxyURL       string `json:"proxyUrl"`
	HardwareDecode bool   `json:"hardwareDecode"`

	// 外观与交互
	FullscreenLook string `json:"fullscreenLook"` // 默认 default，可选 immersive
	BossKey        string `json:"bossKey"`
	KeepAwake      bool   `json:"keepAwake"`
	ButtonsVisible bool   `json:"buttonsVisible"`

	StartPage string `json:"startPage"`
}

// DefaultSettings 返回出厂设置。
func DefaultSettings() Settings {
	return Settings{
		Theme:          "dark",
		AutoPlay:       true,
		DefaultQuality: "720p",
		DownloadDir:    "",
		MaxConcurrent:  3,
		DanmakuEnabled: true,
		DanmakuOpacity: 80,
		ProxyURL:       "",
		HardwareDecode: true,
		FullscreenLook: "default",
		KeepAwake:      true,
		ButtonsVisible: true,
		StartPage:      "/recommend",
	}
}

// Region 是配置下发的可用区域。
type Region struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Host string `json:"host"`
}

// Notice 是一条公告。
type Notice struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Level    string `json:"level"`
	Pinned   bool   `json:"pinned"`
	CreateAt int64  `json:"createAt"`
}
