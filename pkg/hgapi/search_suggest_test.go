package hgapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSearchSuggestParsesRichAndPlainItemsWithoutTurningIDsIntoWords(t *testing.T) {
	s := sessionForJSON(t, func(r *http.Request) string {
		if r.URL.Path != "/reading/bookapi/search/suggest/v" || r.URL.Query().Get("query") != "总裁" {
			t.Fatal("suggestion endpoint/query mismatch")
		}
		return `{"code":0,"data":{"query_result":["总裁与她","总裁回家"],"query_result_v2":[{"name":"总裁与她","keyword":"9007199254740993123","word_type_str":"short_play_name","sug_abstract":"都市·热度","search_high_light":{"rich_text":"<em>总裁</em>与她"},"video_data":{"series_id":9007199254740993123,"cover":"https://cover.example/drama.jpg"}},{"name":"总裁回家","keyword":"总裁回家","word_type_str":"title"}]}}`
	})
	items, err := New(s).SearchSuggest(context.Background(), " 总裁 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Word != "总裁与她" || items[0].SeriesID != "9007199254740993123" || items[0].Cover == "" || items[0].Abstract == "" {
		t.Fatalf("rich suggestion lost: %+v", items)
	}
	if len(items[0].Parts) != 2 || items[0].Parts[0].Text != "总裁" || !items[0].Parts[0].HL || items[1].SeriesID != "" {
		t.Fatal("suggestion highlight or keyword-only navigation incorrect")
	}
}

func TestSuggestionHighlightsAreUnicodeSafeAndStayText(t *testing.T) {
	for _, tc := range []struct{ word, keyword, rich string }{{"总裁😀与总裁", "总裁", ""}, {"总裁 & 她", "总裁", "<em>总裁</em> &amp; 她"}, {"总裁<script>故事", "总裁", "<script>bad()</script>"}} {
		parts := searchHighlights(tc.word, tc.keyword, tc.rich)
		var text strings.Builder
		highlighted := false
		for _, p := range parts {
			text.WriteString(p.Text)
			highlighted = highlighted || p.HL
		}
		if text.String() != tc.word || !highlighted {
			t.Fatalf("unsafe or broken highlight: %+v", parts)
		}
	}
}

func TestSuggestionNeverUsesGenericPicURLAsDramaCover(t *testing.T) {
	data := map[string]any{"query_result_v2": []any{
		map[string]any{"name": "糯糯下山", "keyword": "7677801492920667198", "word_type_str": "short_play_name", "pic_url": "https://example.test/novel-static/magnifier.png"},
		map[string]any{"name": "糯糯下山第二季", "keyword": "7680883072278989849", "word_type_str": "short_play_name", "pic_url": "https://example.test/novel-static/magnifier.png", "video_data": map[string]any{"series_id": "7680883072278989849", "cover": "https://cover.example/season2.jpg"}},
		map[string]any{"name": "云糯糯全集", "word_type_str": "common_query", "pic_url": "https://example.test/novel-static/magnifier.png"},
	}}
	items := parseSearchSuggestions(data, "糯糯")
	if len(items) != 3 || items[0].SeriesID == "" || items[0].Cover != "" || items[1].Cover != "https://cover.example/season2.jpg" || items[2].SeriesID != "" || items[2].Cover != "" {
		t.Fatalf("placeholder mistaken for a poster or keyword assigned a fake series: %+v", items)
	}
}
