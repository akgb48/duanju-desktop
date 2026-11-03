package hgapi

import "strconv"

func feedTabParams(tab int, cursor string) map[string]any {
	return map[string]any{
		"bottom_tab_type": "7", "landing_bottom_tab_type": "7", "need_personal_recommend": "1", "device_level": "3", "screen_width_px": "1078", "is_horizontal_screen": "false", "disable_digg_stat": "false",
		"stream_count": `[{"scene":"1","StreamCount":1,"StreamType":"1"}]`, "auth_aweme": "true", "auth_backward": "true", "has_video_cache": "false", "last_tab_type": "0", "last_tab_index": "0", "tab_index": "0", "req_rank_category_id": "0", "video_type_preferences_str": "[]",
		"tab_type": strconv.Itoa(tab), "offset": first(cursor, "0"), "session_id": "", "client_req_type": "1",
	}
}

func videoModelParams(videoID string, contentType int) map[string]any {
	return map[string]any{
		"mixed_video_id_map": map[string][]string{strconv.Itoa(contentType): {videoID}},
		"biz_param":          map[string]any{"detail_page_version": 0, "device_level": 4, "disable_digg_stat": false, "need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false, "use_server_dns": false, "video_platform": 1024},
	}
}
