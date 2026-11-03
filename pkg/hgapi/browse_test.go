package hgapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"duanju-desktop/pkg/api"
)

func TestRecommendationsContinuePastLayoutEndAndRenew(t *testing.T) {
	hits := 0
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		q := r.URL.Query()
		var body string
		switch hits {
		case 1, 4:
			if !strings.HasSuffix(r.URL.Path, "/tab/v") || q.Get("tab_type") != "16" {
				t.Fatalf("wrong layout request: %s", r.URL)
			}
			body = fmt.Sprintf(`{"code":0,"data":{"tab_item":[{"tab_type":16,"has_more":false,"session_id":"S1","cell_data":[{"cell_id":7294257141911650341,"cell_id_str":"7294257141911650341","use_recommend":true,"cell_operation_type":1,"cell_data":[{"video_data":[{"series_id":"%d","title":"首屏","vid":"v1"}]}]}]}]}}`, hits)
		case 2, 3:
			expectedOffset, expectedSession := "0", "S1"
			if hits == 3 {
				expectedOffset, expectedSession = "6", "S2"
			}
			if q.Get("cell_id") != "7294257141911650341" || q.Get("bottom_tab_type") != "7" || q.Get("offset") != expectedOffset || q.Get("session_id") != expectedSession {
				t.Fatalf("lost continuation: %s", r.URL)
			}
			body = fmt.Sprintf(`{"code":0,"data":{"has_more":%t,"next_offset":6,"session_id":"S2","cell_view":{"cell_data":[{"video_data":[{"series_id":"%d","title":"续页","vid":"v2"}]}]}}}`, hits == 2, hits)
		default:
			t.Fatal("unexpected extra background request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	c := New(sess)
	cursor := ""
	for i := 0; i < 4; i++ {
		page, err := c.Feed(context.Background(), 16, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || !page.HasMore || page.Cursor == cursor {
			t.Fatalf("stream stopped at page %d: %+v", i, page)
		}
		cursor = page.Cursor
	}
	if hits != 4 {
		t.Fatalf("requests=%d", hits)
	}
}

func TestRankPreservesSelectionAndNestedPagination(t *testing.T) {
	hits := 0
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		q := r.URL.Query()
		if q.Get("cell_id") != "7470092475068071998" || q.Get("selected_items") != "comic_series_rank" || q.Get("sub_selected_items") != "comic_series_hot_play" || q.Get("panel_selected_items") != "cate_1" {
			t.Fatalf("wrong rank selection: %s", r.URL)
		}
		if hits == 2 && (q.Get("offset") != "10" || q.Get("session_id") != "RS" || q.Get("unlimited_selector_change_type") != "1") {
			t.Fatalf("lost rank cursor: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"has_more":false,"next_offset":0,"cell_view":{"has_more":true,"next_offset":10,"session_id":"RS","cell_data":[{"video_data":[{"series_id":"9007199254740993123","title":"真实榜单","score":"8.2","rec_text_item":{"RecommendText":"10万推荐"}}]}]}}}`))}, nil
	})}))
	c := New(sess)
	filters := map[string]string{"rank": "comic_series_rank", "rank_sub": "comic_series_hot_play", "rank_panel": "cate_1"}
	page, err := c.RankSelected(context.Background(), filters, "")
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 1 || page.Items[0].SeriesID != "9007199254740993123" || page.Items[0].Series.RankText != "10万推荐" {
		t.Fatalf("bad rank page: %+v", page)
	}
	if _, err = c.RankSelected(context.Background(), filters, page.Cursor); err != nil {
		t.Fatal(err)
	}
}

func TestRankPanelMapsCategorySubrankAndFilters(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("cell_id") != "7470092475068071998" {
			t.Fatal("wrong rank panel cell")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"cell_view":{"cell_selector":{"outer_row":{"items":[{"selector_item_id":"all","show_name":"全部","sub_cell_selector":{"outer_row":{"items":[{"selector_item_id":"ranklist_hot_sc","show_name":"推荐榜","sub_title":"已更新","panel_selector":{"inner_rows":[{"type":"theme","row_name":"主题","items":[{"selector_item_id":"","show_name":"总榜"},{"selector_item_id":"cate_11","show_name":"乡村"}]}]}}]}}}]}}}}}`))}, nil
	})}))
	value, err := New(sess).RankPanel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	groups := value.([]map[string]any)
	subs := groups[0]["subs"].([]map[string]any)
	panel := subs[0]["panel"].([]map[string]any)
	if groups[0]["id"] != "all" || subs[0]["id"] != "ranklist_hot_sc" || panel[0]["options"].([]map[string]string)[0]["id"] != "cate_11" {
		t.Fatalf("bad selector: %+v", value)
	}
}

func TestNewSeriesGenderAndContinuation(t *testing.T) {
	hits := 0
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		q := r.URL.Query()
		if q.Get("cell_id") != "7431550523368554558" || q.Get("cell_gender") != "0" || q.Get("selected_items") != "firstonlinetime_new" || q.Get("limit") != "18" {
			t.Fatalf("wrong new series request: %s", r.URL)
		}
		if hits == 2 && (q.Get("offset") != "18" || q.Get("session_id") != "NS") {
			t.Fatal("lost new series continuation")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"has_more":true,"next_offset":18,"session_id":"NS","cell_view":{"video_data":[{"series_id":"s1","title":"新剧","episode_cnt":80}]}}}`))}, nil
	})}))
	c := New(sess)
	page, err := c.NewSeriesPage(context.Background(), "0", "")
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 1 || page.Items[0].Series.EpisodeCnt != 80 {
		t.Fatalf("bad new series page: %+v", page)
	}
	if _, err = c.NewSeriesPage(context.Background(), "0", page.Cursor); err != nil {
		t.Fatal(err)
	}
}

func TestActorRankUsesPeopleInsteadOfVideoRecords(t *testing.T) {
	c := New(api.NewSession())
	page, err := c.parseVideoFeed([]byte(`{"cell_data":[{"ugc_user_data":[{"user_info":{"base_info":{"actor_id_str":"7602119348190205246","user_name":"王志欣","user_avatar":"https://example.test/avatar.jpg"}},"rec_text_item":{"RecommendText":"997万人气"},"intro":"代表作品","sub_title_list":[{"content":"9部作品"}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.Items[0].Series.Person || page.Items[0].Series.RankText != "997万人气" || page.Items[0].Series.Title != "王志欣" {
		t.Fatalf("bad actor row: %+v", page)
	}
}

func TestCalendarUsesServerDatesAndReleaseItems(t *testing.T) {
	hits := 0
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		q := r.URL.Query()
		if q.Get("tab_type") != "5" || q.Get("active_panel") != "6" || q.Get("target_date") != "20261101" {
			t.Fatalf("wrong calendar request: %s", r.URL)
		}
		if hits == 2 && (q.Get("offset") != "15" || q.Get("subscribe_offset") != "15" || q.Get("session_id") != "CS") {
			t.Fatal("lost calendar continuation")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"has_more":true,"next_offset":15,"session_id":"CS","calendar_schema":{"date_list":["20261031","20261101"],"current_date":"20261101","default_date":"20261101"},"subscribe_items":[{"is_online":true,"category":"爱情","subscribe_data":{"title":"新上线","vid":"v1","content_type":1,"video_detail":{"series_id_str":"7701357402754386968","series_title":"新上线","episode_cnt":64}}}]}}`))}, nil
	})}))
	c := New(sess)
	page, err := c.CalendarPage(context.Background(), "20261101", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Dates) != 2 || page.Today != "20261101" || len(page.Items) != 1 || !page.Items[0].Online || page.Items[0].SeriesID != "7701357402754386968" {
		t.Fatalf("bad release calendar: %+v", page)
	}
	if _, err = c.CalendarPage(context.Background(), "20261101", page.Cursor); err != nil {
		t.Fatal(err)
	}
}
