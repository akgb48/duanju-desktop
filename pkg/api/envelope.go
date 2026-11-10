package api

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Envelope 是上游接口的响应信封。
//
// 实测发现服务端存在两种命名风格，客户端必须同时兼容：
//
//  1. 小写风格（内容系接口，如 feed / search / video_detail）：
//     {"code":0,"message":"SUCCESS","data":{...}}
//
//  2. PascalCase 风格（评论、passport 等由另一套 Go 服务提供）：
//     {"Code":110001,"Message":"未知异常","BaseResp":{"StatusCode":110001,...}}
//
// 两者不能靠 json tag 同时匹配，因此 UnmarshalJSON 里做了双路解析。
// 证据：GET /reading/bookapi/bookmall/tab/v 与 GET /novel/commentapi/comment/list/
// 的实际响应体（见 docs/protocol/envelope.md）。
type Envelope struct {
	HTTPCode int             `json:"-"`
	Code     int             `json:"-"`
	Message  string          `json:"-"`
	Data     json.RawMessage `json:"-"`
	// LogID 便于排查服务端问题，部分接口返回。
	LogID string `json:"-"`
	// Style 记录本条响应命中的命名风格，便于诊断。
	Style string `json:"-"`
}

// 响应信封的两种命名风格。
const (
	StyleLower  = "lower"  // code / message / data
	StylePascal = "pascal" // Code / Message / BaseResp
	StyleBare   = "bare"   // 只有 data，没有状态字段
)

// UnmarshalJSON 双路解析信封。
//
// 关键陷阱：encoding/json 的字段匹配是大小写不敏感的，
// 所以 `Code int json:"code"` 会同时吃掉 PascalCase 的 "Code"。
// 必须先看键的**确切拼写**再决定走哪条分支，
// 否则 110001 这类错误码会被误判成小写风格的成功响应。
func (e *Envelope) UnmarshalJSON(b []byte) error {
	*e = Envelope{HTTPCode: e.HTTPCode}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if !json.Valid(trimmed) {
			return fmt.Errorf("无法识别的响应信封: %s", truncate(string(b), 120))
		}
		e.Data = append(json.RawMessage(nil), trimmed...)
		e.Style = StyleBare
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil || probe == nil {
		return fmt.Errorf("无法识别的响应信封: %s", truncate(string(b), 120))
	}

	_, hasLowerCode := probe["code"]
	_, hasUpperCode := probe["Code"]

	switch {
	case hasLowerCode:
		if err := json.Unmarshal(probe["code"], &e.Code); err != nil {
			return err
		}
		e.Style = StyleLower
	case hasUpperCode:
		if err := json.Unmarshal(probe["Code"], &e.Code); err != nil {
			return err
		}
		e.Style = StylePascal
	}
	for _, key := range []string{"message", "Message"} {
		if raw, ok := probe[key]; ok {
			if err := json.Unmarshal(raw, &e.Message); err != nil {
				return err
			}
			break
		}
	}
	if raw, ok := probe["log_id"]; ok {
		if err := json.Unmarshal(raw, &e.LogID); err != nil {
			return err
		}
	}

	// A zero/missing outer Code does not override a denial in BaseResp. The
	// reference client uses this status before deciding whether to recover.
	if raw, ok := probe["BaseResp"]; ok {
		var base struct {
			Code    int    `json:"StatusCode"`
			Message string `json:"StatusMessage"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return err
		}
		if e.Code == 0 {
			e.Code = base.Code
		}
		if e.Message == "" {
			e.Message = base.Message
		}
		if e.Style == "" {
			e.Style = StylePascal
		}
	}
	// Read data keys exactly; encoding/json otherwise lets Data overwrite data
	// according to field order when a response contains both spellings.
	if raw, ok := probe["data"]; ok {
		e.Data = raw
	} else if raw, ok := probe["Data"]; ok {
		e.Data = raw
	}
	if e.Style != "" {
		return nil
	}

	// 没有状态字段：只有 data，或整个响应体就是 data。
	if len(e.Data) > 0 {
		e.Style = StyleBare
		return nil
	}

	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		e.Data = append(json.RawMessage(nil), trimmed...)
		e.Style = StyleBare
		return nil
	}

	return fmt.Errorf("无法识别的响应信封: %s", truncate(string(b), 120))
}

// OK 报告业务是否成功。
func (e *Envelope) OK() bool { return e != nil && e.Code == 0 }

// BizError 是业务错误，附带服务端原始信息。
type BizError struct {
	Action   string
	Code     int
	HTTPCode int
	Message  string
	LogID    string
}

func (e *BizError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "未知错误"
	}
	return fmt.Sprintf("api[%s]: %s (code=%d http=%d)", e.Action, msg, e.Code, e.HTTPCode)
}

// Result 是 CallJSON / Call 的返回：已解析的信封 + 原始字节。
type Result struct {
	Action   string
	Envelope Envelope
	Raw      []byte
	// ResponseToken is the token sent by this response, if present.
	ResponseToken string `json:"-"`
}

// Decode 把 data 段解到 v。
func (r *Result) Decode(v any) error {
	if !r.Envelope.OK() {
		return &BizError{
			Action:   r.Action,
			Code:     r.Envelope.Code,
			HTTPCode: r.Envelope.HTTPCode,
			Message:  r.Envelope.Message,
			LogID:    r.Envelope.LogID,
		}
	}
	if len(r.Envelope.Data) == 0 {
		return fmt.Errorf("api[%s]: 响应没有 data 字段", r.Action)
	}
	if err := json.Unmarshal(r.Envelope.Data, v); err != nil {
		return fmt.Errorf("api[%s]: 解析 data 失败: %w", r.Action, err)
	}
	return nil
}

// DecodeData 返回 data 原始 JSON。
func (r *Result) DecodeData() (json.RawMessage, error) {
	if !r.Envelope.OK() {
		return nil, &BizError{
			Action:   r.Action,
			Code:     r.Envelope.Code,
			HTTPCode: r.Envelope.HTTPCode,
			Message:  r.Envelope.Message,
			LogID:    r.Envelope.LogID,
		}
	}
	if len(r.Envelope.Data) == 0 {
		return nil, fmt.Errorf("api[%s]: 响应没有 data 字段", r.Action)
	}
	return r.Envelope.Data, nil
}

// truncate 截断字符串用于错误信息。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
