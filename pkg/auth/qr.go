// Package auth implements the website QR passport protocol separately from the
// Android content API. The cookie jar belongs to one QR attempt only.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const Origin = "https://hongguoduanju.com"
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func XorEncode(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] ^= 5
	}
	return hex.EncodeToString(b)
}

type QR struct {
	client  *http.Client
	origin  string
	token   string
	expires int64
}
type Code struct {
	Token    string `json:"token"`
	Image    string `json:"image"` // PNG base64, without a data URI prefix.
	ExpireAt int64  `json:"expireAt"`
}
type Poll struct {
	State   string
	Cookies string
}

func NewQR() *QR { return newQR(Origin) }
func newQR(origin string) *QR {
	jar, _ := cookiejar.New(nil)
	q := &QR{origin: origin}
	q.client = &http.Client{Timeout: 20 * time.Second, Jar: jar, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("登录跳转次数过多")
		}
		return q.allowURL(r.URL)
	}}
	return q
}
func (q *QR) allowURL(u *url.URL) error {
	base, _ := url.Parse(q.origin)
	if u.Scheme == base.Scheme && u.Host == base.Host {
		return nil
	}
	if u.Scheme == "https" && (u.Hostname() == "hongguoduanju.com" || strings.HasSuffix(u.Hostname(), ".hongguoduanju.com")) {
		return nil
	}
	return errors.New("登录接口返回了不支持的跳转地址")
}
func (q *QR) params() url.Values {
	source := `{"hardwareConcurrency":8,"webdriver":false,"chromedriver":false,"shelldriver":false,"plugins":5,"permissions":[{"name":"notifications","state":"prompt"}],"innerHeight":760,"innerWidth":1480,"outerHeight":760,"outerWidth":1480}`
	return url.Values{
		"aid": {"513755"}, "language": {"zh"}, "account_sdk_source": {"web"},
		"account_sdk_source_info": {XorEncode(source)}, "is_from_ttaccountsdk": {"1"},
		"passport_jssdk_version": {"2.4.13"}, "passport_jssdk_type": {"normal"},
		"p_js_v": {"2.4.13"}, "p_js_t": {"pro"}, "p_zt": {"3.3.5"},
		"p_ver": {"1.0.29"}, "p_bd": {"1.0.1.20"}, "request_host": {url.QueryEscape(q.origin)},
		"ts": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "next": {q.origin + "/"},
	}
}
func (q *QR) get(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, errors.New("登录地址无效")
	}
	if err = q.allowURL(u); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", browserUA)
	r.Header.Set("Referer", q.origin+"/")
	res, err := q.client.Do(r)
	if err != nil {
		return nil, errors.New("连接登录服务失败，请稍后重试")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("登录服务 HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 2<<20))
}
func (q *QR) getJSON(ctx context.Context, path string, p url.Values, v any) error {
	b, err := q.get(ctx, q.origin+path+"?"+p.Encode())
	if err != nil {
		return err
	}
	var root struct {
		Data      json.RawMessage `json:"data"`
		Message   string          `json:"message"`
		ErrorCode int             `json:"error_code"`
	}
	if json.Unmarshal(b, &root) != nil || len(root.Data) == 0 {
		return errors.New("登录服务返回无效数据")
	}
	if root.ErrorCode != 0 {
		return fmt.Errorf("登录服务错误（%d）", root.ErrorCode)
	}
	var status struct {
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if json.Unmarshal(root.Data, &status) != nil {
		return errors.New("登录服务返回无效数据")
	}
	if status.ErrorCode != 0 || root.Message == "error" {
		return fmt.Errorf("登录服务拒绝请求（%d），请刷新二维码或使用短信登录", status.ErrorCode)
	}
	if err = json.Unmarshal(root.Data, v); err != nil {
		return errors.New("登录响应格式不正确")
	}
	return nil
}
func (q *QR) Start(ctx context.Context) (*Code, error) {
	var out struct {
		Token    string `json:"token"`
		Image    string `json:"qrcode"`
		ExpireAt int64  `json:"expire_time"`
	}
	if err := q.getJSON(ctx, "/passport/web/get_qrcode/", q.params(), &out); err != nil {
		return nil, err
	}
	if i := strings.Index(out.Image, "base64,"); i >= 0 {
		out.Image = out.Image[i+7:]
	}
	b, err := base64.StdEncoding.DecodeString(out.Image)
	if err != nil {
		return nil, errors.New("二维码图片编码无效")
	}
	cfg, err := png.DecodeConfig(strings.NewReader(string(b)))
	if err != nil || cfg.Width < 21 || cfg.Height < 21 || cfg.Width > 4096 || cfg.Height > 4096 || out.Token == "" || out.ExpireAt <= time.Now().Unix() {
		return nil, errors.New("登录服务未返回有效二维码，请重试")
	}
	q.token, q.expires = out.Token, out.ExpireAt
	return &Code{Token: out.Token, Image: out.Image, ExpireAt: out.ExpireAt}, nil
}
func (q *QR) Poll(ctx context.Context) (*Poll, error) {
	if q.token == "" || time.Now().Unix() >= q.expires {
		return &Poll{State: "expired"}, nil
	}
	p := q.params()
	p.Set("token", q.token)
	var out struct {
		Status   json.RawMessage `json:"status"`
		Redirect string          `json:"redirect_url"`
	}
	if err := q.getJSON(ctx, "/passport/web/check_qrconnect/", p, &out); err != nil {
		return nil, err
	}
	state := strings.Trim(string(out.Status), `"`)
	switch {
	case state == "expired" || state == "5":
		return &Poll{State: "expired"}, nil
	case state == "confirmed" || state == "3" || out.Redirect != "":
		if out.Redirect != "" {
			u, err := url.Parse(out.Redirect)
			if err != nil {
				return nil, errors.New("登录跳转地址无效")
			}
			base, _ := url.Parse(q.origin)
			u = base.ResolveReference(u)
			if _, err = q.get(ctx, u.String()); err != nil {
				return nil, err
			}
		}
		u, _ := url.Parse(q.origin + "/")
		cookies := q.client.Jar.Cookies(u)
		parts := []string{}
		valid := false
		for _, c := range cookies {
			parts = append(parts, c.Name+"="+c.Value)
			if (c.Name == "sessionid" || c.Name == "sessionid_ss" || c.Name == "sid_tt") && c.Value != "" {
				valid = true
			}
		}
		if valid {
			return &Poll{State: "confirmed", Cookies: strings.Join(parts, "; ")}, nil
		}
		return &Poll{State: "scanned"}, nil
	case state == "scanned" || state == "2":
		return &Poll{State: "scanned"}, nil
	default:
		return &Poll{State: "waiting"}, nil
	}
}
