package hgapi

import (
	"context"
	"duanju-desktop/pkg/model"
	"errors"
	"strconv"
)

type LikeRef struct {
	SeriesID string `json:"seriesId"`
	VideoID  string `json:"videoId"`
}
type RemoveLikesResult struct {
	Removed []LikeRef `json:"removed"`
	Failed  int       `json:"failed"`
	Error   string    `json:"error"`
}

// Likes returns liked videos. This endpoint lists records, not boolean states.
func (c *Client) Likes(ctx context.Context, cursor, kind string) (*model.FeedPage, error) {
	offset, err := strconv.Atoi(first(cursor, "0"))
	if err != nil || offset < 0 {
		return nil, errors.New("点赞分页位置无效")
	}
	out := &model.FeedPage{Items: []model.FeedItem{}}
	for page := 0; page < 5; page++ {
		res, err := c.sess.Call(ctx, "like_list", map[string]any{"object_type_list": "6,15,10", "action_type": "3", "offset": offset, "count": 100})
		if err != nil {
			return nil, err
		}
		raw, err := res.DecodeData()
		if err != nil {
			return nil, err
		}
		p, err := c.parseVideoFeed(raw)
		if err != nil {
			return nil, err
		}
		for _, it := range p.Items {
			if it.Series != nil {
				it.Series.Liked = true
			}
			itemKind := "real"
			if it.Series != nil && it.Series.ContentType == 1004 {
				itemKind = "anime"
			}
			if it.Series != nil && it.Series.Kind != "" {
				itemKind = it.Series.Kind
			}
			if kind == "" || kind == itemKind {
				out.Items = append(out.Items, it)
			}
		}
		out.HasMore = p.HasMore
		out.Cursor = p.Cursor
		if !p.HasMore {
			out.Cursor = ""
			break
		}
		next, _ := strconv.Atoi(p.Cursor)
		if next <= offset {
			next = offset + 100
		}
		out.Cursor = strconv.Itoa(next)
		offset = next
		if kind == "" || len(out.Items) >= 20 {
			break
		}
	}
	return out, nil
}
func (c *Client) SetLike(ctx context.Context, seriesID, videoID string, on bool) error {
	if !numericID(seriesID) || !numericID(videoID) {
		return errors.New("点赞需要有效的剧集与视频 ID")
	}
	action := 4
	if on {
		action = 3
	}
	_, err := c.sess.CallJSON(ctx, "article_do_action", map[string]any{"action_category": 1, "action_reason_remark": "", "action_type": action, "object_id": videoID, "object_type": 6, "business_param": map[string]any{"book_id": 0, "has_aigc_content": false, "modify_count": 0, "video_id": seriesID, "report_info": map[string]any{"enter_from": "MainFragmentActivity", "category_name": "", "position": "MainFragmentActivity"}}})
	return err
}
