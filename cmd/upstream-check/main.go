// Local diagnostic: saves response bodies only, never request cookies/headers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/crypto"
	"duanju-desktop/pkg/register"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := ".runtime-signed-probe"
	b, err := os.ReadFile(filepath.Join(dir, "device.json"))
	if err != nil {
		panic(err)
	}
	var rec struct {
		Profile  *register.Profile
		Identity register.Identity
		Cookies  string
	}
	if err = json.Unmarshal(b, &rec); err != nil {
		panic(err)
	}
	sess := api.NewSession()
	register.ApplySession(sess, rec.Profile, rec.Identity, rec.Cookies)
	sess.SetSigner(crypto.NewDefaultSigner())
	for _, input := range []struct {
		Action string
		Body   any
	}{
		{"reservation_list", map[string]any{"tab_type": "5", "tab_style": "2", "active_panel": "6", "gender_type": "2", "need_calendar_schema": "true"}},
		{"feed_change", map[string]any{"bottom_tab_type": "7", "client_req_type": "2", "client_template": "7", "change_type": "0", "category_id": "0", "cell_sub_id": "0", "limit": "12", "need_personal_recommend": "1", "video_type_preferences_str": "[]", "cell_id": "7294257141911650341", "plan_id": "", "tab_type": "16", "offset": "0", "session_id": ""}},
		{"feed", map[string]any{"bottom_tab_type": "7", "landing_bottom_tab_type": "7", "tab_type": "16", "offset": "0", "client_req_type": "1", "need_personal_recommend": "1", "device_level": "3", "screen_width_px": "1078", "is_horizontal_screen": "false", "disable_digg_stat": "false", "stream_count": `[{"scene":"1","StreamCount":1,"StreamType":"1"}]`, "auth_aweme": "true", "auth_backward": "true", "has_video_cache": "false", "last_tab_type": "0", "last_tab_index": "0", "tab_index": "0", "req_rank_category_id": "0", "video_type_preferences_str": "[]", "session_id": ""}},
		{"rank_change", map[string]any{"tab_type": "26", "client_req_type": "2", "client_template": "2", "background_selected_items": "", "category_id": "0", "limit": "0", "cell_sub_id": "0", "gender": "2", "cell_id": "7470092475068071998", "selected_items": "", "sub_selected_items": "", "panel_selected_items": "", "unlimited_selector_change_type": "2", "offset": "0"}},
		{"new_series", map[string]any{"change_type": "1", "selected_items": "firstonlinetime_new", "client_req_type": "1", "cell_id": "7431550523368554558", "limit": "18", "offset": "0", "cell_gender": "2"}},
		{"category_landpage", map[string]any{"req_scene": "default", "need_selector_panel": false, "req_type": "only_content", "select_items": map[string][]string{}, "offset": 0, "limit": 18, "client_req_type": 3, "filter_ids": "", "session_id": ""}},
		{"search", map[string]any{"query": "总裁", "count": 100, "offset": 0, "tab_type": 11, "use_correct": true, "bookshelf_search_plan": "4"}},
		{"multi_video_model", map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"7701201568489557017"}}, "biz_param": map[string]any{"detail_page_version": 0, "device_level": 4, "disable_digg_stat": false, "need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false, "use_server_dns": false, "video_platform": 1024}}},
	} {
		if len(os.Args) > 1 && input.Action != os.Args[1] {
			continue
		}
		if len(os.Args) > 2 {
			requestBody, readErr := os.ReadFile(os.Args[2])
			if readErr != nil {
				panic(readErr)
			}
			if err = json.Unmarshal(requestBody, &input.Body); err != nil {
				panic(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		res, err := sess.CallJSON(ctx, input.Action, input.Body)
		cancel()
		if err != nil {
			fmt.Printf("%s: %v\n", input.Action, err)
			continue
		}
		if err = os.WriteFile(filepath.Join(dir, input.Action+"-wire.json"), res.Raw, 0600); err != nil {
			panic(err)
		}
		fmt.Printf("%s: HTTP %d, %d bytes\n", input.Action, res.Envelope.HTTPCode, len(res.Raw))
	}
}
