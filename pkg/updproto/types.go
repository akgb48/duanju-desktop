package updproto

// Notice 是一条更新器公告，显示在更新面板顶部。
type Notice struct {
	// ID 是公告唯一标识，前端用它做已读去重。
	ID string `json:"id"`
	// Level 是级别：info / warn / error。
	Level string `json:"level"`
	// Text 是正文。
	Text string `json:"text"`
	// URL 是可选的跳转链接。
	URL string `json:"url"`
}

// Disable 是远端停机指令：命中后客户端拒绝继续使用在线功能。
type Disable struct {
	// Message 是展示给用户的停机原因。
	Message string `json:"message"`
	// URL 是可选的详情页。
	URL string `json:"url"`
}

// FeedbackMsg 是反馈会话中的一条消息。
type FeedbackMsg struct {
	// From 是发送方：user / admin。
	From string `json:"from"`
	// Content 是消息正文。
	Content string `json:"content"`
	// At 是发送时间，Unix 秒。
	At int64 `json:"at"`
}

// Feedback 是一条反馈工单。
type Feedback struct {
	// ID 是工单号。
	ID string `json:"id"`
	// Category 是分类：bug / play / suggest / other。
	Category string `json:"category"`
	// Content 是用户提交的正文。
	Content string `json:"content"`
	// Email 是回信地址，可为空。
	Email string `json:"email"`
	// Status 是工单状态：open / processing / resolved。
	Status string `json:"status"`
	// Reply 是作者的首次回复（旧字段，新数据用 Messages）。
	Reply string `json:"reply"`
	// CreatedAt 是提交时间，Unix 秒。
	CreatedAt int64 `json:"createdAt"`
	// RepliedAt 是首次回复时间，Unix 秒；未回复为 0。
	RepliedAt int64 `json:"repliedAt"`
	// Messages 是完整会话，按时间正序。
	Messages []FeedbackMsg `json:"messages"`
}

// FeedbackReply 是回复工单时提交的内容。
//
// 对应 POST /api/v1/feedback/%d/reply 的请求体；
// 服务端完成后把新增消息并入 Feedback.Messages 返回。
type FeedbackReply struct {
	// ID 是工单号，与路径参数一致。
	ID string `json:"id"`
	// From 是发送方，客户端固定传 user。
	From string `json:"from"`
	// Content 是回复正文。
	Content string `json:"content"`
	// At 是发送时间，Unix 秒；为 0 时由服务端填充。
	At int64 `json:"at"`
}

// Envelope 是更新服务通用信封：code=0 表示成功。
type Envelope struct {
	// Code 是业务码，0 表示成功。
	Code int `json:"code"`
	// Message 是错误说明。
	Message string `json:"message"`
	// Data 是业务数据原文，由调用方二次解析。
	Data []byte `json:"data"`
}

// Manifest 是 /api/v1/check 返回的清单结构。
//
// 它与 IndexFile 是同一份数据的不同视图：Manifest 用于「检查更新」，
// IndexFile 用于「本地缓存的索引」。
type Manifest struct {
	// Version 是最新版本号。
	Version string `json:"version"`
	// MinVersion 是最低可自更新版本。
	MinVersion string `json:"minVersion"`
	// Notes 是版本说明。
	Notes string `json:"notes"`
	// PubDate 是发布时间，Unix 秒。
	PubDate int64 `json:"pubDate"`
	// Notice 是随版本下发的公告。
	Notice *Notice `json:"notice"`
	// Disable 是随版本下发的停机指令。
	Disable *Disable `json:"disabled"`
	// ManifestVersionCode 是清单自身的版本号，用于缓存判定。
	ManifestVersionCode int64 `json:"manifestVersionCode"`
	// Files 是该版本的全部文件。
	Files []Package `json:"files"`
}

// Release 是「有新版本」时给 UI 的摘要信息。
type Release struct {
	// Version 是新版本号。
	Version string `json:"version"`
	// Changelog 是变更说明。
	Changelog string `json:"changelog"`
	// Size 是安装包字节数。
	Size int64 `json:"size"`
	// PublishedAt 是发布时间，Unix 秒。
	PublishedAt int64 `json:"publishedAt"`
}
