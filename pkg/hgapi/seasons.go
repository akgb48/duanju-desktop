package hgapi

import (
	"context"
	"errors"
	"strings"
)

type SeriesSeason struct {
	SeriesID     string `json:"seriesId"`
	FirstVideoID string `json:"firstVideoId"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
	Label        string `json:"label"`
	Sub          string `json:"sub"`
	Upcoming     bool   `json:"upcoming"`
	Reserved     bool   `json:"reserved"`
}
type SeasonRef struct {
	SeriesID     string `json:"seriesId"`
	FirstVideoID string `json:"firstVideoId"`
	SeasonIndex  int    `json:"seasonIndex"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
}

func (c *Client) seasonRecords(ctx context.Context, id, from string) ([]map[string]any, error) {
	if !numericID(id) {
		return nil, errors.New("剧集 ID 无效")
	}
	res, err := c.sess.Call(ctx, "plan_v", map[string]any{"scene": "10", "from": from, "offset": "0", "limit": "0", "total_chapter_num": "0", "current_chapter_num": "0", "post_id": "0", "is_horizontal_screen": "false", "bookstore_tab": "0", "bookstore_tab_type": "0", "book_id": id, "filter_ids": id})
	if err != nil {
		return nil, err
	}
	raw, err := res.DecodeData()
	if err != nil {
		return nil, err
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	cells, ok := data.([]any)
	if !ok {
		return nil, errors.New("多季列表响应格式错误")
	}
	for _, v := range cells {
		for _, item := range array(object(v)["video_data"]) {
			if m := object(item); m != nil {
				out = append(out, m)
			}
		}
	}
	return out, nil
}
func array(v any) []any { a, _ := v.([]any); return a }
func (c *Client) SeriesSeasons(ctx context.Context, id string) ([]SeriesSeason, error) {
	records, err := c.seasonRecords(ctx, id, "single_col_same_ip_series")
	if err != nil {
		return nil, err
	}
	out := []SeriesSeason{}
	seen := map[string]bool{}
	for _, m := range records {
		sid := text(m, "series_id")
		title := text(m, "title", "series_title")
		if sid == "" || title == "" || seen[sid] {
			continue
		}
		seen[sid] = true
		s := SeriesSeason{SeriesID: sid, FirstVideoID: text(m, "vid"), Title: title, Cover: text(m, "cover"), Upcoming: truth(m, "disable_play") || number(m, "episode_cnt") == 0, Reserved: truth(object(m["subscribe_item"]), "is_subscribed")}
		for _, v := range array(m["secondary_info_list"]) {
			label := text(object(v), "content")
			if strings.HasPrefix(label, "第") && strings.HasSuffix(label, "季") {
				s.Label = label
				break
			}
		}
		var subs []string
		for _, v := range array(m["sub_title_list"]) {
			if sub := strings.TrimSpace(text(object(v), "content")); sub != "" {
				subs = append(subs, sub)
			}
		}
		if s.Upcoming {
			s.Sub = strings.Join(subs, " · ")
		} else if len(subs) > 0 {
			s.Sub = subs[0]
			for _, v := range subs {
				if strings.Contains(v, "更新") {
					s.Sub = v
					break
				}
			}
		}
		out = append(out, s)
	}
	if len(out) < 2 {
		return []SeriesSeason{}, nil
	}
	return out, nil
}
func (c *Client) NextSeason(ctx context.Context, id string, index int) (*SeasonRef, error) {
	records, err := c.seasonRecords(ctx, id, "series_short_play_info")
	if err != nil {
		return nil, err
	}
	if index < 1 {
		index = 1
	}
	var next *SeasonRef
	for _, m := range records {
		season := number(m, "season_index")
		vid := text(m, "vid")
		sid := text(m, "series_id")
		if season <= index || number(m, "episode_cnt") <= 0 || truth(m, "disable_play") || vid == "" || sid == "" || sid == id {
			continue
		}
		if next == nil || season < next.SeasonIndex {
			next = &SeasonRef{SeriesID: sid, FirstVideoID: vid, SeasonIndex: season, Title: text(m, "title", "series_title"), Cover: text(m, "cover")}
		}
	}
	return next, nil
}
