package hgapi

import (
	"compress/gzip"
	"context"
	"duanju-desktop/pkg/api"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const socialSID = "7697574151787121726"
const socialVID = "7697626904827792409"

func socialResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestSocialListsUseDynamicPOSTRouteAndCorrectRouting(t *testing.T) {
	for _, kind := range []int{4, 20, 2} {
		t.Run(string(rune('a'+kind)), func(t *testing.T) {
			group := socialVID
			source, channel := 4, 18
			if kind == 20 {
				source, channel = 601, 1000
			}
			if kind == 2 {
				source, channel = 1, 34
				group = socialSID
			}
			s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Path != "/novel/commentapi/comment/list/"+group+"/v1/" {
					t.Fatalf("wrong list endpoint %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Content-Encoding") != "" {
					t.Fatal("unexpected wire encoding")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if number(body, "comment_source") != source || number(body, "server_channel") != channel {
					t.Fatal("wrong JSON routing")
				}
				if r.URL.Query().Get("comment_source") != text(body, "comment_source") || r.URL.Query().Get("server_channel") != text(body, "server_channel") {
					t.Fatal("query routing missing")
				}
				business := object(body["business_param"])
				if text(business, "book_id") != socialSID {
					t.Fatal("book association missing")
				}
				if kind == 20 && (number(business, "start_offset_time") != 45000 || number(business, "playlet_item_duration") != 274000) {
					t.Fatal("danmaku playback time ignored")
				}
				return socialResponse(`{"code":0,"data":{"common_list_info":{"has_more":false,"cursor":"{\"end_offset_time\":60000}","total":0},"data_list":[]}}`), nil
			})}))
			c := New(s)
			var err error
			if kind == 20 {
				p, e := c.Danmaku(context.Background(), socialSID, socialVID, 45000, 274000)
				err = e
				if e == nil && p.NextMs != 60000 {
					t.Fatal("server window not preserved")
				}
			} else if kind == 2 {
				_, err = c.Reviews(context.Background(), socialSID, "")
			} else {
				_, err = c.Comments(context.Background(), socialSID, socialVID, 4, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.AppParams()["comment_source"] != "" {
				t.Fatal("comment routing leaked into content session")
			}
		})
	}
}
func TestNestedCommentFieldsImagesScoreAndLongIDs(t *testing.T) {
	raw := `{"common_list_info":{"cursor":"next-cursor","has_more":true,"total":99},"data_list":[{"comment":{"comment_id":"9007199254740993123","common":{"create_timestamp":1793560000,"content":{"text":"真实内容","image_data_list":{"image_data":[{"web_uri":"https://cdn.example/small.heic","web_url":"https://cdn.example/large.heic","width":300,"height":400}]}},"user_info":{"base_info":{"user_id":"9007199254740993124","user_name":"测试用户","user_avatar":"https://cdn.example/avatar.heic"}}},"expand":{"offset_time":12345,"score":10,"score_suffix_text":"观看后点评"},"stat":{"digg_count":12,"reply_count":3},"user_action":{"user_digg":true}}}],"extra":{"credibility_score":8.3,"credibility_score_count":1435}}`
	p, err := parseSocialPage([]byte(raw), socialSID, socialVID, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].ID != "9007199254740993123" || p.Items[0].UserID != "9007199254740993124" || p.Items[0].Text != "真实内容" || p.Items[0].LikeCount != 12 || !p.Items[0].IsLiked || len(p.Items[0].Images) != 1 || p.Cursor != "next-cursor" || !p.HasMore || p.Score != 8.3 || p.ScoreCount != 1435 {
		t.Fatalf("nested comment incorrectly mapped: %+v", p)
	}
	if _, err = parseSocialPage([]byte(`{}`), socialSID, socialVID, 4, 0); err == nil {
		t.Fatal("invalid response masqueraded as empty comments")
	}
}
func TestRepliesDecodeUppercaseCommonAndTheirOwnPagination(t *testing.T) {
	p, err := parseSocialPage([]byte(`{"comment_list_info":{"cursor":"10","has_more":true,"total":23},"reply_list":[{"reply_id":"9007199254740993123","reply_to_comment_id":"123","Common":{"content":{"text":"回复内容"},"user_info":{"base_info":{"user_name":"回复用户"}}},"stat":{"digg_count":2}}]}`), socialSID, socialVID, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Text != "回复内容" || p.Items[0].ParentID != "123" || p.Cursor != "10" || p.Total != 23 {
		t.Fatal("reply list lost")
	}
}
func TestRelatedUsesPlanSceneAndKeepsBothGroups(t *testing.T) {
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("scene") != "10" || r.URL.Query().Get("from") != "detail_page_more_related" || r.URL.Query().Get("book_id") != socialSID {
			t.Fatal("related request still uses a generic feed")
		}
		return socialResponse(`{"code":0,"data":[{"cell_name":"相关作品","video_data":[{"series_id":"1001","title":"同系列","vid":"2001","content_type":1004}]},{"cell_name":"猜你喜欢","cell_data":[{"video_data":[{"series_id":"1002","title":"推荐作品","vid":"2002","content_type":1004}]}]}]}`), nil
	})}))
	p, err := New(s).Related(context.Background(), socialSID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Works) != 1 || p.Works[0].ID != "1001" || len(p.Guess) != 1 || p.Guess[0].ID != "1002" {
		t.Fatal("related groups not preserved")
	}
}
func TestPublishingUsesCorrectObjectAndRequiresActualResponseID(t *testing.T) {
	for _, p := range []SocialPost{{SeriesID: socialSID, VideoID: socialVID, Text: "测试", Kind: 4}, {SeriesID: socialSID, VideoID: socialVID, Text: "测试", Kind: 20, OffsetMs: 12345}, {SeriesID: socialSID, Text: "测试", Kind: 2, Score: 10}, {SeriesID: socialSID, VideoID: socialVID, CommentID: "123", ReplyID: "456", Text: "测试", Kind: 4}} {
		s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var reader io.Reader = r.Body
			if r.Header.Get("Content-Encoding") == "gzip" {
				z, e := gzip.NewReader(r.Body)
				if e != nil {
					t.Fatal(e)
				}
				defer z.Close()
				reader = z
			}
			var body map[string]any
			if err := json.NewDecoder(reader).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if number(body, "data_type") != p.Kind || text(body, "text") != "测试" {
				t.Fatal("publishing lost text/type")
			}
			if p.Kind == 20 && number(object(body["business_param"]), "offset") != 12345 {
				t.Fatal("danmaku timestamp lost")
			}
			if p.CommentID != "" && (text(body, "reply_to_comment_id") != "123" || text(body, "reply_to_reply_id") != "456") {
				t.Fatal("reply parent lost")
			}
			return socialResponse(`{"code":0,"data":{"comment_info":{"comment_id":"9007199254740993123"}}}`), nil
		})}))
		v, err := New(s).PublishSocial(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if v.ID != "9007199254740993123" {
			t.Fatal("server ID lost")
		}
	}
}

func TestPublishingDoesNotInventSuccessWhenServerOmitsID(t *testing.T) {
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return socialResponse(`{"code":0,"data":{}}`), nil
	})}))
	if item, err := New(s).PublishSocial(context.Background(), SocialPost{SeriesID: socialSID, VideoID: socialVID, Text: "测试", Kind: 4}); err == nil || item != nil {
		t.Fatal("missing server ID must not appear as a published comment")
	}
}
