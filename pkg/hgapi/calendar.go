package hgapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type CalendarItem = ReservationItem

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
			if item, ok := c.reservationItem(object(value), false, false); ok {
				page.Items = append(page.Items, item)
			}
		}
	}
	page.Cursor = encodeBrowseCursor(browseCursor{Kind: "calendar", Offset: number(data, "next_offset"), Session: text(data, "session_id")})
	return page, nil
}
