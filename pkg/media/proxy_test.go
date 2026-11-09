package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolver 是测试用 Resolver。
type fakeResolver struct {
	mu       sync.Mutex
	imageURL string
	imageErr error
	videoURL string
	referer  string
	videoErr error
	lastKey  string
	lastID   string
	lastQ    string
}

func (f *fakeResolver) ResolveImage(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = key
	return f.imageURL, f.imageErr
}

func (f *fakeResolver) ResolveVideo(_ context.Context, videoID, quality string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastID, f.lastQ = videoID, quality
	return f.videoURL, f.referer, f.videoErr
}

// newProxy 构造一个不启动监听的代理。
func newProxy(t *testing.T, res Resolver, cacheDir string) *Proxy {
	t.Helper()
	p := NewProxy(cacheDir, res)
	p.imageClient = p.client // Test upstreams use httptest's loopback listener.
	return p
}

func TestCORSAndPreflight(t *testing.T) {
	p := newProxy(t, &fakeResolver{}, "")

	// OPTIONS 预检
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, RouteMedia+"v1/1080p", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("预检状态码 = %d，期望 204", rec.Code)
	}
	if got := rec.Header().Get(HeaderAllowOrigin); got != "*" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get(HeaderAllowHeaders); got != "*" {
		t.Errorf("Allow-Headers = %q", got)
	}
	if got := rec.Header().Get(HeaderExposeHeaders); got != ExposeHeadersValue {
		t.Errorf("Expose-Headers = %q", got)
	}

	// 普通请求也要带 CORS 头
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if got := rec.Header().Get(HeaderAllowOrigin); got != "*" {
		t.Errorf("404 响应也应带 CORS，实际 %q", got)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知路径状态码 = %d，期望 404", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	p := newProxy(t, &fakeResolver{}, "")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, RouteAudio, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 状态码 = %d，期望 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); !strings.Contains(got, "GET") {
		t.Errorf("Allow 头 = %q", got)
	}
}

func TestServeMediaRangePassthrough(t *testing.T) {
	// 上游是一个真实的 Range 服务器。
	const body = "0123456789abcdefghij"
	var gotRange, gotReferer, gotUA string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		gotReferer = r.Header.Get("Referer")
		gotUA = r.Header.Get("User-Agent")

		http.ServeContent(w, r, "v.mp4", time.Unix(1700000000, 0), strings.NewReader(body))
	}))
	defer up.Close()

	res := &fakeResolver{videoURL: up.URL + "/v.mp4", referer: "https://hongguoduanju.com/"}
	p := newProxy(t, res, "")
	// 空 Referer 也要能用默认值：这里显式给一个非默认值便于断言透传。
	res.referer = "https://custom.example/"

	// 1) 全量请求
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"vid123/1080p", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("全量请求状态码 = %d", rec.Code)
	}
	if rec.Body.String() != body {
		t.Fatalf("全量内容不一致: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q，期望 bytes", got)
	}
	if got := rec.Header().Get(HeaderAllowOrigin); got != "*" {
		t.Errorf("媒体响应缺 CORS: %q", got)
	}
	if gotReferer != "https://custom.example/" {
		t.Errorf("Referer 未透传，实际 %q", gotReferer)
	}
	if gotUA == "" {
		t.Error("上游请求缺 User-Agent")
	}
	if res.lastID != "vid123" || res.lastQ != "1080p" {
		t.Errorf("Resolver 入参错误: id=%q q=%q", res.lastID, res.lastQ)
	}

	// 2) Conditional Range must reach the CDN and preserve 206 + Content-Range.
	req := httptest.NewRequest(http.MethodGet, RouteMedia+"vid123/1080p", nil)
	req.Header.Set("Range", "bytes=5-9")
	req.Header.Set("If-Range", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 状态码 = %d，期望 206", rec.Code)
	}
	if got := rec.Body.String(); got != body[5:10] {
		t.Fatalf("分片内容 = %q，期望 %q", got, body[5:10])
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 5-9/20" {
		t.Errorf("Content-Range = %q，期望 bytes 5-9/20", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "5" {
		t.Errorf("Content-Length = %q，期望 5", got)
	}
	if gotRange != "bytes=5-9" {
		t.Errorf("上游收到的 Range = %q", gotRange)
	}

	// 3) 尾部区间
	req = httptest.NewRequest(http.MethodGet, RouteMedia+"vid123/1080p", nil)
	req.Header.Set("Range", "bytes=-4")
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("尾部分片状态码 = %d", rec.Code)
	}
	if got := rec.Body.String(); got != body[len(body)-4:] {
		t.Errorf("尾部分片内容 = %q", got)
	}

	// 4) 不可满足区间应由上游返回 416 并透传
	req = httptest.NewRequest(http.MethodGet, RouteMedia+"vid123/1080p", nil)
	req.Header.Set("Range", "bytes=999-1000")
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("越界分片状态码 = %d，期望 416", rec.Code)
	}
}

func TestServeMediaHead(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "v.mp4", time.Unix(1700000000, 0), strings.NewReader("abcdef"))
	}))
	defer up.Close()

	p := newProxy(t, &fakeResolver{videoURL: up.URL}, "")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, RouteMedia+"v/720p", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD 状态码 = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD 不应有 body，实际 %d 字节", rec.Body.Len())
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("HEAD 也应带 Accept-Ranges，实际 %q", got)
	}
}

func TestServeMediaErrors(t *testing.T) {
	// 未配置 Resolver
	p := newProxy(t, nil, "")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"v/q", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("无 Resolver 状态码 = %d，期望 500", rec.Code)
	}

	// 路径不完整
	p2 := newProxy(t, &fakeResolver{}, "")
	rec = httptest.NewRecorder()
	p2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空 videoID 状态码 = %d，期望 400", rec.Code)
	}

	// Resolver 报错
	p3 := newProxy(t, &fakeResolver{videoErr: errors.New("boom")}, "")
	rec = httptest.NewRecorder()
	p3.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"v/q", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("Resolver 报错状态码 = %d，期望 502", rec.Code)
	}

	// 上游 404
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer up.Close()
	p4 := newProxy(t, &fakeResolver{videoURL: up.URL}, "")
	rec = httptest.NewRecorder()
	p4.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"v/q", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("上游 404 应透传，实际 %d", rec.Code)
	}
}

func TestSplitMediaPath(t *testing.T) {
	cases := []struct {
		in    string
		id, q string
		ok    bool
	}{
		{"vid/1080p", "vid", "1080p", true},
		{"/vid/1080p", "vid", "1080p", true},
		{"vid", "vid", "", true},
		{"vid/", "vid", "", true},
		{"", "", "", false},
		{"/", "", "", false},
	}
	for _, c := range cases {
		id, q, ok := splitMediaPath(c.in)
		if ok != c.ok || id != c.id || q != c.q {
			t.Errorf("splitMediaPath(%q) = (%q,%q,%v)，期望 (%q,%q,%v)",
				c.in, id, q, ok, c.id, c.q, c.ok)
		}
	}
	// 编码过的 id 要能还原
	id, q, ok := splitMediaPath("a%2Fb/1080p")
	if !ok || id != "a/b" || q != "1080p" {
		t.Errorf("编码路径解析错误: (%q,%q,%v)", id, q, ok)
	}
}

func TestServeImageAndCache(t *testing.T) {
	const payload = "\x89PNG\r\n\x1a\nfakepngdata"
	hits := 0
	var gotReferer string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, payload)
	}))
	defer up.Close()

	dir := t.TempDir()
	res := &fakeResolver{imageURL: up.URL + "/i.png"}
	p := newProxy(t, res, dir)

	// 第一次：MISS，走上游
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"https%3A%2F%2Fx%2Fa.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if rec.Body.String() != payload {
		t.Fatalf("内容 = %q", rec.Body.String())
	}
	if got := rec.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("首次 X-Cache = %q，期望 MISS", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	if hits != 1 {
		t.Fatalf("上游被调用 %d 次", hits)
	}
	if gotReferer == "" {
		t.Error("上游请求缺 Referer")
	}
	if res.lastKey != "https://x/a.png" {
		t.Errorf("ResolveImage 入参 = %q", res.lastKey)
	}

	// 第二次：HIT，不再打上游
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"https%3A%2F%2Fx%2Fa.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("二次状态码 = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("二次 X-Cache = %q，期望 HIT", got)
	}
	if hits != 1 {
		t.Errorf("命中缓存后不应再请求上游，实际 %d 次", hits)
	}
	if rec.Body.String() != payload {
		t.Errorf("缓存内容 = %q", rec.Body.String())
	}
}

func TestServeImageErrors(t *testing.T) {
	// 空 key
	p := newProxy(t, &fakeResolver{}, "")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage, nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空 key 状态码 = %d，期望 400", rec.Code)
	}

	// 无 Resolver
	noRes := newProxy(t, nil, "")
	rec = httptest.NewRecorder()
	noRes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"k", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("无 Resolver 状态码 = %d，期望 500", rec.Code)
	}

	// Resolver 报错
	p2 := newProxy(t, &fakeResolver{imageErr: errors.New("nope")}, "")
	rec = httptest.NewRecorder()
	p2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"k", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("Resolver 报错状态码 = %d，期望 502", rec.Code)
	}

	// 上游 403
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer up.Close()
	p3 := newProxy(t, &fakeResolver{imageURL: up.URL}, "")
	rec = httptest.NewRecorder()
	p3.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"k", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("上游 403 状态码 = %d，期望 502", rec.Code)
	}
}

func TestImageCacheSkipsNonImage(t *testing.T) {
	// 上游返回 text/html 时不应落盘，避免把错误页缓存住。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>err</html>")
	}))
	defer up.Close()

	dir := t.TempDir()
	p := newProxy(t, &fakeResolver{imageURL: up.URL}, dir)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"bad", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	removed, err := p.CleanExpired()
	if err != nil {
		t.Fatalf("CleanExpired: %v", err)
	}
	if removed != 0 {
		t.Errorf("非图片不应落盘，实际清理了 %d 个", removed)
	}
}

func TestCleanExpired(t *testing.T) {
	dir := t.TempDir()
	p := newProxy(t, &fakeResolver{}, dir)

	// 手写两个缓存文件：一个过期、一个新。
	write := func(name string, mod time.Time) string {
		sub := filepath.Join(dir, name[:2])
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		path := filepath.Join(sub, name[2:])
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
		return path
	}
	oldPath := write("aabbccdd", time.Now().Add(-8*24*time.Hour))
	newPath := write("11223344", time.Now())

	n, err := p.CleanExpired()
	if err != nil {
		t.Fatalf("CleanExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("清理数量 = %d，期望 1", n)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("过期文件应被删除")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Error("新文件不应被删除")
	}

	// 空 cacheDir 不报错
	empty := newProxy(t, &fakeResolver{}, "")
	if n, err := empty.CleanExpired(); err != nil || n != 0 {
		t.Errorf("空 cacheDir: n=%d err=%v", n, err)
	}
}

func TestServeAudio(t *testing.T) {
	p := newProxy(t, &fakeResolver{}, "")

	// 未加载
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteAudio, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("未加载 audio 状态码 = %d，期望 404", rec.Code)
	}

	p.SetAudioBook(&AudioBook{
		ID:    "book1",
		Title: "测试音频",
		Chapters: []json.RawMessage{
			json.RawMessage(`{"index":1,"title":"第一章","url":"a.mp3","duration":120}`),
			json.RawMessage(`{"index":2,"title":"第二章","url":"b.mp3","duration":130}`),
		},
	})

	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteAudio, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var got AudioBook
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.ID != "book1" || len(got.Chapters) != 2 {
		t.Fatalf("内容错误: %+v", got)
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q，body = %d", cl, rec.Body.Len())
	}

	// HEAD 无 body
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, RouteAudio, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: code=%d body=%d", rec.Code, rec.Body.Len())
	}
}

func TestLoadAudioBook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.json")
	if err := os.WriteFile(path,
		[]byte(`{"id":"b","title":"t","chapters":[{"index":1}]}`), 0o644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	p := newProxy(t, &fakeResolver{}, "")
	if err := p.LoadAudioBook(path); err != nil {
		t.Fatalf("LoadAudioBook: %v", err)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteAudio, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"index":1`) {
		t.Fatalf("内容错误: %d %s", rec.Code, rec.Body.String())
	}

	if err := p.LoadAudioBook(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("文件不存在应报错")
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{`), 0o644)
	if err := p.LoadAudioBook(bad); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

func TestStartAndURLs(t *testing.T) {
	p := newProxy(t, &fakeResolver{}, "")
	// 未启动时地址为空
	if got := p.Addr(); got != "" {
		t.Errorf("未启动 Addr = %q", got)
	}
	if got := p.URL("v", "q"); got != "" {
		t.Errorf("未启动 URL = %q", got)
	}

	if err := p.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	addr := p.Addr()
	if addr == "" {
		t.Fatal("Start 后 Addr 为空")
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("Addr = %q", addr)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if port == "0" {
		t.Error(":0 应被替换成真实端口")
	}

	// URL 形状
	wantPrefix := "http://" + addr
	if got := p.URL("vid 1", "1080p"); !strings.HasPrefix(got, wantPrefix+RouteMedia) {
		t.Errorf("URL = %q", got)
	} else if strings.Contains(got, " ") {
		t.Errorf("URL 未编码: %q", got)
	}
	if got := p.ImageURL("https://x/a.png"); !strings.HasPrefix(got, wantPrefix+RouteImage) {
		t.Errorf("ImageURL = %q", got)
	}
	if p.LocalImageURL("k") != p.ImageURL("k") {
		t.Error("LocalImageURL 应等价于 ImageURL")
	}
	if got := p.AudioURL(); got != wantPrefix+RouteAudio {
		t.Errorf("AudioURL = %q", got)
	}

	// 真的能连上
	resp, err := http.Get(p.URL("v", "q"))
	if err != nil {
		t.Fatalf("请求本地代理: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get(HeaderAllowOrigin) != "*" {
		t.Errorf("真实请求缺 CORS")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := p.Addr(); got != "" {
		t.Errorf("Close 后 Addr = %q", got)
	}
	// 重复 Close 无错
	if err := p.Close(); err != nil {
		t.Errorf("重复 Close: %v", err)
	}
}

func TestStartBadAddr(t *testing.T) {
	p := newProxy(t, &fakeResolver{}, "")
	if err := p.Start("256.256.256.256:99999"); err == nil {
		t.Error("非法地址应报错")
	}
}

func TestSetResolverAfterStart(t *testing.T) {
	p := newProxy(t, nil, "")
	if err := p.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	// 启动时无 Resolver，请求失败
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"v/q", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("无 Resolver 状态码 = %d", rec.Code)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "v.mp4", time.Time{}, strings.NewReader("data"))
	}))
	defer up.Close()

	p.SetResolver(&fakeResolver{videoURL: up.URL})
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteMedia+"v/q", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("注入 Resolver 后状态码 = %d", rec.Code)
	}
}

func TestConcurrentImageRequests(t *testing.T) {
	// 同一 key 的并发缓存读写应返回完整图片。
	const image = "\x89PNG\r\n\x1a\nfakepngdata"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, image)
	}))
	defer up.Close()

	p := newProxy(t, &fakeResolver{imageURL: up.URL}, t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"same-key", nil))
			if rec.Code != http.StatusOK || rec.Body.String() != image {
				t.Errorf("并发状态码 = %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}

func TestDumpResponseHeaderShape(t *testing.T) {
	// 明确断言三条 CORS 头的名字与值，防止被误改。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "x")
	}))
	defer up.Close()

	p := newProxy(t, &fakeResolver{imageURL: up.URL}, "")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+"k", nil))
	for _, c := range []struct{ name, want string }{
		{"Access-Control-Allow-Origin", "*"},
		{"Access-Control-Allow-Headers", "*"},
		{"Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges"},
	} {
		if got := rec.Header().Get(c.name); got != c.want {
			t.Errorf("%s = %q，期望 %q", c.name, got, c.want)
		}
	}
}

func TestApplyHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	applyImageHeaders(req)
	if req.Header.Get("Referer") == "" || req.Header.Get("User-Agent") == "" {
		t.Error("图片请求头缺失")
	}
	req2, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	applyMediaHeaders(req2, "")
	if req2.Header.Get("Referer") != imageReferer {
		t.Errorf("空 referer 应回落到默认值，实际 %q", req2.Header.Get("Referer"))
	}
	if req2.Header.Get("Accept-Encoding") != "identity" {
		t.Errorf("Accept-Encoding 应为 identity，实际 %q", req2.Header.Get("Accept-Encoding"))
	}
	req3, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	applyMediaHeaders(req3, "https://r.example/")
	if req3.Header.Get("Referer") != "https://r.example/" {
		t.Errorf("Referer = %q", req3.Header.Get("Referer"))
	}
}

func TestErrorSentinels(t *testing.T) {
	if ErrNoResolver == nil || ErrBadKey == nil || ErrNotServing == nil {
		t.Fatal("错误哨兵不应为 nil")
	}
	if !strings.Contains(fmt.Sprint(ErrNoResolver), "Resolver") {
		t.Errorf("ErrNoResolver 文案 = %q", ErrNoResolver)
	}
}
