package api

import "testing"

// TestRegistrySize 守卫接口表条数。改动 endpoints.go 时必须同步常量与文档。
func TestRegistrySize(t *testing.T) {
	got := DefaultRegistry.Len()
	if got != RegistrySize {
		t.Fatalf("接口数不一致: 注册表 %d 条，期望 %d 条。"+
			"若为有意变更，请同步更新 RegistrySize 与 docs/protocol/endpoints.md", got, RegistrySize)
	}
}

// TestNoDuplicateAction 确保 action 全局唯一。
func TestNoDuplicateAction(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range DefaultRegistry.All() {
		if seen[a.Action] {
			t.Fatalf("重复 action: %s", a.Action)
		}
		seen[a.Action] = true
	}
}

// TestAllPathsValid 校验每条接口的 path 与 method 合法。
func TestAllPathsValid(t *testing.T) {
	for _, a := range DefaultRegistry.All() {
		if len(a.Path) == 0 || a.Path[0] != '/' {
			t.Errorf("api[%s]: path 必须以 / 开头，实际 %q", a.Action, a.Path)
		}
		if a.Method != GET && a.Method != POST {
			t.Errorf("api[%s]: 非法 method %q", a.Action, a.Method)
		}
		if a.Route == 0 {
			t.Errorf("api[%s]: 未设置 Route", a.Action)
		}
	}
}

// TestRouteDistribution 校验四族的条数与设计一致。
//
// 分组依据是路径前缀：
//   - /reading/ (26) + /novel/ (12) = 38 → RouteContent
//   - /passport/ (7)                = 7  → RoutePassport
//   - /service/  (2)                = 2  → RouteBase
//   - /api/v1/   (4)                = 4  → RouteUpdate
func TestRouteDistribution(t *testing.T) {
	want := map[Route]int{
		RouteContent:  38,
		RoutePassport: 7,
		RouteBase:     2,
		RouteUpdate:   4,
	}
	got := map[Route]int{}
	for _, a := range DefaultRegistry.All() {
		got[a.Route]++
	}
	for r, n := range want {
		if got[r] != n {
			t.Errorf("%s 族条数不符: 实际 %d，期望 %d", r, got[r], n)
		}
	}
	sum := 0
	for _, n := range got {
		sum += n
	}
	if sum != RegistrySize {
		t.Errorf("四族合计 %d，应为 %d", sum, RegistrySize)
	}
}

func TestFixedHost(t *testing.T) {
	cases := map[string]string{
		"device_register": "log.snssdk.com",
		"settings_v3":     "is.snssdk.com",
	}
	for action, host := range cases {
		a, ok := DefaultRegistry.Get(action)
		if !ok {
			t.Fatalf("缺少接口 %s", action)
		}
		if a.Host != host {
			t.Errorf("api[%s]: host 实际 %q，期望 %q", action, a.Host, host)
		}
	}
}

// TestSharedPaths 校验共享路径的分组关系。
//
// 说明：剧评（review_list）与评论共用 /novel/commentapi/comment/list/，
// 但在客户端由同一 action 携带不同 tab_type 调用，故注册表中只有一条
// comment_list；此处只校验真实存在的多 action 共享路径。
func TestSharedPaths(t *testing.T) {
	groups := [][]string{
		{"comment_add", "danmaku_add"},
		{"rank_change", "feed_change"},
	}
	for _, g := range groups {
		var path string
		for _, action := range g {
			a, ok := DefaultRegistry.Get(action)
			if !ok {
				t.Fatalf("缺少接口 %s", action)
			}
			if path == "" {
				path = a.Path
			} else if a.Path != path {
				t.Errorf("%v 应共享同一路径，实际 %q vs %q", g, path, a.Path)
			}
		}
	}
}

// TestHostPool 校验主机池条数与首项。
func TestHostPool(t *testing.T) {
	if len(HostPool) != 15 {
		t.Fatalf("主机池应为 15 条，实际 %d", len(HostPool))
	}
	if HostPool[0] != "api5-normal-lq.fqnovel.com" {
		t.Errorf("主机池首项错误: %q", HostPool[0])
	}
	seen := map[string]bool{}
	for _, h := range HostPool {
		if seen[h] {
			t.Errorf("主机池重复: %s", h)
		}
		seen[h] = true
	}
}

// TestDefaultHeaders 校验 11 个固定头齐全。
func TestDefaultHeaders(t *testing.T) {
	if len(DefaultHeaders) != 12 {
		t.Fatalf("固定头应为 11 个业务头及 1 个压缩协商头，实际 %d", len(DefaultHeaders))
	}
	for _, k := range []string{
		"accept", "x-xs-from-web", "x-vc-bdturing-sdk-version", "lc",
		"sdk-version", "passport-sdk-version", "content-type",
		"x-tt-store-region", "x-tt-store-region-src", "x-ss-dp", "user-agent",
	} {
		if _, ok := DefaultHeaders[k]; !ok {
			t.Errorf("缺少固定头 %s", k)
		}
	}
}
