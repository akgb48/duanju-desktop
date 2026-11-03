package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"testing"
)

// TestEnvelopeLowerStyle 验证小写信封（内容系接口）。
//
// 真实响应（GET /reading/bookapi/bookmall/tab/v）：
//
//	{"code":0,"message":"SUCCESS","data":{...}}
func TestEnvelopeLowerStyle(t *testing.T) {
	var e Envelope
	raw := []byte(`{"code":0,"message":"SUCCESS","data":{"tab_index":0}}`)
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if e.Style != StyleLower {
		t.Errorf("风格应为 %s，实际 %s", StyleLower, e.Style)
	}
	if !e.OK() {
		t.Errorf("应为成功: %+v", e)
	}
	if e.Message != "SUCCESS" {
		t.Errorf("message 不符: %q", e.Message)
	}
	if len(e.Data) == 0 {
		t.Error("data 不应为空")
	}
}

// TestEnvelopePascalStyle 验证 PascalCase 信封（评论/passport 系接口）。
//
// 真实响应（GET /novel/commentapi/comment/list/）：
//
//	{"Code":110001,"Message":"未知异常","BaseResp":{"StatusCode":110001,...}}
func TestEnvelopePascalStyle(t *testing.T) {
	var e Envelope
	raw := []byte(`{"Code":110001,"Message":"未知异常","BaseResp":{"StatusMessage":"unknown error","StatusCode":110001}}`)
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if e.Style != StylePascal {
		t.Errorf("风格应为 %s，实际 %s", StylePascal, e.Style)
	}
	if e.OK() {
		t.Error("110001 不应视为成功")
	}
	if e.Code != 110001 {
		t.Errorf("code 不符: %d", e.Code)
	}
	if e.Message != "未知异常" {
		t.Errorf("message 不符: %q", e.Message)
	}
}

// TestEnvelopeBareData 验证只有 data 的信封。
func TestEnvelopeBareData(t *testing.T) {
	var e Envelope
	if err := json.Unmarshal([]byte(`{"data":{"ok":1}}`), &e); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if e.Style != StyleBare {
		t.Errorf("风格应为 %s，实际 %s", StyleBare, e.Style)
	}
	if !e.OK() {
		t.Error("无 code 时应视为成功")
	}
}

// TestEnvelopeRejectGarbage 验证非 JSON 无法解析。
func TestEnvelopeRejectGarbage(t *testing.T) {
	var e Envelope
	if err := json.Unmarshal([]byte(`not json at all`), &e); err == nil {
		t.Error("垃圾输入应报错")
	}
}

// TestEnvelopeEmptyBody 验证空响应体。
//
// 这是实测遇到的关键场景：占位签名头导致服务端返回 200 + 空体。
// 解析必须报错，不能静默当成成功。
func TestEnvelopeEmptyBody(t *testing.T) {
	var e Envelope
	if err := json.Unmarshal([]byte(``), &e); err == nil {
		t.Error("空体应报错")
	}
}

// TestBizErrorFromPascal 验证 PascalCase 业务错误转 BizError。
func TestBizErrorFromPascal(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Code":100004,"Message":"网络错误","BaseResp":{"StatusCode":100004}}`))
	})
	res, err := s.Call(t.Context(), "user_info", nil)
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
		t.Fatalf("应为 *BizError，实际 %T", err)
	}
	if be.Code != 100004 {
		t.Errorf("code 不符: %d", be.Code)
	}
}

// TestGetUsesQueryNotBody 验证 GET 走查询串、POST 走请求体。
//
// 这是实测踩到的坑：全部塞进 body 时 GET 接口拿不到参数，
// 服务端返回空体，feed 永远是 0 条。
func TestGetUsesQueryNotBody(t *testing.T) {
	var getBodyLen int
	var getQuery string
	var postBody map[string]any

	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			b := make([]byte, 0)
			if r.Body != nil {
				tmp := make([]byte, 4096)
				n, _ := r.Body.Read(tmp)
				b = tmp[:n]
			}
			getBodyLen = len(b)
			getQuery = r.URL.RawQuery
		} else {
			var body = r.Body
			if r.Header.Get("content-encoding") == "gzip" {
				if zr, err := gzip.NewReader(r.Body); err == nil {
					defer zr.Close()
					body = zr
				}
			}
			tmp := make([]byte, 8192)
			n, _ := body.Read(tmp)
			_ = json.Unmarshal(tmp[:n], &postBody)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"SUCCESS","data":{}}`))
	})

	// feed 在注册表里是 GET。
	if _, err := s.CallJSON(t.Context(), "feed", map[string]any{"tab_type": 0}); err != nil {
		t.Fatalf("feed 调用失败: %v", err)
	}
	if getBodyLen != 0 {
		t.Errorf("GET 不应带请求体，实际 %d 字节", getBodyLen)
	}
	if getQuery == "" {
		t.Error("GET 应把参数拼进查询串")
	}

	// comment_add 是 POST（写接口），参数应进请求体。
	if _, err := s.CallJSON(t.Context(), "comment_add", map[string]any{"book_id": "42"}); err != nil {
		t.Fatalf("comment_add 调用失败: %v", err)
	}
	if postBody["book_id"] != "42" {
		t.Errorf("POST 参数应在请求体: %v", postBody)
	}
}
