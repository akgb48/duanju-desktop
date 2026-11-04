package hgapi

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

type ReservationItem struct {
	ItemID              string `json:"itemId,omitempty"`
	SeriesID            string `json:"seriesId"`
	Title               string `json:"title"`
	Cover               string `json:"cover"`
	Intro               string `json:"intro"`
	Category            string `json:"category"`
	EpisodeCount        int    `json:"episodeCount"`
	Online              bool   `json:"online"`
	Subscribed          bool   `json:"subscribed"`
	ExpectedPublishTime int64  `json:"expectedPublishTime"`
	SchedulePublishTime int64  `json:"schedulePublishTime"`
	Hint                string `json:"hint"`
	Badge               string `json:"badge"`
	Note                string `json:"note"`
}

type ReservationPage struct {
	Items        []ReservationItem `json:"items"`
	Cursor       string            `json:"cursor"`
	HasMore      bool              `json:"hasMore"`
	OnlineTotal  int               `json:"onlineTotal"`
	OfflineTotal int               `json:"offlineTotal"`
}

func (c *Client) reservationItem(entry map[string]any, online, subscribed bool) (ReservationItem, bool) {
	itemID := text(entry, "item_id")
	video := object(entry["subscribe_data"])
	var fallbackID string
	if video == nil {
		// Pending releases can return a flat record without video details.
		if !numericID(itemID) || itemID == "0" || text(entry, "name") == "" {
			return ReservationItem{}, false
		}
		video, fallbackID = entry, itemID
		if detail, err := url.Parse(text(entry, "detail_schema")); err == nil &&
			detail.Scheme == "dragon8662" && detail.Hostname() == "shortSeriesDetail" {
			if id := detail.Query().Get("series_id"); numericID(id) && id != "0" {
				fallbackID = id
			}
		}
	}
	if detail := object(video["video_detail"]); detail != nil {
		video = mergeMaps(video, detail)
	}
	series := parseSeries(video, fallbackID)
	if series.ID == "" || series.ID == "0" {
		return ReservationItem{}, false
	}
	if vid := text(video, "vid", "first_vid"); vid != "" && series.ContentType > 0 {
		c.contentTypes.Store(vid, series.ContentType)
	}
	if entry["is_online"] != nil {
		online = truth(entry, "is_online")
	}
	for _, key := range []string{"has_subscribed", "is_subscribed", "is_subscribe", "subscribed"} {
		if entry[key] != nil {
			subscribed = truth(entry, key)
			break
		}
	}
	return ReservationItem{
		ItemID: itemID, SeriesID: series.ID, Title: first(series.Title, text(entry, "name")),
		Cover: first(series.Cover, text(entry, "cover")), Intro: first(series.Description, text(entry, "item_desc")),
		Category: first(text(entry, "category"), series.Category), EpisodeCount: series.EpisodeCnt,
		Online: online, Subscribed: subscribed,
		ExpectedPublishTime: integer64(entry, "expected_publish_time"),
		SchedulePublishTime: integer64(entry, "schedule_publish_time"),
		Hint:                text(entry, "hint", "publish_time_text", "expected_rec_text"), Badge: series.Badge, Note: text(entry, "note"),
	}, true
}

func (c *Client) Reservations(ctx context.Context, online bool, cursor string) (*ReservationPage, error) {
	const pageSize = 20
	offset, err := strconv.Atoi(first(cursor, "0"))
	if err != nil || offset < 0 {
		return nil, errors.New("预约分页位置无效")
	}
	data, err := c.browseData(ctx, "reservation_list", map[string]any{
		"tab_type": "13", "subscribe_order_type": "0", "swipe_type": "0",
		"need_calendar_schema": "false", "offset": offset, "subscribe_offset": offset,
		"limit": pageSize, "is_online": online,
	})
	if err != nil {
		return nil, err
	}
	page := &ReservationPage{
		Items: []ReservationItem{}, HasMore: truth(data, "has_more"),
		OnlineTotal: number(data, "online_total_count"), OfflineTotal: number(data, "offline_total_count"),
	}
	rows, ok := data["subscribe_items"].([]any)
	if !ok && data["subscribe_items"] != nil {
		return nil, errors.New("预约列表响应格式错误")
	}
	for _, row := range rows {
		if item, ok := c.reservationItem(object(row), online, true); ok {
			page.Items = append(page.Items, item)
		}
	}
	if len(rows) != 0 && len(page.Items) == 0 {
		return nil, errors.New("预约列表缺少有效剧集信息")
	}
	if page.HasMore {
		next := number(data, "next_offset")
		if next <= offset {
			next = offset + pageSize
		}
		page.Cursor = strconv.Itoa(next)
	}
	return page, nil
}

func (c *Client) SetReservation(ctx context.Context, seriesID string, on bool) error {
	if !numericID(seriesID) {
		return errors.New("剧集 ID 无效")
	}
	op := 2
	if on {
		op = 1
	}
	return c.updateReservation(ctx, "uncover_subscribe", map[string]any{
		"item_id": seriesID, "item_type": 1, "op_type": op, "wish_list_all_del": 0,
		"shark_param": map[string]string{
			"enter_from": "BulletActivity", "previous_page": "MainFragmentActivity",
			"page_list": "MainFragmentActivity,BulletActivity",
		},
	})
}

func (c *Client) DeleteReservations(ctx context.Context, ids []string, online bool) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if !numericID(id) {
			return errors.New("预约记录 ID 无效")
		}
	}
	return c.updateReservation(ctx, "subscribe_delete", map[string]any{
		"item_id": ids, "all_select": false, "not_del_item_id": []string{},
		"is_online": online, "tab_type": 13,
	})
}

func (c *Client) updateReservation(ctx context.Context, action string, body map[string]any) error {
	res, err := c.sess.CallJSON(ctx, action, body)
	if err != nil {
		return err
	}
	if !res.Envelope.OK() {
		_, err = res.DecodeData()
	}
	return err
}
