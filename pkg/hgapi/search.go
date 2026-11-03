package hgapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"duanju-desktop/pkg/model"
)

// Search continuation belongs to one keyword/tab. Other empty search tabs also
// contain next_offset=0, so feed's recursive metadata collector is unsuitable.
type searchCursor struct {
	Keyword  string `json:"k"`
	Tab      int    `json:"t"`
	Offset   string `json:"o"`
	SearchID string `json:"s,omitempty"`
	Passback string `json:"p,omitempty"`
}

func decodeSearchCursor(keyword, cursor string) searchCursor {
	state := searchCursor{Keyword: keyword, Tab: 11, Offset: "0"}
	if strings.HasPrefix(cursor, "{") {
		var saved searchCursor
		if json.Unmarshal([]byte(cursor), &saved) == nil && saved.Keyword == keyword && saved.Tab > 0 && saved.Offset != "" {
			return saved
		}
	} else if n, err := strconv.Atoi(cursor); err == nil && n >= 0 {
		state.Offset = strconv.Itoa(n)
	}
	return state
}

func (c *Client) Search(ctx context.Context, keyword, cursor string) (*model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	out := &model.SearchResult{Keyword: keyword, Series: []model.Series{}, Source: "server"}
	if keyword == "" {
		return out, nil
	}
	state := decodeSearchCursor(keyword, cursor)
	params := map[string]any{"query": keyword, "count": 20, "offset": state.Offset, "tab_type": state.Tab, "use_correct": true, "bookshelf_search_plan": "4"}
	if state.SearchID != "" {
		params["search_id"] = state.SearchID
	}
	if state.Passback != "" {
		params["passback"] = state.Passback
	}
	res, err := c.sess.CallJSON(ctx, "search", params)
	if err != nil {
		return nil, err
	}
	if !res.Envelope.OK() {
		_, err = res.DecodeData()
		return nil, err
	}
	return c.parseSearch(res.Raw, state)
}

func (c *Client) parseSearch(raw []byte, state searchCursor) (*model.SearchResult, error) {
	decoded, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("搜索响应格式无效")
	}
	if nested, ok := root["data"].(map[string]any); ok {
		root = nested
	}
	meta := root
	if tabs, ok := root["search_tabs"].([]any); ok {
		var selected map[string]any
		for _, v := range tabs {
			if tab, ok := v.(map[string]any); ok && number(tab, "tab_type") == state.Tab {
				selected = tab
				break
			}
		}
		if selected == nil {
			for _, v := range tabs {
				if tab, ok := v.(map[string]any); ok && text(tab, "search_target_channel") == "xs_short_play" {
					selected = tab
					break
				}
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("搜索响应缺少短剧结果页签")
		}
		meta = selected
	}
	payload := any(meta)
	if data, ok := meta["data"]; ok {
		payload = data
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	page, err := c.parseVideoFeed(encoded)
	if err != nil {
		return nil, err
	}
	out := &model.SearchResult{Keyword: state.Keyword, Series: []model.Series{}, Source: "server", HasMore: truth(meta, "has_more")}
	seen := map[string]bool{}
	for _, item := range page.Items {
		if item.Series != nil && !seen[item.Series.ID] {
			seen[item.Series.ID] = true
			out.Series = append(out.Series, *item.Series)
		}
	}
	out.Total = number(meta, "total", "total_count")
	if out.Total == 0 {
		out.Total = len(out.Series)
	}
	next := state
	next.SearchID = first(text(meta, "search_id"), state.SearchID)
	next.Passback = first(text(meta, "passback"), state.Passback)
	if _, ok := meta["next_offset"]; ok {
		next.Offset = text(meta, "next_offset")
	} else if out.HasMore {
		count := len(out.Series)
		if list, ok := payload.([]any); ok {
			count = len(list)
		}
		next.Offset = strconv.Itoa(numberString(state.Offset) + count)
	}
	if out.HasMore && (next.Offset != state.Offset || next.Passback != state.Passback) {
		b, _ := json.Marshal(next)
		out.Cursor = string(b)
	} else {
		out.HasMore = false
	}
	return out, nil
}
