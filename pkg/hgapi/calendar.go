package hgapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"hongguo-desktop/pkg/model"
)

type CalendarItem struct {
	SeriesID     string `json:"seriesId"`
	Title        string `json:"title"`
	Cover        string `json:"cover"`
	Intro        string `json:"intro"`
	Category     string `json:"category"`
	EpisodeCount int    `json:"episodeCount"`
	Online       bool   `json:"online"`
	Subscribed   bool   `json:"subscribed"`
	Hint         string `json:"hint"`
	Badge        string `json:"badge"`
	Note         string `json:"note"`
}
type CalendarPage struct {
	Items   []CalendarItem `json:"items"`
	Dates   []string       `json:"dates"`
	Today   string         `json:"today"`
	Date    string         `json:"date"`
	Cursor  string         `json:"cursor"`
	HasMore bool           `json:"hasMore"`
}

func (c *Client) CalendarPage(ctx context.Context, date, cursor string) (*CalendarPage, error) {
	if date != "" {
		if len(date) != 8 || strings.Trim(date, "0123456789") != "" {
			return nil, fmt.Errorf("上新日期格式应为 YYYYMMDD")
		}
	}
	state := decodeBrowseCursor(cursor, "calendar")
	args := map[string]any{"tab_type": "5", "tab_style": "2", "active_panel": "6", "gender_type": "2", "need_calendar_schema": "true"}
	if date != "" {
		args["target_date"] = date
	}
	if state.Offset > 0 {
		args["offset"] = strconv.Itoa(state.Offset)
		args["subscribe_offset"] = strconv.Itoa(state.Offset)
	}
	if state.Session != "" {
		args["session_id"] = state.Session
	}
	data, err := c.browseData(ctx, "reservation_list", args)
	if err != nil {
		return nil, err
	}
	page := &CalendarPage{Items: []CalendarItem{}, Dates: []string{}, Date: date, HasMore: truth(data, "has_more")}
	if schema, ok := data["calendar_schema"].(map[string]any); ok {
		page.Today = text(schema, "current_date")
		if page.Date == "" {
			page.Date = text(schema, "default_date")
		}
		if dates, ok := schema["date_list"].([]any); ok {
			for _, value := range dates {
				if d, ok := value.(string); ok {
					page.Dates = append(page.Dates, d)
				}
			}
		}
	}
	if items, ok := data["subscribe_items"].([]any); ok {
		for _, value := range items {
			entry, ok := value.(map[string]any)
			if !ok {
				continue
			}
			video, ok := entry["subscribe_data"].(map[string]any)
			if !ok {
				continue
			}
			if detail, ok := video["video_detail"].(map[string]any); ok {
				video = mergeMaps(video, detail)
			}
			s := parseSeries(video, "")
			if s.ID == "" {
				continue
			}
			if vid := text(video, "vid", "first_vid"); vid != "" && s.ContentType > 0 {
				c.contentTypes.Store(vid, s.ContentType)
			}
			item := calendarItem(s, entry)
			page.Items = append(page.Items, item)
		}
	}
	page.Cursor = encodeBrowseCursor(browseCursor{Kind: "calendar", Offset: number(data, "next_offset"), Session: text(data, "session_id")})
	return page, nil
}
func calendarItem(s *model.Series, entry map[string]any) CalendarItem {
	return CalendarItem{SeriesID: s.ID, Title: s.Title, Cover: s.Cover, Intro: s.Description, Category: text(entry, "category"), EpisodeCount: s.EpisodeCnt, Online: truth(entry, "is_online"), Subscribed: truth(entry, "is_subscribed", "subscribed"), Hint: text(entry, "hint", "publish_time_text"), Badge: s.Badge, Note: text(entry, "note")}
}
