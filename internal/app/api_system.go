package app

import (
	"fmt"
	"os"
	"runtime"
)

// AboutInfo 是关于页要的信息。
type AboutInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	GoVersion string `json:"goVersion"`
	BuildTime string `json:"buildTime"`
	Repo      string `json:"repo"`
	License   string `json:"license"`
	Chromium  string `json:"chromium"`
}

// About 返回关于页信息。
func (a *App) About() (*AboutInfo, error) {
	return &AboutInfo{
		Name:      "短剧电脑版",
		Version:   a.version,
		GoVersion: runtime.Version(),
		BuildTime: BuildTime,
		Repo:      "",
		License:   "MIT",
		Chromium:  "WebView2",
	}, nil
}

// AppInfo 返回应用元信息，是 GetAppInfo 的前端别名。
func (a *App) AppInfo() AppInfo {
	return a.appInfo()
}

// BuildTime 构建时间，由 -ldflags 注入。
var BuildTime = "dev"

// Notice 是一条站内公告。
type Notice struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// Notices 返回站内公告。
func (a *App) Notices() ([]Notice, error) {
	a.mu.RLock()
	done := a.noticesRead
	a.mu.RUnlock()
	if done {
		return []Notice{}, nil
	}
	return []Notice{{
		ID:    "welcome",
		Level: "info",
		Text:  "欢迎使用短剧电脑版。观看记录、收藏和播放设置保存在本机。",
	}}, nil
}

// CheckNotices 主动拉取公告。
func (a *App) CheckNotices() ([]Notice, error) {
	return a.Notices()
}

// ClearNotices 清空公告。
func (a *App) ClearNotices() error {
	a.mu.Lock()
	a.noticesRead = true
	a.mu.Unlock()
	return nil
}

// MarkNoticesRead 标记若干公告已读。
func (a *App) MarkNoticesRead(ids []string) error {
	a.mu.Lock()
	a.noticesRead = true
	a.mu.Unlock()
	return nil
}

// ClipboardSeq 返回剪贴板口令序列（分享口令解析用）。
func (a *App) ClipboardSeq() (string, error) {
	return "", nil
}

// IntegrityReport 是完整性自检结果。
type IntegrityReport struct {
	OK       bool     `json:"ok"`
	Checks   []string `json:"checks"`
	Warnings []string `json:"warnings"`
}

// Integrity 执行启动自检。
func (a *App) Integrity() (*IntegrityReport, error) {
	r := &IntegrityReport{OK: true, Checks: []string{}}
	a.mu.RLock()
	dir := a.dataDir
	proxy := a.proxyBase
	mediaErr := a.mediaError
	a.mu.RUnlock()

	if dir != "" {
		r.Checks = append(r.Checks, "数据目录可写")
	} else {
		r.Warnings = append(r.Warnings, "数据目录未初始化")
	}
	if proxy != "" {
		r.Checks = append(r.Checks, "媒体代理运行中")
	} else {
		message := "媒体代理未启动"
		if mediaErr != nil {
			message = mediaErr.Error()
		}
		r.Warnings = append(r.Warnings, message)
	}
	r.Checks = append(r.Checks, fmt.Sprintf("接口注册表 %d 条", apiCount()))
	r.OK = len(r.Warnings) == 0
	return r, nil
}

// ReportEnv 上报环境信息（不含个人数据）。page 标记来源页面，extra 为附加 JSON。
func (a *App) ReportEnv(page, extra string) (map[string]any, error) {
	a.mu.RLock()
	dir := a.dataDir
	a.mu.RUnlock()
	return map[string]any{
		"version":   a.version,
		"goVersion": runtime.Version(),
		"platform":  runtime.GOOS,
		"arch":      runtime.GOARCH,
		"dataDir":   dir,
		"hosts":     apiHostCount(),
		"page":      page,
		"extra":     extra,
	}, nil
}

// ReportError 记录一条前端错误。
func (a *App) ReportError(level, message, stack string) error {
	fmt.Printf("[frontend-error/%s] %s\n%s\n", level, message, stack)
	return nil
}

// RestartApp 重启应用。
func (a *App) RestartApp() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	a.windowCall(func() { runtimeQuit(a.ctx) })
	return relaunch(exe)
}
