package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func discoveryWire(prefix string) []map[string]any {
	items := []map[string]any{}
	for i := 0; i < 20; i++ {
		id := prefix + itoa(i)
		if i == 0 && (prefix == "n" || prefix == "h") {
			id = "shared"
		}
		v := map[string]any{"series_id": id, "title": id, "vid": "video_" + id}
		if prefix == "r" && i == 8 {
			v["tag_info"] = map[string]any{"text": "爆剧", "bg_color": []string{"#FF6868", "#EFB662"}}
		}
		if prefix == "r" && i == 9 {
			v["series_id"] = "shared"
			v["tag_info"] = map[string]any{"text": "爆剧", "bg_color": []string{"#FF6868"}}
		}
		if prefix == "n" && i == 2 {
			v["tag_info"] = map[string]any{"text": "新剧首发"}
		}
		items = append(items, v)
	}
	return items
}

func TestRecommendationsMixOfficialSourcesKeepPendingAndDeduplicateAcrossPages(t *testing.T) {
	var hits atomic.Int32
	s := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		var data any
		switch {
		case strings.HasSuffix(r.URL.Path, "/cell/change/v1/"):
			if q.Get("selected_items") != "firstonlinetime_new" {
				t.Error("new-release source mismatch")
			}
			data = map[string]any{"has_more": true, "next_offset": 20, "session_id": "N", "cell_view": map[string]any{"video_data": discoveryWire("n")}}
		case strings.HasSuffix(r.URL.Path, "/cell/change/v"):
			if q.Get("selected_items") != "ranklist_hot_play_sc" {
				t.Error("recommendation rank used instead of official hot-play rank")
			}
			data = map[string]any{"has_more": true, "next_offset": 20, "session_id": "H", "cell_view": map[string]any{"video_data": discoveryWire("h")}}
		case strings.HasSuffix(r.URL.Path, "/tab/v"):
			if q.Get("tab_type") != "16" {
				t.Error("general recommendation tab mismatch")
			}
			data = map[string]any{"tab_item": []any{map[string]any{"tab_type": 16, "video_data": discoveryWire("r")}}}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	})
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		p, err := s.Recommend(context.Background(), 16, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Items) != 12 || !p.HasMore || p.Cursor == cursor {
			t.Fatalf("lost pending items or stopped early: %+v", p)
		}
		if page == 0 && p.Items[2].SeriesID != "r8" {
			t.Fatal("server-tagged burst drama was not prioritized")
		}
		sources := map[string]int{}
		for _, item := range p.Items {
			if seen[item.SeriesID] {
				t.Fatalf("repeated drama %s", item.SeriesID)
			}
			if item.SeriesID == "shared" && item.Series.TitleTag.Text != "爆剧" {
				t.Fatal("deduplication discarded burst tag from another source")
			}
			seen[item.SeriesID] = true
			sources[item.RecommendSource]++
			if item.RecommendSource == "hot" && (item.Series.TitleTag == nil || item.Series.TitleTag.Text != "热剧") {
				t.Fatal("hot-play source tag missing")
			}
			if item.SeriesID == "n2" && item.Series.TitleTag.Text != "新剧首发" {
				t.Fatal("server badge overwritten by source badge")
			}
		}
		if len(sources) != 3 {
			t.Fatalf("source diversity lost: %v", sources)
		}
		again, err := s.Recommend(context.Background(), 16, cursor)
		if err != nil || again.Cursor != p.Cursor {
			t.Fatal("repeat/prefetch request advanced the same cursor twice")
		}
		cursor = p.Cursor
	}
	if hits.Load() != 3 {
		t.Fatalf("pending source pages were refetched: %d requests", hits.Load())
	}
}

func TestRecommendationsFallBackWhenNewSourceFailsAndKeepFollowTab(t *testing.T) {
	var followCalls atomic.Int32
	s := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		var data any
		if strings.HasSuffix(r.URL.Path, "/cell/change/v1/") {
			http.Error(w, "new releases unavailable", 502)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tab/v") {
			tab := 16
			prefix := "r"
			if r.URL.Query().Get("tab_type") == "45" {
				tab = 45
				prefix = "follow"
				followCalls.Add(1)
			}
			data = map[string]any{"tab_item": []any{map[string]any{"tab_type": tab, "video_data": discoveryWire(prefix)}}}
		} else {
			data = map[string]any{"has_more": false, "cell_view": map[string]any{"video_data": discoveryWire("h")}}
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	})
	p, err := s.Recommend(context.Background(), 16, "")
	if err != nil || len(p.Items) != 12 {
		t.Fatalf("one source failure blocked working sources: %v", err)
	}
	for _, item := range p.Items {
		if item.RecommendSource == "new" {
			t.Fatal("invented failed source results")
		}
	}
	follow, err := s.Recommend(context.Background(), 45, "")
	if err != nil || followCalls.Load() != 1 {
		t.Fatal("follow tab changed into discovery mix")
	}
	for _, item := range follow.Items {
		if !strings.HasPrefix(item.SeriesID, "follow") || item.RecommendSource != "" {
			t.Fatal("unfollowed content inserted in follow tab")
		}
	}
}
