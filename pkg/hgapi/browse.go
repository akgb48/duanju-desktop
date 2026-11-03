package hgapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"duanju-desktop/pkg/model"
)

// A tab's has_more describes its layout, not the recommendation stream.
// The stream continues through its refreshable cells and their own cursors.
type feedCellCursor struct {
	ID      string `json:"id"`
	Plan    string `json:"plan,omitempty"`
	Offset  string `json:"offset"`
	Session string `json:"session,omitempty"`
}
type browseCursor struct {
	Kind    string           `json:"kind"`
	Offset  int              `json:"offset,omitempty"`
	Session string           `json:"session,omitempty"`
	Round   int              `json:"round,omitempty"`
	Cells   []feedCellCursor `json:"cells,omitempty"`
}

func encodeBrowseCursor(v browseCursor) string { b, _ := json.Marshal(v); return string(b) }
func decodeBrowseCursor(raw, kind string) browseCursor {
	v := browseCursor{Kind: kind}
	if strings.HasPrefix(raw, "{") && len(raw) < 32768 {
		var parsed browseCursor
		if json.Unmarshal([]byte(raw), &parsed) == nil && parsed.Kind == kind && len(parsed.Cells) <= 16 {
			return parsed
		}
	}
	v.Offset, _ = strconv.Atoi(raw)
	if v.Offset < 0 {
		v.Offset = 0
	}
	return v
}
func (c *Client) browseData(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	res, err := c.sess.CallJSON(ctx, action, params)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = res.Decode(&raw); err != nil {
		return nil, err
	}
	value, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	data, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("上游内容格式错误")
	}
	return data, nil
}
func (c *Client) parseBrowseData(data map[string]any) (*model.FeedPage, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return c.parseVideoFeed(b)
}
func (c *Client) recommendationPage(ctx context.Context, tab int, rawCursor string) (*model.FeedPage, error) {
	state := decodeBrowseCursor(rawCursor, "recommend")
	next := browseCursor{Kind: "recommend", Round: state.Round + 1}
	out := &model.FeedPage{Items: []model.FeedItem{}}
	if len(state.Cells) > 0 {
		for _, cell := range state.Cells {
			params := map[string]any{"bottom_tab_type": "7", "client_req_type": "2", "client_template": "7", "change_type": "0", "category_id": "0", "cell_sub_id": "0", "limit": "12", "need_personal_recommend": "1", "video_type_preferences_str": "[]", "cell_id": cell.ID, "plan_id": cell.Plan, "tab_type": strconv.Itoa(tab), "offset": first(cell.Offset, "0"), "session_id": cell.Session}
			data, err := c.browseData(ctx, "feed_change", params)
			if err != nil {
				return nil, err
			}
			page, err := c.parseBrowseData(data)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, page.Items...)
			meta := paginationData(data)
			if truth(meta, "has_more") {
				cell.Offset = first(text(meta, "next_offset"), strconv.Itoa(numberString(cell.Offset)+len(page.Items)))
				cell.Session = first(text(meta, "session_id"), cell.Session)
				next.Cells = append(next.Cells, cell)
			}
		}
	} else {
		data, err := c.browseData(ctx, "feed", feedTabParams(tab, ""))
		if err != nil {
			return nil, err
		}
		selected := data
		if tabs, ok := data["tab_item"].([]any); ok {
			selected = map[string]any{}
			for _, v := range tabs {
				if item, ok := v.(map[string]any); ok && number(item, "tab_type") == tab {
					selected = item
					break
				}
			}
		}
		page, err := c.parseBrowseData(selected)
		if err != nil {
			return nil, err
		}
		out = page
		var collect func(any)
		collect = func(v any) {
			switch x := v.(type) {
			case []any:
				for _, child := range x {
					collect(child)
				}
			case map[string]any:
				id := text(x, "cell_id_str", "cell_id")
				if id != "" && id != "0" && (truth(x, "use_recommend") || number(x, "cell_operation_type") == 1) {
					next.Cells = append(next.Cells, feedCellCursor{ID: id, Plan: text(x, "plan_id"), Offset: "0", Session: first(text(x, "session_id"), text(selected, "session_id"))})
					return
				}
				if children, ok := x["cell_data"]; ok {
					collect(children)
				}
			}
		}
		collect(selected)
	}
	// Exhausting a refreshable batch renews the upstream recommendation layout.
	// No local watch count, page count or lifetime cap is imposed.
	out.HasMore = tab != 45 || len(next.Cells) > 0
	out.Cursor = encodeBrowseCursor(next)
	out.TotalCount = len(out.Items)
	return out, nil
}
func numberString(s string) int { n, _ := strconv.Atoi(s); return n }
func paginationData(data map[string]any) map[string]any {
	if cell, ok := data["cell_view"].(map[string]any); ok {
		if _, exists := cell["next_offset"]; exists {
			return mergeMaps(data, cell)
		}
	}
	return data
}

func rankArgs(filters map[string]string, state browseCursor) map[string]any {
	change := "2"
	if state.Offset > 0 {
		change = "1"
	}
	args := map[string]any{"tab_type": "26", "client_req_type": "2", "client_template": "2", "background_selected_items": "", "category_id": "0", "limit": "0", "cell_sub_id": "0", "gender": "2", "cell_id": "7470092475068071998", "selected_items": filters["rank"], "sub_selected_items": filters["rank_sub"], "panel_selected_items": filters["rank_panel"], "unlimited_selector_change_type": change, "offset": strconv.Itoa(state.Offset)}
	if state.Session != "" {
		args["session_id"] = state.Session
	}
	return args
}
func (c *Client) RankSelected(ctx context.Context, filters map[string]string, cursor string) (*model.FeedPage, error) {
	state := decodeBrowseCursor(cursor, "rank")
	data, err := c.browseData(ctx, "rank_change", rankArgs(filters, state))
	if err != nil {
		return nil, err
	}
	page, err := c.parseBrowseData(data)
	if err != nil {
		return nil, err
	}
	meta := paginationData(data)
	page.HasMore = truth(meta, "has_more")
	page.Cursor = encodeBrowseCursor(browseCursor{Kind: "rank", Offset: number(meta, "next_offset"), Session: text(meta, "session_id")})
	return page, nil
}
func (c *Client) NewSeriesPage(ctx context.Context, gender, cursor string) (*model.FeedPage, error) {
	if gender != "0" && gender != "1" {
		gender = "2"
	}
	state := decodeBrowseCursor(cursor, "new")
	params := map[string]any{"change_type": "1", "selected_items": "firstonlinetime_new", "client_req_type": "1", "cell_id": "7431550523368554558", "limit": "18", "offset": strconv.Itoa(state.Offset), "cell_gender": gender}
	if state.Session != "" {
		params["session_id"] = state.Session
	}
	data, err := c.browseData(ctx, "new_series", params)
	if err != nil {
		return nil, err
	}
	page, err := c.parseBrowseData(data)
	if err != nil {
		return nil, err
	}
	meta := paginationData(data)
	page.HasMore = truth(meta, "has_more")
	page.Cursor = encodeBrowseCursor(browseCursor{Kind: "new", Offset: number(meta, "next_offset"), Session: text(meta, "session_id")})
	return page, nil
}

type rankSelector struct {
	Outer selectorRow   `json:"outer_row"`
	Inner []selectorRow `json:"inner_rows"`
}
type selectorRow struct {
	Type  string         `json:"type"`
	Name  string         `json:"row_name"`
	Items []selectorItem `json:"items"`
}
type selectorItem struct {
	ID    string        `json:"selector_item_id"`
	Name  string        `json:"show_name"`
	Desc  string        `json:"sub_title"`
	Sub   *rankSelector `json:"sub_cell_selector"`
	Panel *rankSelector `json:"panel_selector"`
}

func (c *Client) rankPanel(ctx context.Context) (any, error) {
	data, err := c.browseData(ctx, "rank_change", rankArgs(nil, browseCursor{}))
	if err != nil {
		return nil, err
	}
	cell, _ := data["cell_view"].(map[string]any)
	b, err := json.Marshal(cell["cell_selector"])
	if err != nil {
		return nil, err
	}
	var selector rankSelector
	if err = json.Unmarshal(b, &selector); err != nil {
		return nil, err
	}
	groups := []map[string]any{}
	for _, category := range selector.Outer.Items {
		subs := []map[string]any{}
		if category.Sub != nil {
			for _, sub := range category.Sub.Outer.Items {
				panel := []map[string]any{}
				if sub.Panel != nil {
					for _, row := range sub.Panel.Inner {
						options := []map[string]string{}
						for _, option := range row.Items {
							if option.ID != "" {
								options = append(options, map[string]string{"id": option.ID, "name": option.Name})
							}
						}
						if len(options) > 0 {
							panel = append(panel, map[string]any{"key": row.Type, "name": row.Name, "options": options})
						}
					}
				}
				subs = append(subs, map[string]any{"id": sub.ID, "name": sub.Name, "desc": sub.Desc, "panel": panel})
			}
		}
		groups = append(groups, map[string]any{"id": category.ID, "name": category.Name, "subs": subs})
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("上游未返回排行榜分类")
	}
	return groups, nil
}
