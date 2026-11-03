package hgapi

import (
	"context"
	"encoding/json"
	"hongguo-desktop/pkg/api"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSearchContinuationUsesOnlySelectedTabAndRetainsSessionPassback(t *testing.T) {
	calls := 0
	s := sessionForJSON(t, func(r *http.Request) string {
		calls++
		q := r.URL.Query()
		if calls == 1 {
			if q.Get("offset") != "0" || q.Has("search_id") || q.Has("passback") {
				t.Fatal("first search inherited previous context")
			}
			return `{"code":0,"search_tabs":[{"tab_type":11,"has_more":true,"next_offset":6,"search_id":"session#1","passback":"opaque=6&next","data":[{"video_data":[{"series_id":"9007199254740993123","title":"首剧"}]}]},{"tab_type":29,"next_offset":0,"data":null}]}`
		}
		if q.Get("offset") != "6" || q.Get("search_id") != "session#1" || q.Get("passback") != "opaque=6&next" {
			t.Fatalf("search continuation lost: %v", q)
		}
		return `{"code":0,"search_tabs":[{"tab_type":11,"has_more":false,"next_offset":8,"data":[{"video_data":[{"series_id":"9007199254740993124","title":"续页"}]}]},{"tab_type":29,"has_more":true,"next_offset":0,"data":[{"video_data":[{"series_id":"wrong-tab","title":"其他频道"}]}]}]}`
	})
	c := New(s)
	first, err := c.Search(context.Background(), "测试", "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Series) != 1 || first.Series[0].ID != "9007199254740993123" {
		t.Fatalf("selected tab lost: %+v", first)
	}
	second, err := c.Search(context.Background(), "测试", first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.Cursor != "" || len(second.Series) != 1 || second.Series[0].Title != "续页" {
		t.Fatalf("other tab replaced search results or pagination: %+v", second)
	}
}

func TestSearchForeignKeywordResetsAndStalledCursorStops(t *testing.T) {
	foreign, _ := json.Marshal(searchCursor{Keyword: "旧关键词", Tab: 11, Offset: "99", SearchID: "old", Passback: "old"})
	if s := decodeSearchCursor("新词", string(foreign)); s.Offset != "0" || s.SearchID != "" {
		t.Fatal("new keyword reused old pagination")
	}
	c := New(nil)
	p, err := c.parseSearch([]byte(`{"search_tabs":[{"tab_type":11,"has_more":true,"next_offset":6,"search_id":"new-id","passback":"6","data":[]}]}`), searchCursor{Keyword: "新词", Tab: 11, Offset: "6", Passback: "6"})
	if err != nil || p.HasMore {
		t.Fatalf("unchanged cursor causes endless result reload: %+v %v", p, err)
	}
}

// One fake transport keeps the endpoint's real query/JSON boundary exercised.
func sessionForJSON(t *testing.T, response func(*http.Request) string) *api.Session {
	t.Helper()
	return api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response(r)))}, nil
	})}))
}
