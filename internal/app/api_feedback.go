package app

import (
	"errors"
	"fmt"
)

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
