package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/service"
)

type searchTransport func(*http.Request) (*http.Response, error)

func (f searchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchKeepsEmptyResultsAndErrorsInsteadOfRecommendationFallback(t *testing.T) {
	for _, status := range []int{200, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: searchTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/reading/bookapi/search/tab/v" || r.URL.Query().Get("query") != "无匹配" {
					t.Fatal("search used a recommendation feed or untrimmed keyword")
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"search_tabs":[{"tab_type":11,"has_more":false,"data":[]}]}`))}, nil
			})}))
			a := &App{ctx: context.Background(), sess: s, svc: service.New(hgapi.New(s))}
			page, err := a.Search(" 无匹配 ", "")
			if status == 200 {
				if err != nil || page.Source != "server" || page.Series == nil || len(page.Series) != 0 {
					t.Fatalf("empty search changed into local results: %+v %v", page, err)
				}
			} else {
				var business *api.BizError
				if !errors.As(err, &business) || business.HTTPCode != 502 {
					t.Fatalf("search error was hidden: %v", err)
				}
			}
			if calls != 1 {
				t.Fatal("search made an unrelated fallback request")
			}
			if _, err := a.Search("  ", ""); err != nil || calls != 1 {
				t.Fatal("blank input sent a network request")
			}
		})
	}
}

// TestParseTabType 验证页签标识解析。
func TestParseTabType(t *testing.T) {
	cases := map[string]int{
		"":    0,
		"0":   0,
		"2":   2,
		" 4 ": 4,
	}
	for in, want := range cases {
		got, err := parseTabType(in)
		if err != nil {
			t.Errorf("parseTabType(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseTabType(%q) = %d，期望 %d", in, got, want)
		}
	}
	if _, err := parseTabType("abc"); err == nil {
		t.Error("非法输入应报错")
	}
}

// TestFirstNonEmpty 验证取第一个非空串。
func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("期望 x，实际 %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("全空应返回空串，实际 %q", got)
	}
}

// TestShort 验证日志截断。
func TestShort(t *testing.T) {
	long := strings.Repeat("错", 300)
	got := short(long)
	if len(got) > 170 {
		t.Errorf("截断后仍过长: %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("截断后应有省略号")
	}
	if got := short("短"); got != "短" {
		t.Errorf("短文本不应改动: %q", got)
	}
}
