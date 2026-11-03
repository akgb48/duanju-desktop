package app

// 本文件是前端绑定契约的**单一事实源**。
//
// frontend/src/api/bridge.js 声明了 118 个 `call('Name')` 包装，
// 每个名字必须对应 internal/app 上一个导出方法，参数个数也必须一致，
// 否则 Wails 生成的绑定会拒绝调用。TestBindingContract 会逐一校验。
//
// 命名分组只是为了便于阅读，Wails 不关心分组。

// bindingNames 列出全部 118 个前端可调方法名。
//
// 新增方法时必须同时改三处：
//  1. internal/app 上加方法
//  2. 本切片加名字
//  3. frontend/src/api/bridge.js 加 export
var bindingNames = []string{
	// ── 账号（9）────────────────────────────────────────────
	"Login", "Logout", "SendCode", "QRLoginStart", "QRLoginPoll",
	"QRLoginCancel", "LoginRegions", "RetryDeviceRegister", "Status",

	// ── 内容（13）───────────────────────────────────────────
	"RecommendTab", "FeedCategories", "Category", "SelectorPanel",
	"Rank", "RankPanel", "NewSeries", "Search", "SearchSuggest",
	"SeriesDetail", "SeriesSeasons", "NextSeason", "Related",

	// ── 播放（9）────────────────────────────────────────────
	"PlayInfo", "PrefetchPlay", "PrefetchPlayItems", "SaveProgress",
	"History", "DeleteHistory", "LocalPending", "DiagnoseLocalPlay",

	// ── 原生解码（9）─────────────────────────────────────────
	"HEVCStatus", "HEVCDiag", "RevealHEVCInstaller",
	"NativeDecOpen", "NativeDecPrepare", "NativeDecSeek",
	"NativeDecPreload", "NativeDecClose", "NativeDecSetCPUOnly",
	"NativeDecStatus",

	// ── 评论（13）───────────────────────────────────────────
	"Comments", "Danmaku", "Replies", "Reviews", "ReviewReplies",
	"SendComment", "SendDanmaku", "SendReply", "SendReview",
	"SendReviewReply", "DeleteComment", "LikeComment", "LikeDanmaku",

	// ── 互动（12）───────────────────────────────────────────
	"LikeReview", "Likes", "RemoveLikes", "SetLike", "Emoticons",
	"CollectEmoticon", "UploadPicture", "UploadLocal",
	"Favorites", "SetFavorite", "RemoveFavorites", "FollowUpdates",

	// ── 关注与预约（7）───────────────────────────────────────
	"IsFollowingUpdate", "SetFollowUpdate", "FollowUser",
	"Reservations", "SetReservation", "DeleteReservations", "Calendar",

	// ── 下载（7）────────────────────────────────────────────
	"Downloads", "AddDownloads", "DeleteDownloads", "PauseDownloads",
	"ResumeDownloads", "DownloadSeries", "OpenDownloadDir",

	// ── 设置与隐身（16）───────────────────────────────────────
	"BossToggle", "ClearCache", "DismissDataDirNotice", "GetDataDirInfo",
	"KeepAwake", "MoveDataDir", "PickDataDir", "RemoteConfig",
	"RestoreMiniWindow", "SetBossKey", "SetFullscreenLook",
	"SetPlaybackPrefs", "SetWindowButtonsVisible", "SetWindowMode",
	"StealthStart", "StealthStop",

	// ── 更新（4）────────────────────────────────────────────
	"CheckUpdate", "ApplyUpdate", "DownloadUpdate", "UpdateState",

	// ── 系统与信息（12）───────────────────────────────────────
	"About", "AppInfo", "CheckNotices", "ClearNotices", "ClipboardSeq",
	"FixFirewall", "FixLoopbackProxy", "Integrity", "MarkNoticesRead",
	"MyFeedback", "Notices", "OpenFirewallSettings",

	// ── 反馈与分享（7）────────────────────────────────────────
	"ReplyFeedback", "ReportEnv", "ReportError", "ResolveShare",
	"RestartApp", "ShareLink", "SubmitFeedback",
}

// BindingCount 是预期绑定数，供测试与 CI 守卫。
const BindingCount = 118
