package hgapi

import (
	"encoding/json"
	"testing"
)

func TestSeriesTitleTagPreservesServerTextColorsAndFallbackReasonJSON(t *testing.T) {
	for _, raw := range []string{
		`{"tag_info":{"text":"爆剧","bg_color":["#FF6868","#FF4C6DF7","#EFB662"]}}`,
		`{"recommend_reason_list":"{\"tag_info\":{\"text\":\"爆剧\",\"bg_color\":[\"#FF6868\",\"#FF4C6DF7\",\"#EFB662\"]}}"}`,
	} {
		var data map[string]any
		json.Unmarshal([]byte(raw), &data)
		series := parseSeries(data, "s")
		if series.TitleTag == nil || series.TitleTag.Text != "爆剧" || len(series.TitleTag.Colors) != 3 || series.TitleTag.Source != "server" {
			t.Fatalf("server title tag lost: %+v", series)
		}
	}
	var data map[string]any
	json.Unmarshal([]byte(`{"tag_info":{"text":"新剧","bg_color":["#00AE8314","url(bad)"],"text_color":"#00AE83"},"category_schema":"[{\"name\":\"都市\"},{\"name\":\"系统\"}]"}`), &data)
	series := parseSeries(data, "s")
	if len(series.TitleTag.Colors) != 1 || series.TitleTag.TextColor != "#00AE83" || series.Category != "都市" || len(series.Tags) != 2 {
		t.Fatalf("tag style or categories lost: %+v", series)
	}
	if parseSeries(map[string]any{}, "s").TitleTag != nil {
		t.Fatal("invented server title tag")
	}
}
