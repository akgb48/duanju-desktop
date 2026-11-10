package api

// Content 组：/reading/ 与 /novel/ 前缀的内容与 UGC 接口。
func init() {
	reg := DefaultRegistry
	for _, a := range []*API{
		// ---- 内容浏览 ----
		{Action: "feed", Path: "/reading/bookapi/bookmall/tab/v", Method: GET,
			Route: RouteContent, Doc: "书城首页信息流"},
		{Action: "feed_change", Path: "/reading/bookapi/bookmall/cell/change/v", Method: GET,
			Route: RouteContent, Doc: "换一换（按 tab_type 区分 feed/rank）"},
		{Action: "rank_change", Path: "/reading/bookapi/bookmall/cell/change/v", Method: GET,
			Route: RouteContent, Doc: "榜单换一换"},
		{Action: "new_series", Path: "/reading/bookapi/bookmall/cell/change/v1/", Method: GET,
			Route: RouteContent, Doc: "新剧集推荐"},
		{Action: "category_landpage", Path: "/reading/distribution/category/landpage/v1/", Method: POST,
			Route: RouteContent, Doc: "分类落地页"},
		{Action: "search", Path: "/reading/bookapi/search/tab/v", Method: GET,
			Route: RouteContent, Doc: "搜索结果页"},
		{Action: "search_suggest", Path: "/reading/bookapi/search/suggest/v", Method: GET,
			Route: RouteContent, Doc: "搜索联想词"},
		{Action: "plan_v", Path: "/reading/bookapi/plan/v", Method: GET,
			Route: RouteContent, Doc: "运营投放计划"},
		{Action: "book_pack_fields", Path: "/reading/distribution/book_pack_fields/v1/", Method: POST,
			Route: RouteContent, Doc: "书封字段批量拉取"},
		{Action: "multi_video_detail", Path: "/novel/player/multi_video_detail/preload/v1", Method: POST,
			Route: RouteContent, Doc: "多集视频详情预加载"},
		{Action: "video_detail", Path: "/novel/player/video_detail/v1/", Method: POST,
			Route: RouteContent, Doc: "单集视频详情"},
		{Action: "multi_video_model", Path: "/novel/player/multi_video_model/player/v1", Method: POST,
			Route: RouteContent, Doc: "剧集结构模型"},

		// ---- 用户数据读取 ----
		{Action: "account_info", Path: "/passport/account/info/v2/", Method: GET,
			Route: RoutePassport, Doc: "当前账号信息"},
		{Action: "user_info", Path: "/reading/user/info/v1/", Method: GET,
			Route: RouteContent, Doc: "用户资料（路径在 /reading/ 下，走内容网关）"},
		{Action: "read_history", Path: "/reading/bookapi/read_history/list/v", Method: GET,
			Route: RouteContent, Doc: "阅读历史列表"},
		{Action: "reading_history", Path: "/reading/bookapi/read_history/v1/", Method: GET,
			Route: RouteContent, Doc: "阅读历史（旧版）"},
		{Action: "favorite_list", Path: "/reading/bookapi/bookshelf/video/list/v", Method: GET,
			Route: RouteContent, Doc: "追剧书架"},
		{Action: "read_progress_list", Path: "/reading/bookapi/read_progress/list/v", Method: GET,
			Route: RouteContent, Doc: "批量进度"},
		{Action: "read_progress_get", Path: "/reading/bookapi/read_progress/get/v", Method: POST,
			Route: RouteContent, Doc: "单本进度"},
		{Action: "like_list", Path: "/reading/ugc/action/mget/v", Method: GET,
			Route: RouteContent, Doc: "点赞状态批量查询"},
		{Action: "follow_update_list", Path: "/novel/articleapi/relation/get_user_action/v1/", Method: GET,
			Route: RouteContent, Doc: "关注动态"},
		{Action: "reservation_list", Path: "/reading/user/subscribe/list/v1/", Method: GET,
			Route: RouteContent, Doc: "预约列表"},

		// ---- 用户数据写入 ----
		{Action: "read_progress_upload", Path: "/reading/bookapi/read_progress/upload/v", Method: POST,
			Route: RouteContent, Doc: "上报播放进度（40 字段）"},
		{Action: "read_history_update", Path: "/reading/bookapi/read_history/update/v", Method: POST,
			Route: RouteContent, Doc: "更新阅读历史"},
		{Action: "favorite_update", Path: "/reading/bookapi/bookshelf/video/update/v", Method: POST,
			Route: RouteContent, Doc: "加入/移出追剧"},
		{Action: "subscribe_delete", Path: "/reading/user/subscribe/delete/v", Method: POST,
			Route: RouteContent, Doc: "删除预约"},
		{Action: "uncover_subscribe", Path: "/reading/bookapi/search/uncover_subscribe/v", Method: POST,
			Route: RouteContent, Doc: "解锁预约"},
		{Action: "article_do_action", Path: "/novel/articleapi/do_action/v1/", Method: POST,
			Route: RouteContent, Doc: "文章点赞/收藏"},

		// ---- 评论 / 弹幕 / 回复 ----
		{Action: "comment_add", Path: "/novel/commentapi/comment/add/v1/", Method: POST,
			Route: RouteContent, Doc: "发评论（data_type=4）"},
		{Action: "danmaku_add", Path: "/novel/commentapi/comment/add/v1/", Method: POST,
			Route: RouteContent, Doc: "发弹幕（data_type=20）"},
		{Action: "reply_add", Path: "/novel/commentapi/reply/add/v1/", Method: POST,
			Route: RouteContent, Doc: "回复评论"},
		{Action: "comment_do_action", Path: "/novel/commentapi/comment/do_action/v1/", Method: POST,
			Route: RouteContent, Doc: "评论点赞/举报"},
		{Action: "comment_del", Path: "/novel/commentapi/comment/del/v1/", Method: POST,
			Route: RouteContent, Doc: "删除评论"},
		{Action: "comment_list", Path: "/novel/commentapi/comment/list/", Method: POST,
			Route: RouteContent, Doc: "评论/剧评/弹幕列表；动态追加 group_id/v1/，按来源和类型分流"},
		{Action: "reply_list", Path: "/novel/commentapi/reply/list/", Method: POST,
			Route: RouteContent, Doc: "回复列表；动态追加 comment_id/v1/"},

		// ---- UGC 图片 / 表情 ----
		{Action: "upload_picture", Path: "/reading/reader/upload/picture/", Method: POST,
			Route: RouteContent, Doc: "上传评论配图（multipart）"},
		{Action: "emoticon_list", Path: "/reading/ugc/comment/emoticon/v", Method: GET,
			Route: RouteContent, Doc: "表情包列表"},
		{Action: "emoticon_collect", Path: "/reading/ugc/comment/emoticon/collect/v", Method: POST,
			Route: RouteContent, Doc: "收藏表情"},

		// ---- 工具 ----
		{Action: "share_short_url", Path: "/reading/user/share/short_url/", Method: GET,
			Route: RouteContent, Doc: "生成分享短链"},

		// ---- 账号 / 设备 ----
		{Action: "device_register", Path: "/service/2/device_register/", Method: POST,
			Host: "log.snssdk.com", Route: RouteBase, Doc: "设备注册（TTEncrypt 二进制体）"},
		{Action: "settings_v3", Path: "/service/settings/v3/", Method: GET,
			Host: "is.snssdk.com", Route: RouteBase, Doc: "区域与全局配置"},
		{Action: "send_code", Path: "/passport/mobile/send_code/v1/", Method: POST,
			Route: RoutePassport, Doc: "发送短信验证码"},
		{Action: "sms_login", Path: "/passport/mobile/sms_login/", Method: POST,
			Route: RoutePassport, Doc: "短信登录"},
		{Action: "upsms_verify", Path: "/passport/upsms/verify/", Method: POST,
			Route: RoutePassport, Doc: "上行短信校验"},
		{Action: "qr_get", Path: "/passport/web/get_qrcode/", Method: GET,
			Route: RoutePassport, Doc: "获取扫码登录二维码"},
		{Action: "qr_poll", Path: "/passport/web/check_qrconnect/", Method: GET,
			Route: RoutePassport, Doc: "轮询扫码状态"},
		{Action: "tt_token", Path: "/passport/x-tt-token", Method: GET,
			Route: RoutePassport, Doc: "换取 tt token"},

		// ---- 自更新（独立服务） ----
		{Action: "upd_check", Path: "/api/v1/check", Method: GET,
			Route: RouteUpdate, Doc: "检查更新清单"},
		{Action: "upd_events", Path: "/api/v1/events", Method: POST,
			Route: RouteUpdate, Doc: "上报更新遥测"},
		{Action: "upd_feedback", Path: "/api/v1/feedback", Method: POST,
			Route: RouteUpdate, Doc: "提交反馈"},
		{Action: "upd_feedback_reply", Path: "/api/v1/feedback/%d/reply", Method: GET,
			Route: RouteUpdate, Doc: "查询反馈回复"},
	} {
		reg.MustRegister(a)
	}
}

// DefaultRegistry 是进程级唯一注册表。
var DefaultRegistry = NewRegistry()

// RegistrySize 是预期注册条数，用于启动自检。改动接口表时必须同步。
const RegistrySize = 51
