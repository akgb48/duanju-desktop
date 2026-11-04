package app

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"duanju-desktop/pkg/model"
)

func TestReservationsUseSignedInAccountAndPreservePagination(t *testing.T) {
	const itemID = "9007199254740993123"
	const seriesID = "9007199254740993124"
	requests := 0
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		requests++
		q := r.URL.Query()
		if r.URL.Path != "/reading/user/subscribe/list/v1/" || r.Method != http.MethodGet ||
			q.Get("tab_type") != "13" || q.Get("need_calendar_schema") != "false" ||
			q.Get("limit") != "20" || q.Get("offset") != q.Get("subscribe_offset") ||
			q.Get("subscribe_order_type") != "0" || q.Get("swipe_type") != "0" {
			t.Fatalf("unexpected reservation request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-TT-Token") != "test-token" || r.Header.Get("Cookie") != "sessionid=test" || q.Get("user_id") != "123" {
			t.Fatal("reservation request lost account credentials")
		}
		switch q.Get("is_online") + "/" + q.Get("offset") {
		case "true/0":
			return accountResponse(`{"code":0,"data":{"subscribe_items":[{"item_id":9007199254740993123,"is_online":true,"category":"剧情","schedule_publish_time":1761900000,"subscribe_data":{"video_detail":{"series_id_str":"9007199254740993124","series_title":"已上线的剧","series_cover":"https://example.com/cover.jpg","episode_cnt":24}}}],"next_offset":20,"has_more":true,"online_total_count":21,"offline_total_count":1}}`), nil
		case "true/20":
			return accountResponse(`{"code":0,"data":{"subscribe_items":[],"online_total_count":21,"offline_total_count":1}}`), nil
		case "false/0":
			return accountResponse(`{"code":0,"data":{"subscribe_items":[{"item_id":"9007199254740993125","item_type":14,"name":"待上线的剧","cover":"https://example.com/pending.jpg","item_desc":"预约简介","detail_schema":"dragon8662://shortSeriesDetail?detail_type=1&series_id=9007199254740993126","expected_publish_time":1793800000,"expected_rec_text":"即将上线","has_subscribed":false,"is_online":false}],"online_total_count":21,"offline_total_count":1}}`), nil
		case "false/20":
			return accountResponse(`{"code":403,"message":"请重新登录"}`), nil
		default:
			return nil, fmt.Errorf("unexpected reservation filter or cursor")
		}
	})
	if _, err := a.Reservations(true, ""); err == nil || requests != 0 {
		t.Fatal("logged-out reservation request reached upstream")
	}
	a.sess.SetAccount("123", "test-token", "sessionid=test")
	a.svc.SetLoggedIn(true, &model.User{UserID: "123"})
	page, err := a.Reservations(true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || page.Cursor != "20" || page.OnlineTotal != 21 || page.OfflineTotal != 1 || len(page.Items) != 1 {
		t.Fatalf("incorrect reservation page: %+v", page)
	}
	if it := page.Items[0]; it.ItemID != itemID || it.SeriesID != seriesID || it.Title != "已上线的剧" ||
		it.Cover == "" || it.Category != "剧情" || it.EpisodeCount != 24 || !it.Online || it.SchedulePublishTime != 1761900000 {
		t.Fatalf("incorrect reservation item: %+v", it)
	}
	next, err := a.Reservations(true, page.Cursor)
	if err != nil || next.HasMore || next.Cursor != "" || next.Items == nil || len(next.Items) != 0 {
		t.Fatalf("incorrect final page: %+v, %v", next, err)
	}
	offline, err := a.Reservations(false, "")
	if err != nil || len(offline.Items) != 1 || offline.OfflineTotal != 1 {
		t.Fatalf("incorrect pending reservations: %+v, %v", offline, err)
	}
	if it := offline.Items[0]; it.ItemID != "9007199254740993125" || it.SeriesID != "9007199254740993126" ||
		it.Title != "待上线的剧" || it.Cover != "https://example.com/pending.jpg" || it.Intro != "预约简介" ||
		it.Online || it.Subscribed || it.ExpectedPublishTime != 1793800000 || it.Hint != "即将上线" {
		t.Fatalf("incorrect pending reservation item: %+v", it)
	}
	if _, err := a.Reservations(false, "20"); err == nil {
		t.Fatal("upstream failure was reported as an empty list")
	}
}

func TestReservationUpdatesReachUpstreamAndPropagateRejection(t *testing.T) {
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.Header.Get("X-TT-Token") != "test-token" {
			t.Fatal("reservation update did not use the authenticated POST route")
		}
		body, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		switch r.URL.Path {
		case "/reading/bookapi/search/uncover_subscribe/v":
			var request struct {
				ItemID string `json:"item_id"`
				Op     int    `json:"op_type"`
			}
			if err := json.NewDecoder(body).Decode(&request); err != nil {
				return nil, err
			}
			if request.ItemID != "123456789" || request.Op != 2 {
				t.Fatalf("incorrect cancellation payload: %+v", request)
			}
			return accountResponse(`{"code":103,"message":"预约操作被拒绝"}`), nil
		case "/reading/user/subscribe/delete/v":
			var request struct {
				IDs    []string `json:"item_id"`
				Online bool     `json:"is_online"`
				Tab    int      `json:"tab_type"`
				All    bool     `json:"all_select"`
			}
			if err := json.NewDecoder(body).Decode(&request); err != nil {
				return nil, err
			}
			if len(request.IDs) != 1 || request.IDs[0] != "9007199254740993123" || !request.Online || request.Tab != 13 || request.All {
				t.Fatalf("incorrect deletion payload: %+v", request)
			}
			return accountResponse(`{"code":0,"message":"SUCCESS"}`), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	a.sess.SetAccount("123", "test-token", "sessionid=test")
	a.svc.SetLoggedIn(true, &model.User{UserID: "123"})
	if _, err := a.SetReservation("123456789", false); err == nil {
		t.Fatal("upstream rejection was reported as a successful cancellation")
	}
	if err := a.DeleteReservations([]string{"9007199254740993123"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestSocialPublishingWithoutAccountMakesNoRequest(t *testing.T) {
	requests := 0
	a := accountTestApp(t, func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected publishing request")
	})
	for name, send := range map[string]func() (*model.Comment, error){
		"comment": func() (*model.Comment, error) { return a.SendComment("123", "456", "测试", "", "[]") },
		"danmaku": func() (*model.Comment, error) { return a.SendDanmaku("123", "456", "测试", 1200, "") },
		"reply": func() (*model.Comment, error) {
			return a.SendReply("123", "456", "789", "", "", "测试", false, 0, "[]")
		},
		"review":       func() (*model.Comment, error) { return a.SendReview("123", "测试", 10) },
		"review reply": func() (*model.Comment, error) { return a.SendReviewReply("123", "789", "", "测试") },
	} {
		t.Run(name, func(t *testing.T) {
			if item, err := send(); err == nil || item != nil {
				t.Fatal("anonymous publishing must fail before network access")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("anonymous publishing made %d network requests", requests)
	}
}
