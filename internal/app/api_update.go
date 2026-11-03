package app

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"hongguo-desktop/pkg/model"
)

// 本文件覆盖 bridge.js 的「更新 / 反馈与分享」两组绑定。

// UpdateStateView 是更新状态快照。
type UpdateStateView struct {
	Stage     string `json:"stage"` // idle|checking|available|downloading|ready|applying|failed
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Notes     string `json:"notes"`
	Progress  int    `json:"progress"`
	Speed     int64  `json:"speed"`
	Error     string `json:"error"`
	Mandatory bool   `json:"mandatory"`
}

// UpdateState 返回当前更新状态。
func (a *App) UpdateState() (*UpdateStateView, error) {
	a.mu.RLock()
	st := a.update
	a.mu.RUnlock()
	st.Current = a.version
	if st.Stage == "" {
		st.Stage = "idle"
	}
	return &st, nil
}

// CheckUpdate 检查更新。不自动下载。
func (a *App) CheckUpdate() (*UpdateStateView, error) {
	return nil, errors.New("当前版本暂未配置更新发布服务")
}

// DownloadUpdate 下载更新包。
func (a *App) DownloadUpdate() error {
	return errors.New("更新下载与原子替换尚未实现")
}

// ApplyUpdate 应用已下载的更新并重启。
func (a *App) ApplyUpdate() error {
	return errors.New("没有经过验证的更新包可供安装")
}

// ---------------------------------------------------------------- 反馈与分享（6）

// 反馈数据与窗口运行时分开维护，保留现有接口契约。
var memFeedback = map[string]FeedbackThread{}

// FeedbackThread 是一条反馈及其会话。
type FeedbackThread struct {
	ID        string        `json:"id"`
	Category  string        `json:"category"`
	Content   string        `json:"content"`
	Email     string        `json:"email"`
	Status    string        `json:"status"`
	Reply     string        `json:"reply"`
	CreatedAt int64         `json:"createdAt"`
	RepliedAt int64         `json:"repliedAt"`
	Messages  []FeedbackMsg `json:"messages"`
}

// FeedbackMsg 是反馈会话里的一条消息。
type FeedbackMsg struct {
	From    string `json:"from"`
	Content string `json:"content"`
	At      int64  `json:"at"`
}

// MyFeedback 返回本地记录的反馈列表。
func (a *App) MyFeedback() ([]FeedbackThread, error) {
	memMu.RLock()
	defer memMu.RUnlock()
	out := make([]FeedbackThread, 0, len(memFeedback))
	for _, v := range memFeedback {
		out = append(out, v)
	}
	return out, nil
}

// ReplyFeedback 在已有反馈下追加一条消息。
func (a *App) ReplyFeedback(id, text string) error {
	if id == "" || text == "" {
		return errors.New("反馈 ID 与内容不能为空")
	}
	memMu.Lock()
	defer memMu.Unlock()
	t, ok := memFeedback[id]
	if !ok {
		return fmt.Errorf("未找到反馈 %s", id)
	}
	t.Messages = append(t.Messages, FeedbackMsg{From: "user", Content: text})
	memFeedback[id] = t
	return nil
}

// SubmitFeedback 提交新反馈。
func (a *App) SubmitFeedback(category, content, contact string) (string, error) {
	if content == "" {
		return "", errors.New("请填写反馈内容")
	}
	id := fmt.Sprintf("fb-%d", nowUnix())
	memMu.Lock()
	memFeedback[id] = FeedbackThread{
		ID:        id,
		Category:  category,
		Content:   content,
		Email:     contact,
		Status:    "open",
		CreatedAt: nowUnix(),
		Messages:  []FeedbackMsg{{From: "user", Content: content, At: nowUnix()}},
	}
	memMu.Unlock()
	return id, nil
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

// ResolveShare 解析分享链接或口令。
func (a *App) ResolveShare(text string) (*model.Series, error) {
	if text == "" {
		return nil, errors.New("请粘贴分享链接或口令")
	}
	id := extractSeriesID(text)
	if id == "" {
		return nil, errors.New("无法从内容中识别剧集 ID")
	}
	return a.seriesDetail(id)
}

// ShareLink 生成某剧的分享链接。kind 1=短链 2=口令。
func (a *App) ShareLink(seriesID string, kind int) (string, error) {
	if seriesID == "" {
		return "", errors.New("缺少剧集 ID")
	}
	switch kind {
	case 2:
		return fmt.Sprintf("【红果短剧】发现一部好剧 %s", seriesID), nil
	default:
		return fmt.Sprintf("https://hg.example/share/%s", seriesID), nil
	}
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

// extractSeriesID 从文本里抠出剧集 ID（纯数字段）。
func extractSeriesID(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			continue
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j-i >= 6 {
			return s[i:j]
		}
		i = j
	}
	return ""
}
