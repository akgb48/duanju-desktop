package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// selfTest 是启动自检：把关键链路的真实结果写进日志。
//
// 只有在 HG_SELFTEST=1 时由 OnStartup 起一个 goroutine 调用。
// 目的：在拿不到 DevTools 的机器上也能确认「接口到底通没通」。
func (a *App) selfTest() {
	out := filepath.Join(os.TempDir(), "hg-selftest.log")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	logln := func(format string, args ...any) {
		fmt.Fprintf(f, format+"\n", args...)
	}

	logln("=== hgplayer 启动自检 %s ===", time.Now().Format(time.RFC3339))
	logln("数据目录: %s", a.appInfo().DataDir)
	logln("媒体代理: %s", a.proxyBase)
	logln("注册接口: %d 条", apiCount())
	logln("前端绑定: %d 个", BindingCount)
	logln("")

	// 1. 页签
	tabs, err := a.FeedCategories()
	if err != nil {
		logln("[FAIL] FeedCategories: %v", err)
	} else {
		logln("[ OK ] FeedCategories: %d 个", len(tabs))
		for _, t := range tabs {
			logln("        id=%-3s name=%s", t.ID, t.Name)
		}
	}

	// 2. 推荐流
	page, err := a.RecommendTab(0, "")
	if err != nil {
		logln("[FAIL] RecommendTab: %v", err)
	} else {
		logln("[ OK ] RecommendTab: %d 条 hasMore=%v", len(page.Items), page.HasMore)
		for i, it := range page.Items {
			if i >= 5 {
				logln("        ...")
				break
			}
			logln("        [%d] id=%s title=%s cover=%.60s",
				i, it.SeriesID, it.Title, it.Cover)
		}
	}

	// 3. 榜单
	if r, err := a.Rank("", ""); err != nil {
		logln("[FAIL] Rank: %v", err)
	} else {
		logln("[ OK ] Rank: %d 条", len(r.Items))
	}

	// 4. 搜索（用推荐的第一个书名当关键词，成功率高）
	kw := ""
	if page != nil && len(page.Items) > 0 {
		kw = page.Items[0].Title
	}
	if kw != "" {
		if sr, err := a.Search(kw, ""); err != nil {
			logln("[FAIL] Search(%q): %v", kw, short(err.Error()))
		} else {
			logln("[ OK ] Search(%q): %d 条", kw, len(sr.Series))
		}
	}

	// 5. 详情（用第一条的 seriesId）
	if page != nil && len(page.Items) > 0 {
		sid := page.Items[0].SeriesID
		if d, err := a.SeriesDetail(sid); err != nil {
			logln("[FAIL] SeriesDetail(%s): %v", sid, short(err.Error()))
		} else {
			logln("[ OK ] SeriesDetail(%s): title=%s episodeCnt=%d",
				sid, d.Title, d.EpisodeCnt)
		}
	}

	logln("")
	logln("=== 自检结束 ===")
}

// short 截断错误信息，日志里够看就行。
func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= 160 {
		return s
	}
	return s[:160] + "..."
}
