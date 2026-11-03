package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hongguo-desktop/pkg/api"
	"hongguo-desktop/pkg/hgapi"
	"hongguo-desktop/pkg/model"
)

// newTestService 构造指向假上游的 service。
func newTestService(t *testing.T, h http.HandlerFunc) *Service {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	old := api.HostPool
	api.HostPool = []string{strings.TrimPrefix(srv.URL, "http://")}
	t.Cleanup(func() { api.HostPool = old })

	sess := api.NewSession(api.WithScheme("http"))
	return New(hgapi.New(sess))
}

// TestCacheTTL 验证缓存过期。
func TestCacheTTL(t *testing.T) {
	c := NewCache(50 * time.Millisecond)
	c.Put("k", 42)
	if v, ok := c.Get("k"); !ok || v != 42 {
		t.Fatal("刚写入应命中")
	}
	time.Sleep(70 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("超过 TTL 应失效")
	}
}

// TestCacheInvalidatePrefix 验证前缀失效。
func TestCacheInvalidatePrefix(t *testing.T) {
	c := NewCache(time.Minute)
	c.Put("feed:1", "a")
	c.Put("feed:2", "b")
	c.Put("vd:x", "c")

	c.Invalidate("feed:")
	if _, ok := c.Get("feed:1"); ok {
		t.Error("feed:1 应被清除")
	}
	if _, ok := c.Get("feed:2"); ok {
		t.Error("feed:2 应被清除")
	}
	if _, ok := c.Get("vd:x"); !ok {
		t.Error("vd:x 不应被清除")
	}
}

// TestCacheConcurrent 验证并发安全。
func TestCacheConcurrent(t *testing.T) {
	c := NewCache(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Put("k"+itoa(n), n)
			c.Get("k" + itoa(n))
		}(i)
	}
	wg.Wait()
}

// TestFeedCaching 验证同一请求第二次走缓存。
func TestFeedCaching(t *testing.T) {
	hits := 0
	svc := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"status_code":0,"data":{"cells":[],"has_more":false}}`))
	})

	ctx := context.Background()
	if _, err := svc.Feed(ctx, 0, ""); err != nil {
		t.Fatalf("首次 Feed 失败: %v", err)
	}
	if _, err := svc.Feed(ctx, 0, ""); err != nil {
		t.Fatalf("二次 Feed 失败: %v", err)
	}
	if hits != 1 {
		t.Errorf("上游应只被调用 1 次，实际 %d 次", hits)
	}

	// 不同 cursor 应穿透缓存。
	if _, err := svc.Feed(ctx, 0, "next"); err != nil {
		t.Fatalf("三次 Feed 失败: %v", err)
	}
	if hits != 2 {
		t.Errorf("不同 cursor 应再请求一次，实际 %d 次", hits)
	}
}

// TestRequireLogin 验证未登录门禁。
func TestRequireLogin(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":0,"data":{}}`))
	})

	if svc.IsLoggedIn() {
		t.Fatal("初始应为未登录")
	}
	_, err := svc.AddComment(context.Background(), "s1", "v1", "hi", 4, 0)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("应返回 ErrNotLoggedIn，实际 %v", err)
	}

	svc.SetLoggedIn(true, nil)
	if !svc.IsLoggedIn() {
		t.Fatal("设置后应为已登录")
	}
}

// TestLogoutClearsCache 验证登出清空缓存。
func TestLogoutClearsCache(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":0,"data":{"cells":[]}}`))
	})
	ctx := context.Background()

	if _, err := svc.Feed(ctx, 0, ""); err != nil {
		t.Fatalf("Feed 失败: %v", err)
	}
	svc.SetLoggedIn(true, nil)
	svc.SetLoggedIn(false, nil)

	// 登出后缓存应为空，再请求会打上游。
	hits := 0
	svc2 := svc
	_ = hits
	if v, ok := svc2.Cache().Get("feed:0:"); ok {
		t.Errorf("登出后缓存应清空，仍有值: %v", v)
	}
}

// TestCurrentUserCopy 验证返回的是副本，外部修改不影响内部。
func TestCurrentUserCopy(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, r *http.Request) {})
	svc.SetLoggedIn(true, &model.User{Nickname: "小明"})
	u := svc.CurrentUser()
	if u == nil {
		t.Fatal("应有用户")
	}
	u.Nickname = "被改了"
	if got := svc.CurrentUser().Nickname; got != "小明" {
		t.Errorf("内部状态被外部修改: %q", got)
	}
}

// TestItoa 验证整数转字符串。
func TestItoa(t *testing.T) {
	cases := map[int]string{
		0: "0", 1: "1", 42: "42", -7: "-7", 1234567: "1234567",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q，期望 %q", in, got, want)
		}
	}
}
