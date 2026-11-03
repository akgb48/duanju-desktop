package hgapi

import (
	"context"
	"errors"
	"hongguo-desktop/pkg/model"
	"strings"
)

type SocialPost struct {
	SeriesID, VideoID, CommentID, ReplyID, ReplyUserID, Text, PresetID string
	Kind                                                               int
	OffsetMs                                                           int64
	Score                                                              int
	Images                                                             []model.CommentImage
}

func socialPostBody(p SocialPost) map[string]any {
	shark := map[string]any{"enter_from": "MainFragmentActivity", "previous_page": "", "page_list": "MainFragmentActivity"}
	business := map[string]any{"ignore_urge_rule": false, "text_feature": map[string]any{}, "book_id": p.SeriesID, "shark_param": shark, "has_aigc_content": false, "offset": p.OffsetMs, "log_extra": map[string]any{}}
	group, groupType, source := p.VideoID, 30, 3
	if p.Kind == 20 {
		source = 1500
		shark["type"] = "short_play"
		shark["aid"] = "8662"
		business["preset_text_id"] = p.PresetID
	}
	if p.CommentID != "" {
		source = 9
		if p.Kind == 20 {
			source = 1500
		}
	}
	if p.Kind == 2 {
		group, groupType, source = p.SeriesID, 1, 12
		if p.CommentID != "" {
			source = 13
		}
		business = map[string]any{"book_id": p.SeriesID, "score": p.Score, "offset": 0, "ignore_urge_rule": false, "has_aigc_content": false, "is_confirm_request": false, "read_item_cnt": 0, "from_famous_comment_id": 0, "support_para_audio_play": false, "text_feature": map[string]any{}, "video_is_muted": 0, "log_extra": map[string]any{}}
	}
	body := map[string]any{"commit_source": source, "group_id": group, "group_type": groupType, "data_type": p.Kind, "text": p.Text, "business_param": business, "aid": 8662}
	if p.CommentID != "" {
		body["reply_to_comment_id"] = p.CommentID
		if p.ReplyID != "" {
			body["reply_to_reply_id"] = p.ReplyID
		}
		if p.ReplyUserID != "" {
			body["reply_to_userid"] = p.ReplyUserID
		}
	}
	if len(p.Images) > 0 {
		images := []map[string]any{}
		for _, im := range p.Images {
			images = append(images, map[string]any{"web_uri": im.WebURI, "web_url": first(im.WebURL, im.URL), "expand_web_url": first(im.ExpandURL, im.Large), "width": im.Width, "height": im.Height})
		}
		body["image_data"] = images
	}
	return body
}
func (c *Client) PublishSocial(ctx context.Context, p SocialPost) (*model.Comment, error) {
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" {
		return nil, errors.New("请填写内容")
	}
	if !numericID(p.SeriesID) || (p.Kind != 2 && !numericID(p.VideoID)) {
		return nil, errors.New("发言对象无效")
	}
	if p.Kind != 2 && p.Kind != 4 && p.Kind != 20 {
		return nil, errors.New("发言类型无效")
	}
	if p.Kind == 2 && p.CommentID == "" && (p.Score < 1 || p.Score > 10) {
		return nil, errors.New("请先为剧集评分")
	}
	if p.CommentID != "" && !numericID(p.CommentID) {
		return nil, errors.New("回复对象无效")
	}
	action := "comment_add"
	if p.CommentID != "" {
		action = "reply_add"
	}
	res, err := c.sess.CallJSON(ctx, action, socialPostBody(p))
	if err != nil {
		return nil, err
	}
	raw, err := res.DecodeData()
	if err != nil {
		return nil, err
	}
	v, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	m := object(v)
	cm := object(m["comment"])
	if cm == nil {
		cm = object(m["reply"])
	}
	if cm == nil {
		cm = object(m["comment_info"])
	}
	if cm == nil {
		cm = m
	}
	id := text(cm, "comment_id", "reply_id")
	if id == "" {
		return nil, errors.New("服务端未返回已发布内容的 ID，请刷新确认")
	}
	return &model.Comment{ID: id, SeriesID: p.SeriesID, VideoID: p.VideoID, Text: p.Text, DataType: p.Kind, OffsetMs: int(p.OffsetMs), ParentID: p.CommentID, Score: float64(p.Score), Images: p.Images}, nil
}
