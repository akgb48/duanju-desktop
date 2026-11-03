package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"duanju-desktop/pkg/crypto"
	"github.com/andybalholm/brotli"
)

func TestSessionRefreshesResponseTokenWithoutClearingItWhenAbsent(t *testing.T) {
	calls := 0
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		want := "new-token"
		if calls == 0 {
			want = "old-token"
			w.Header().Set("X-TT-Token", "new-token")
		}
		if got := r.Header.Get("X-TT-Token"); got != want {
			t.Errorf("request token = %q want %q", got, want)
		}
		calls++
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	s.SetToken("old-token")
	for i := 0; i < 3; i++ {
		if _, err := s.CallJSON(context.Background(), "video_detail", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Token() != "new-token" {
		t.Fatal("response token was not retained")
	}
}

func TestPlaybackTransportMatchesV119FullSignatureHeaders(t *testing.T) {
	var gotHeader http.Header
	var rawQuery string
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader, rawQuery = r.Header.Clone(), r.URL.RawQuery
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Error("normal playback must retain request gzip")
		}
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		defer gz.Close()
		b, _ := io.ReadAll(gz)
		if string(b) != `{"mixed_video_id_map":{"1004":["episode"]}}` {
			t.Errorf("unexpected playback body: %s", b)
		}
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	s.SetSigner(crypto.NewDefaultSigner())
	s.SetParam("resolution", "1200*2670")
	s.SetParam("device_type", "Xiaomi 14+test")
	s.SetHeader("X-SS-Req-Ticket", "stale")
	s.SetHeader("X-Medusa", "stale")
	before := time.Now().UnixMilli()
	_, err := s.CallJSON(context.Background(), "multi_video_model", map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"episode"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawQuery, "resolution=1200*2670") || !strings.Contains(rawQuery, "device_type=Xiaomi%2014%2Btest") {
		t.Errorf("query bytes differ from expected: %s", rawQuery)
	}
	q, _ := url.ParseQuery(rawQuery)
	if q.Has("ts") || q.Get("_rticket") == "" {
		t.Error("content requests must generate _rticket without an implicit ts")
	}
	ticket, err := strconv.ParseInt(gotHeader.Get("X-SS-Req-Ticket"), 10, 64)
	if err != nil || ticket < before || ticket > time.Now().UnixMilli() {
		t.Error("signed playback lost its fresh transport timestamp")
	}
	if !regexp.MustCompile(`^\d{13}-[1-9]\d{8}$`).MatchString(gotHeader.Get("X-Reading-Request")) {
		t.Error("missing expected reading request ID")
	}
	for _, name := range crypto.HeaderNames {
		if gotHeader.Get(name) == "" || gotHeader.Get(name) == "stale" {
			t.Errorf("full playback signature missing or stale: %s", name)
		}
	}
	if s.Token() != "" || s.UID() != "" {
		t.Error("transport repair changed account state")
	}
}

func TestEmptySuccessfulResponseIsDistinctFromBusinessOrJSONFailure(t *testing.T) {
	status, body := http.StatusOK, " \n"
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
	_, err := s.Call(context.Background(), "feed", nil)
	var empty *EmptyResponseError
	if !errors.As(err, &empty) || empty.Action != "feed" {
		t.Fatalf("empty 200 response not classified: %v", err)
	}
	status = http.StatusForbidden
	_, err = s.Call(context.Background(), "feed", nil)
	if errors.As(err, &empty) {
		t.Fatal("access denial must not trigger device recovery")
	}
	status, body = http.StatusOK, `{"code":0,`
	_, err = s.Call(context.Background(), "feed", nil)
	if err == nil || errors.As(err, &empty) {
		t.Fatal("truncated JSON must not be classified as an empty response")
	}
}

// newTestSession 构造一个指向假上游的会话。
func newTestSession(t *testing.T, h http.HandlerFunc) (*Session, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// 把主机池替换成测试服务器地址。
	old := HostPool
	HostPool = []string{strings.TrimPrefix(srv.URL, "http://")}
	t.Cleanup(func() { HostPool = old })

	s := NewSession(WithScheme("http"))
	return s, srv
}

// TestCallJSONEnvelope 验证 JSON 请求体与信封解析。
func TestCallJSONEnvelope(t *testing.T) {
	var gotBody map[string]any
	var gotQuery url.Values
	var gotHeaders http.Header
	var gotMethod string
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotMethod = r.Method
		gotQuery = r.URL.Query()
		// 非表单请求体会被 gzip 压缩，测试里先解压。
		var body io.Reader = r.Body
		if r.Header.Get("content-encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("请求体 gzip 解压失败: %v", err)
				return
			}
			defer zr.Close()
			body = zr
		}
		raw, _ := io.ReadAll(body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &gotBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"SUCCESS","data":{"book_id":"42"}}`))
	})

	res, err := s.CallJSON(context.Background(), "comment_list", map[string]any{
		"book_id":   "42",
		"data_type": 4,
	})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if !res.Envelope.OK() {
		t.Fatalf("信封应为成功: %+v", res.Envelope)
	}
	// 参数落在哪里取决于 method：GET 进 query，POST 进 JSON body。
	if gotMethod == http.MethodGet {
		if gotQuery.Get("book_id") != "42" {
			t.Errorf("GET 查询串 book_id 未透传: %v", gotQuery.Encode())
		}
		if gotQuery.Get("data_type") != "4" {
			t.Errorf("GET 查询串 data_type 未透传: %v", gotQuery.Encode())
		}
	} else {
		if gotBody["book_id"] != "42" {
			t.Errorf("POST 请求体 book_id 未透传: %v", gotBody)
		}
		if gotBody["data_type"] != float64(4) {
			t.Errorf("POST 请求体 data_type 未透传: %v", gotBody)
		}
	}
	// 固定头必须全部带上。
	for k := range DefaultHeaders {
		if gotHeaders.Get(http.CanonicalHeaderKey(k)) == "" {
			t.Errorf("缺少固定头 %s", k)
		}
	}
}

// TestBizErrorOnNonZeroCode 验证业务错误码转成 BizError。
func TestBizErrorOnNonZeroCode(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1001,"message":"参数错误"}`))
	})

	res, err := s.Call(context.Background(), "user_info", nil)
	if err != nil {
		t.Fatalf("传输层不应报错: %v", err)
	}
	var out map[string]any
	err = res.Decode(&out)
	if err == nil {
		t.Fatal("业务错误应返回 error")
	}
	be, ok := err.(*BizError)
	if !ok {
		t.Fatalf("错误类型应为 *BizError，实际 %T", err)
	}
	if be.Code != 1001 || be.Message != "参数错误" {
		t.Errorf("错误字段不符: %+v", be)
	}
}

// TestGzipResponse 验证 gzip 响应解压。
func TestGzipResponse(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte(`{"code":0,"message":"SUCCESS","data":{"ok":true}}`))
		_ = zw.Close()
	})

	res, err := s.Call(context.Background(), "user_info", nil)
	if err != nil {
		t.Fatalf("gzip 响应解析失败: %v", err)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := res.Decode(&out); err != nil {
		t.Fatalf("解 data 失败: %v", err)
	}
	if !out.OK {
		t.Error("data.ok 应为 true")
	}
}

// TestBrotliResponse 验证 brotli 响应解压。
func TestBrotliResponse(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		bw := brotli.NewWriter(w)
		_, _ = bw.Write([]byte(`{"status_code":0,"message":"","data":{"br":1}}`))
		_ = bw.Close()
	})

	res, err := s.Call(context.Background(), "user_info", nil)
	if err != nil {
		t.Fatalf("brotli 响应解析失败: %v", err)
	}
	var out struct {
		BR int `json:"br"`
	}
	if err := res.Decode(&out); err != nil || out.BR != 1 {
		t.Fatalf("brotli data 解析异常: out=%+v err=%v", out, err)
	}
}

// TestRequestGzipCompression 验证非表单请求体会被 gzip 压缩。
func TestRequestGzipCompression(t *testing.T) {
	var encoding string
	var bodyLen int
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		encoding = r.Header.Get("content-encoding")
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("请求体应为 gzip，解压失败: %v", err)
			return
		}
		defer zr.Close()
		raw, _ := io.ReadAll(zr)
		bodyLen = len(raw)
		_, _ = w.Write([]byte(`{"status_code":0,"message":""}`))
	})

	_, err := s.CallJSON(context.Background(), "comment_add", map[string]any{
		"text": strings.Repeat("测", 200),
	})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if encoding != "gzip" {
		t.Errorf("content-encoding 应为 gzip，实际 %q", encoding)
	}
	if bodyLen == 0 {
		t.Error("解压后请求体为空")
	}
}

// TestUnknownAction 验证未注册 action 被拒。
func TestUnknownAction(t *testing.T) {
	s := NewSession()
	_, err := s.Call(context.Background(), "no_such_action", nil)
	if err == nil || !strings.Contains(err.Error(), "未注册") {
		t.Fatalf("应报未注册错误，实际 %v", err)
	}
}

// TestHostRotation 验证主机池轮询。
func TestHostRotation(t *testing.T) {
	old := HostPool
	HostPool = []string{"h1.test", "h2.test", "h3.test"}
	t.Cleanup(func() { HostPool = old })

	s := NewSession()
	a, _ := DefaultRegistry.Get("feed")
	got := []string{s.pickHost(a), s.pickHost(a), s.pickHost(a), s.pickHost(a)}
	want := []string{"h1.test", "h2.test", "h3.test", "h1.test"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 次轮询: 实际 %q，期望 %q", i, got[i], want[i])
		}
	}
}

// TestFixedHostIgnoresPool 验证固定主机不走轮询。
func TestFixedHostIgnoresPool(t *testing.T) {
	s := NewSession()
	a, ok := DefaultRegistry.Get("device_register")
	if !ok {
		t.Fatal("缺少 device_register")
	}
	for i := 0; i < 3; i++ {
		if h := s.pickHost(a); h != "log.snssdk.com" {
			t.Fatalf("固定主机被轮询覆盖: %q", h)
		}
	}
}

// TestIsFormContentType 验证表单判定。
func TestIsFormContentType(t *testing.T) {
	cases := map[string]bool{
		"application/x-www-form-urlencoded":      true,
		"application/x-www-form-urlencoded; a=b": true,
		"multipart/form-data; boundary=xxx":      true,
		"application/json; charset=utf-8":        false,
		"":                                       false,
		"application/octet-stream":               false,
	}
	for ct, want := range cases {
		if got := isFormContentType(ct); got != want {
			t.Errorf("isFormContentType(%q) = %v，期望 %v", ct, got, want)
		}
	}
}
