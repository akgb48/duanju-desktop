package hgapi

import (
	"context"
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/model"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func numericID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func socialParams(seriesID, groupID, cursor string, kind int) map[string]any {
	p := map[string]any{"comment_source": 4, "server_channel": 18, "group_id": groupID, "group_type": 30, "comment_type": 4, "sort": 1, "count": 20, "cursor": cursor, "aid": 8662, "compliance_status": 0}
	b := map[string]any{"need_count": true, "book_id": seriesID, "req_type": 0}
	if kind == 2 {
		p["comment_source"], p["server_channel"], p["group_type"], p["comment_type"], p["count"] = 1, 34, 1, 2, 10
		b["need_count"] = false
		for _, key := range []string{"end_offset_time", "item_count", "max_item_count", "outflow_comment_count", "para_index", "playlet_consume_duration_ms", "playlet_item_consume_duration_ms", "read_item_count", "start_offset_time", "playlet_item_duration"} {
			b[key] = 0
		}
		for _, key := range []string{"comment_sort_debug", "hit_interaction_data_migration_ab", "is_last_episode", "need_danmaku_occlusion_face_data"} {
			b[key] = false
		}
	}
	p["business_param"] = b
	return p
}
func (c *Client) socialList(ctx context.Context, seriesID, groupID, cursor string, kind int, offset, duration int64, replyID string) (*model.CommentPage, error) {
	if !numericID(seriesID) || !numericID(groupID) {
		return nil, errors.New("缺少有效剧集或视频 ID")
	}
	p := socialParams(seriesID, groupID, cursor, kind)
	if kind == 20 {
		if offset < 0 {
			offset = 0
		}
		if duration <= 0 {
			duration = 600000
		}
		p["comment_source"], p["server_channel"], p["comment_type"], p["count"] = 601, 1000, 20, 90
		p["business_param"] = map[string]any{"book_id": seriesID, "start_offset_time": offset, "playlet_item_duration": duration, "need_danmaku_guide_type": []int{}}
	}
	path := "/novel/commentapi/comment/list/" + groupID + "/v1/"
	action := "comment_list"
	if replyID != "" {
		if !numericID(replyID) {
			return nil, errors.New("回复对象 ID 无效")
		}
		p["comment_id"] = replyID
		p["count"] = 10
		p["comment_source"] = 504
		business := map[string]any{"book_id": seriesID, "need_count": false}
		if kind == 2 {
			p["comment_source"] = 501
			business["real_level"] = 2
		}
		p["business_param"] = business
		path = "/novel/commentapi/reply/list/" + replyID + "/v1/"
		action = "reply_list"
	}
	// These source helpers use POST + an ID in the path, with routing in
	// query as well as JSON. A bare GET list endpoint returns a text 404.
	s := c.sess.Fork()
	s.SetParam("comment_source", fmt.Sprint(p["comment_source"]))
	s.SetParam("server_channel", fmt.Sprint(p["server_channel"]))
	res, err := s.CallJSONEndpoint(ctx, api.API{Action: action, Path: path, Method: api.POST, Route: api.RouteContent}, p)
	if err != nil {
		return nil, err
	}
	raw, err := res.DecodeData()
	if err != nil {
		return nil, err
	}
	return parseSocialPage(raw, seriesID, groupID, kind, offset)
}
func (c *Client) Reviews(ctx context.Context, seriesID, cursor string) (*model.CommentPage, error) {
	return c.socialList(ctx, seriesID, seriesID, cursor, 2, 0, 0, "")
}
func (c *Client) Danmaku(ctx context.Context, seriesID, videoID string, startMs, durationMs int64) (*model.CommentPage, error) {
	return c.socialList(ctx, seriesID, videoID, "", 20, startMs, durationMs, "")
}
func (c *Client) Replies(ctx context.Context, seriesID, videoID, commentID, cursor string, review bool) (*model.CommentPage, error) {
	kind := 4
	if review {
		kind = 2
		videoID = seriesID
	}
	return c.socialList(ctx, seriesID, videoID, cursor, kind, 0, 0, commentID)
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func integer64(m map[string]any, key string) int64 {
	n, _ := strconv.ParseInt(text(m, key), 10, 64)
	return n
}
func decimal(m map[string]any, key string) float64 {
	n, _ := strconv.ParseFloat(text(m, key), 64)
	return n
}
func parseSocialPage(raw []byte, seriesID, groupID string, kind int, startMs int64) (*model.CommentPage, error) {
	v, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	root := object(v)
	if root == nil {
		return nil, errors.New("评论响应格式错误")
	}
	meta := object(root["common_list_info"])
	if meta == nil {
		meta = object(root["comment_list_info"])
	}
	if meta == nil {
		return nil, errors.New("评论响应缺少分页信息")
	}
	out := &model.CommentPage{Items: []model.Comment{}, Cursor: text(meta, "cursor"), HasMore: truth(meta, "has_more"), Total: number(meta, "total")}
	list, ok := root["data_list"].([]any)
	if !ok {
		list, ok = root["reply_list"].([]any)
	}
	if !ok && root["data_list"] != nil {
		return nil, errors.New("评论列表格式错误")
	}
	seen := map[string]bool{}
	flat := make([]any, 0, len(list))
	var flatten func(any, int)
	flatten = func(v any, depth int) {
		if depth > 4 || len(flat) >= 2000 {
			return
		}
		flat = append(flat, v)
		if subs, ok := object(v)["sub_reply"].([]any); ok {
			for _, s := range subs {
				flatten(s, depth+1)
			}
		}
	}
	for _, v := range list {
		flatten(v, 0)
	}
	for _, entry := range flat {
		row := object(entry)
		cm := object(row["comment"])
		if cm == nil {
			cm = object(row["reply"])
		}
		if cm == nil && row["reply_id"] != nil {
			cm = row
		}
		common := object(cm["common"])
		if common == nil {
			common = object(cm["Common"])
		}
		expand := object(cm["expand"])
		content := object(common["content"])
		user := object(common["user_info"])
		base := object(user["base_info"])
		stat := object(cm["stat"])
		action := object(cm["user_action"])
		id := text(cm, "comment_id", "reply_id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		item := model.Comment{ID: id, SeriesID: seriesID, VideoID: groupID, UserID: first(text(base, "user_id"), text(user, "user_id")), Nickname: text(base, "user_name"), Avatar: text(base, "user_avatar", "expand_user_avatar"), Text: text(content, "text"), LikeCount: integer64(stat, "digg_count"), ReplyCount: integer64(stat, "reply_count"), IsLiked: truth(action, "user_digg"), CreatedAt: integer64(common, "create_timestamp"), DataType: kind, OffsetMs: int(integer64(expand, "offset_time")), Score: decimal(expand, "score"), ScoreNote: text(expand, "score_suffix_text"), ReplyTo: text(common, "reply_to_username")}
		item.ParentID = text(cm, "reply_to_comment_id")
		if to := object(cm["reply_to_user_info"]); to != nil {
			item.ReplyTo = text(object(to["base_info"]), "user_name")
		}
		images := object(content["image_data_list"])
		if rows, ok := images["image_data"].([]any); ok {
			for _, v := range rows {
				im := object(v)
				u := text(im, "web_uri", "web_url")
				if u != "" {
					item.Images = append(item.Images, model.CommentImage{URL: u, Large: text(im, "expand_web_url", "web_url"), WebURI: text(im, "web_uri"), WebURL: text(im, "web_url"), ExpandURL: text(im, "expand_web_url"), Width: number(im, "width"), Height: number(im, "height")})
				}
			}
		}
		out.Items = append(out.Items, item)
	}
	if kind == 20 {
		var cursor struct {
			End int64 `json:"end_offset_time"`
		}
		_ = json.Unmarshal([]byte(out.Cursor), &cursor)
		out.NextMs = cursor.End
		if out.NextMs <= startMs {
			out.NextMs = startMs + 30000
		}
	}
	extra := object(root["extra"])
	out.Score = decimal(extra, "credibility_score")
	out.ScoreCount = integer64(extra, "credibility_score_count")
	return out, nil
}

func (c *Client) Related(ctx context.Context, seriesID string) (*model.RelatedSeries, error) {
	if !numericID(seriesID) {
		return nil, errors.New("剧集 ID 无效")
	}
	p := map[string]any{"scene": "10", "from": "detail_page_more_related", "offset": "0", "limit": "0", "total_chapter_num": "0", "current_chapter_num": "0", "post_id": "0", "is_horizontal_screen": "false", "bookstore_tab": "0", "bookstore_tab_type": "0", "book_id": seriesID}
	res, err := c.sess.Call(ctx, "plan_v", p)
	if err != nil {
		return nil, err
	}
	raw, err := res.DecodeData()
	if err != nil {
		return nil, err
	}
	var cells []json.RawMessage
	if err = json.Unmarshal(raw, &cells); err != nil {
		return nil, fmt.Errorf("相关推荐响应格式错误: %w", err)
	}
	out := &model.RelatedSeries{Works: []model.Series{}, Guess: []model.Series{}}
	seen := map[string]bool{seriesID: true}
	for _, cell := range cells {
		var info struct {
			Name string `json:"cell_name"`
		}
		_ = json.Unmarshal(cell, &info)
		page, err := c.parseVideoFeed(cell)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.Series == nil || seen[it.SeriesID] {
				continue
			}
			seen[it.SeriesID] = true
			if strings.Contains(info.Name, "作品") {
				out.Works = append(out.Works, *it.Series)
			} else {
				out.Guess = append(out.Guess, *it.Series)
			}
		}
	}
	return out, nil
}
