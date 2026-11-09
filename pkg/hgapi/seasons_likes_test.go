package hgapi

import (
	"compress/gzip"
	"context"
	"duanju-desktop/pkg/api"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSeasonsUseRealMappingAndSkipUnavailableNextSeason(t *testing.T) {
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/reading/bookapi/plan/v" || q.Get("book_id") != socialSID || q.Get("filter_ids") != socialSID || q.Get("scene") != "10" {
			t.Fatalf("incorrect seasons request %s %s", r.Method, r.URL)
		}
		if q.Get("from") == "single_col_same_ip_series" {
			return socialResponse(`{"code":0,"data":[{"video_data":[{"series_id":9007199254740993123,"title":"第一季","vid":"101","episode_cnt":12,"secondary_info_list":[{"content":"第1季"}]},{"series_id":"202","title":"第二季","episode_cnt":0,"disable_play":true,"subscribe_item":{"is_subscribed":true}},{"series_id":"202","title":"重复"}]}]}`), nil
		}
		if q.Get("from") != "series_short_play_info" {
			t.Fatal("wrong next season source")
		}
		return socialResponse(`{"code":0,"data":[{"video_data":[{"series_id":"202","vid":"102","season_index":2,"episode_cnt":10,"disable_play":true},{"series_id":"303","vid":"103","season_index":3,"episode_cnt":10},{"series_id":"404","vid":"104","season_index":4,"episode_cnt":10}]}]}`), nil
	})}))
	c := New(s)
	seasons, err := c.SeriesSeasons(context.Background(), socialSID)
	if err != nil || len(seasons) != 2 || seasons[0].SeriesID != "9007199254740993123" || seasons[0].Label != "第1季" || !seasons[1].Upcoming || !seasons[1].Reserved {
		t.Fatalf("seasons: %+v %v", seasons, err)
	}
	next, err := c.NextSeason(context.Background(), socialSID, 1)
	if err != nil || next == nil || next.SeriesID != "303" {
		t.Fatalf("next: %+v %v", next, err)
	}
	next, err = c.NextSeason(context.Background(), socialSID, 4)
	if err != nil || next != nil {
		t.Fatalf("final season loops: %+v %v", next, err)
	}
}

func TestLikesUsesRecordPaginationAndVideoActionBody(t *testing.T) {
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			q := r.URL.Query()
			if q.Get("object_type_list") != "6,15,10" || q.Get("action_type") != "3" || q.Get("count") != "100" || q.Get("offset") != "200" {
				t.Fatalf("wrong likes request %s", r.URL)
			}
			return socialResponse(`{"code":0,"data":{"mixed_data_list":[{"video_data":{"series_id":9007199254740993123,"vid":9007199254740993124,"vid_index":7,"content_type":1004,"video_category_type":"ai_video","video_detail":{"title":"AI 剧集"}}}],"has_more":true,"next_offset":200}}`), nil
		}
		if r.URL.Path != "/novel/articleapi/do_action/v1/" {
			t.Fatal("video like used comment endpoint")
		}
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var body map[string]any
		if err := json.NewDecoder(reader).Decode(&body); err != nil {
			t.Fatal(err)
		}
		business := object(body["business_param"])
		if number(body, "action_type") != 3 || number(body, "object_type") != 6 || text(body, "object_id") != socialVID || text(business, "video_id") != socialSID {
			t.Fatalf("incorrect like body: %+v", body)
		}
		return socialResponse(`{"code":0,"data":{}}`), nil
	})}))
	c := New(s)
	page, err := c.Likes(context.Background(), "200", "")
	if err != nil || len(page.Items) != 1 || page.Cursor != "300" || page.Items[0].VideoID != "9007199254740993124" || page.Items[0].Series.Kind != "ai" || !page.Items[0].Series.Liked {
		t.Fatalf("likes: %+v %v", page, err)
	}
	if err := c.SetLike(context.Background(), socialSID, socialVID, true); err != nil {
		t.Fatal(err)
	}
}
