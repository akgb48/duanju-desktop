package hgapi

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"duanju-desktop/pkg/api"
)

func TestPlaybackRetriesEmptyReadWithoutChangingRegisteredDevice(t *testing.T) {
	oldHosts := api.HostPool
	api.HostPool = []string{"first.example", "second.example"}
	t.Cleanup(func() { api.HostPool = oldHosts })
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistent=%v", persistent), func(t *testing.T) {
			calls := 0
			s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls <= 2 && r.URL.Query().Get("device_id") != "123" {
					t.Fatal("retry changed installation identity")
				}
				body := ""
				if calls == 2 && !persistent {
					body = `{"code":0,"data":{"456":{"video_id":"456","video_list":{"video_1":{"definition":"720p","main_url":"https://media.example/test.mp4","vwidth":720,"vheight":1280}}}}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}))
			s.SetDevice(api.DeviceContext{DeviceID: "123"})
			c := New(s)
			c.contentTypes.Store("456", 1004)
			d, err := c.VideoDetail(context.Background(), "456")
			wantCalls := 2
			if calls != wantCalls {
				t.Fatalf("expected bounded route attempts (%d), got %d", wantCalls, calls)
			}
			if s.DeviceContext().DeviceID != "123" {
				t.Fatal("playback replaced registered identity")
			}
			var empty *api.EmptyResponseError
			if persistent {
				if !errors.As(err, &empty) || d != nil {
					t.Fatal("persistent empty response must remain an explicit failure")
				}
			} else if err != nil || d == nil || len(d.Sources) != 1 {
				t.Fatalf("retry did not recover metadata: %+v %v", d, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSeriesDetailUsesSeriesEndpointAndPreservesIDs(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/novel/player/multi_video_detail/preload/v1" {
			t.Fatalf("wrong endpoint %s", r.URL.Path)
		}
		var body map[string]any
		reader := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer zr.Close()
			reader = zr
		}
		if err := json.NewDecoder(reader).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["series_id"] != "7654321098765432101" {
			t.Errorf("series_id=%v", body["series_id"])
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"7654321098765432101":{"video_data":{"series_id":7654321098765432101,"series_title":"测试短剧","content_type":7,"video_list":[{"vid":7654321098765432102,"vid_index":1,"title":"第一集"}]}}}}`))}, nil
	})}))
	c := New(sess)
	s, err := c.SeriesDetail(context.Background(), "7654321098765432101")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "7654321098765432101" || len(s.Episodes) != 1 || s.Episodes[0].VideoID != "7654321098765432102" {
		t.Fatalf("IDs lost: %+v", s)
	}
	if typ, ok := c.contentTypes.Load("7654321098765432102"); !ok || typ != 7 {
		t.Fatalf("content type not retained: %v", typ)
	}
}

func TestPlaybackDecryptsProtocolSpadeURLAndFallsBackToBackup(t *testing.T) {
	b, err := os.ReadFile("../media/cenc/testdata/url-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct{ Input, SeedHex, PlainHex string }
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	v := fixture.Vectors[0]
	seed, _ := hex.DecodeString(v.SeedHex)
	plain, _ := hex.DecodeString(v.PlainHex)
	raw := map[string]any{"key_seed": base64.RawURLEncoding.EncodeToString(seed), "video_list": map[string]any{"hd": map[string]any{"main_url": "invalid-base64", "backup_url": v.Input, "definition": "hd", "vwidth": 1280, "vheight": 720, "codec_type": "h264"}}}
	d, err := decodePlaybackModel(raw, "episode")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sources) != 1 || d.Sources[0].URL != string(plain) || d.Sources[0].Width != 1280 || d.Sources[0].Height != 720 {
		t.Fatalf("spade playback not decoded: %+v", d)
	}
}

func TestPlaybackFallbackKeepsContentKeyAndDoesNotForwardCookies(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Gorgon") != "" {
			t.Fatal("account credentials leaked to fallback host")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"video_info":{"data":{"key_seed":"","video_list":{"hd":{"main_url":"https://media.example.test/a.mp4","vwidth":1920,"vheight":1080}}}}}`))}, nil
	})}))
	sess.SetCookieString("sessionid=secret")
	raw := map[string]any{"fallback_api": "https://fallback.example.test/meta", "video_list": map[string]any{"hd": map[string]any{"definition": "hd", "spade_a": "VEcTAVtGEg9ZRhINX0ERD0FCDg1fXSYkXV0mJltaJSSlWA"}}}
	d, err := New(sess).playbackWithFallback(context.Background(), raw, "episode")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sources) != 1 || hex.EncodeToString(d.Sources[0].ContentKey) != "00112233445566778899aabbccddeeff" || d.Sources[0].Height != 1080 {
		t.Fatalf("lost metadata/key when merging fallback: %+v", d)
	}
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), "ContentKey") || strings.Contains(string(encoded), "00112233") {
		t.Fatal("content key leaked to frontend JSON")
	}
}

func TestVideoFeedExcludesNovelsAndRetainsVideoMetadata(t *testing.T) {
	c := New(api.NewSession())
	page, err := c.parseVideoFeed([]byte(`{"book_data":[{"book_id":"novel","book_name":"不是短剧"}],"mixed_data_list":[{"video_data":{"series_id":"series","vid":"episode","vid_index":1,"content_type":7,"video_detail":{"title":"短剧","cover":"https://example.test/poster.jpg","video_list":[{"vid":"episode","vid_index":1}]}}}],"has_more":true,"next_offset":"20"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SeriesID != "series" || page.Items[0].VideoID != "episode" || page.Items[0].Series.Title != "短剧" || !page.HasMore || page.Cursor != "20" {
		t.Fatalf("unexpected feed: %+v", page)
	}
}

func TestProtocolFeedArrayAndNestedSeriesID(t *testing.T) {
	c := New(api.NewSession())
	p, err := c.parseVideoFeed([]byte(`{"cell_data":[{"video_data":[{"vid":"7701201568489557017","video_detail":{"series_id":7701199947957292056,"series_id_str":"7701199947957292056","series_title":"短剧","series_intro":"简介","content_type":1004}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].SeriesID != "7701199947957292056" || p.Items[0].Series.Description != "简介" {
		t.Fatalf("source nested model was lost: %+v", p)
	}
}

func TestSearchReadsRootSearchTabsAndVideoArrays(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("tab_type") != "11" || r.URL.Query().Get("count") != "20" {
			t.Error("wrong short drama search channel")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"message":"SUCCESS","search_tabs":[{"tab_type":11,"data":[{"video_data":[{"series_id":"7654321098765432101","title":"测试短剧","episode_cnt":12,"content_type":1004}]}]}]}`))}, nil
	})}))
	r, err := New(sess).Search(context.Background(), "测试", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Series) != 1 || r.Series[0].Title != "测试短剧" {
		t.Fatalf("valid root search result lost: %+v", r)
	}
}

func TestVideoModelBusinessParametersUseNestedBizParam(t *testing.T) {
	p := videoModelParams("v", 1004)
	if _, ok := p["detail_page_version"]; ok {
		t.Fatal("flattened biz_param")
	}
	biz := p["biz_param"].(map[string]any)
	if biz["video_platform"] != 1024 || biz["need_mp4_align"] != false || biz["device_level"] != 4 {
		t.Fatal("video model wire parameters differ")
	}
}

func TestPlaybackParsesJSONModelAndBase64URL(t *testing.T) {
	u := "https://cdn.example.test/episode.mp4?token=test"
	modelJSON := `{"video_duration":83,"video_list":{"video_1":{"definition":"720p","height":720,"width":1280,"main_url":"` + base64.StdEncoding.EncodeToString([]byte(u)) + `"}}}`
	d, err := decodePlaybackModel(map[string]any{"episode": map[string]any{"video_model": modelJSON}}, "episode")
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != 83 || len(d.Sources) != 1 || d.Sources[0].URL != u {
		t.Fatalf("unexpected playback: %+v", d)
	}
}

func TestPlaybackRejectsNonHTTPAndProtectedSources(t *testing.T) {
	_, err := decodePlaybackModel(map[string]any{"video_list": map[string]any{"a": map[string]any{"main_url": "file:///secret"}}}, "episode")
	if err == nil {
		t.Fatal("non-HTTP source accepted")
	}
}

func TestVideoFeedReportsNovelOnlyResponse(t *testing.T) {
	c := New(api.NewSession())
	_, err := c.parseVideoFeed([]byte(`{"tab_item":[{"cell_data":[{"book_data":[{"book_id":"1","book_name":"小说"}]}]}]}`))
	if err == nil || !strings.Contains(err.Error(), "设备注册/签名尚未完成") {
		t.Fatalf("novel-only response should not look like a working drama feed: %v", err)
	}
}

func TestCategorySendsStructuredFilters(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/reading/distribution/category/landpage/v1/" || r.Method != "POST" {
			t.Fatalf("unexpected category request %s %s", r.Method, r.URL.Path)
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		var body map[string]any
		if err = json.NewDecoder(zr).Decode(&body); err != nil {
			t.Fatal(err)
		}
		filters, ok := body["select_items"].(map[string]any)
		if !ok {
			t.Fatalf("missing structured select_items: %v", body)
		}
		if _, ok = filters["genre"].([]any); !ok {
			t.Fatalf("genre should stay an array: %v", filters)
		}
		if body["offset"] != float64(20) {
			t.Fatalf("offset=%v", body["offset"])
		}
		if body["limit"] != float64(54) {
			t.Fatalf("small batches reintroduced: %v", body["limit"])
		}
		if body["req_scene"] != "default" || body["req_type"] != "only_content" {
			t.Fatalf("wrong category request mode: %v", body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"mixed_data_list":[],"has_more":false}}`))}, nil
	})}))
	_, err := New(sess).Category(context.Background(), map[string]any{"genre": []string{"romance"}}, "20")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCategoryPreservesProtocolSessionCursorAndGenreScene(t *testing.T) {
	calls := 0
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		var body map[string]any
		if err = json.NewDecoder(zr).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["req_scene"] != "comic_series" {
			t.Fatalf("genre scene ignored: %v", body["req_scene"])
		}
		if calls == 0 {
			if body["session_id"] != "" || body["offset"] != float64(0) {
				t.Fatal("unexpected initial continuation")
			}
		} else if body["session_id"] != "test-session" || body["offset"] != float64(54) {
			t.Fatal("continuation session or offset lost")
		}
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"video_data":[],"has_more":true,"next_offset":54,"session_id":"test-session"}}`))}, nil
	})}))
	c := New(sess)
	filters := map[string]any{"genre": []string{"comic_series"}}
	p, err := c.Category(context.Background(), filters, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cursor != `{"o":54,"s":"test-session"}` {
		t.Fatalf("source cursor shape changed: %s", p.Cursor)
	}
	p, err = c.Category(context.Background(), filters, p.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasMore {
		t.Fatal("non-advancing empty batch would spin forever")
	}
}

func TestSelectorPanelMapsRealRows(t *testing.T) {
	sess := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		var body map[string]any
		if err = json.NewDecoder(zr).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["req_scene"] != "comic_series" || body["req_type"] != "only_panel" || body["need_selector_panel"] != false {
			t.Fatalf("wrong selector mode: %v", body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"selector_rows":[{"type":"genre","row_name":"全部体裁","items":[{"selector_item_id":"short_play","show_name":"真人剧"}]}]}}`))}, nil
	})}))
	panel, err := New(sess).SelectorPanel(context.Background(), "comic_series")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(panel)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `[{"key":"genre","name":"全部体裁","options":[{"id":"short_play","name":"真人剧"}]}]` {
		t.Fatalf("bad selector DTO: %s", b)
	}
}
