package hgapi

import (
	"context"
	"html"
	"strings"
	"unicode"

	"duanju-desktop/pkg/model"
)

func (c *Client) SearchSuggest(ctx context.Context, keyword string) ([]model.SearchSuggestion, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []model.SearchSuggestion{}, nil
	}
	res, err := c.sess.CallJSON(ctx, "search_suggest", map[string]any{"query": keyword, "count": 10, "search_source": "search_bar", "tab_name": "short_video", "need_preload": false})
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
	return parseSearchSuggestions(data, keyword), nil
}

func parseSearchSuggestions(data any, keyword string) []model.SearchSuggestion {
	out := []model.SearchSuggestion{}
	seen := map[string]bool{}
	add := func(value any) {
		item := model.SearchSuggestion{}
		var rich string
		switch v := value.(type) {
		case string:
			item.Word = v
		case map[string]any:
			item.Word = text(v, "name", "word", "text", "title", "query")
			item.Abstract = text(v, "sug_abstract", "abstract")
			if video, ok := v["video_data"].(map[string]any); ok {
				item.SeriesID = text(video, "series_id_str", "series_id")
				item.Cover = text(video, "cover", "thumb_url")
				if item.Word == "" {
					item.Word = text(video, "title", "series_title")
				}
			}
			if item.SeriesID == "" {
				item.SeriesID = text(v, "series_id_str", "series_id", "seriesId")
			}
			if item.SeriesID == "" && text(v, "word_type_str") == "short_play_name" {
				item.SeriesID = text(v, "keyword")
			}
			if item.SeriesID != "" && item.Cover == "" {
				// pic_url is the generic magnifier, not this drama's poster.
				item.Cover = text(v, "cover", "series_cover", "cover_url")
			}
			if h, ok := v["search_high_light"].(map[string]any); ok {
				rich = text(h, "rich_text")
				if item.Word == "" {
					item.Word = text(h, "text")
				}
			}
		}
		item.Word = strings.TrimSpace(item.Word)
		if item.Word == "" || seen[item.Word] || len(out) >= 10 {
			return
		}
		seen[item.Word] = true
		item.Parts = searchHighlights(item.Word, keyword, rich)
		out = append(out, item)
	}
	if m, ok := data.(map[string]any); ok {
		// Read rich items first; the plain query_result list repeats their names.
		for _, key := range []string{"query_result_v2", "suggest_result", "query_result", "suggestions", "suggest_list", "sug_list", "words", "items"} {
			if list, ok := m[key].([]any); ok {
				for _, v := range list {
					add(v)
				}
			}
		}
	} else if list, ok := data.([]any); ok {
		for _, v := range list {
			add(v)
		}
	}
	return out
}

func searchHighlights(word, keyword, rich string) []model.SearchHighlight {
	// Preserve server highlighting as text spans only. No HTML is rendered.
	if rich != "" {
		parts := []model.SearchHighlight{}
		rest, highlight := rich, false
		for rest != "" {
			idx := strings.Index(rest, "<em>")
			marker := "<em>"
			if highlight {
				idx = strings.Index(rest, "</em>")
				marker = "</em>"
			}
			if idx < 0 {
				parts = append(parts, model.SearchHighlight{Text: html.UnescapeString(rest), HL: highlight})
				break
			}
			if idx > 0 {
				parts = append(parts, model.SearchHighlight{Text: html.UnescapeString(rest[:idx]), HL: highlight})
			}
			highlight = !highlight
			rest = rest[idx+len(marker):]
		}
		var joined strings.Builder
		for _, p := range parts {
			joined.WriteString(p.Text)
		}
		if joined.String() == word && len(parts) > 0 {
			return parts
		}
	}
	runes, needle := []rune(word), []rune(strings.TrimSpace(keyword))
	parts := []model.SearchHighlight{}
	start := 0
	if len(needle) > 0 {
		for i := 0; i+len(needle) <= len(runes); i++ {
			match := true
			for j, ch := range needle {
				if unicode.ToLower(ch) != unicode.ToLower(runes[i+j]) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if i > start {
				parts = append(parts, model.SearchHighlight{Text: string(runes[start:i])})
			}
			parts = append(parts, model.SearchHighlight{Text: string(runes[i : i+len(needle)]), HL: true})
			i += len(needle) - 1
			start = i + 1
		}
	}
	if start < len(runes) {
		parts = append(parts, model.SearchHighlight{Text: string(runes[start:])})
	}
	return parts
}
