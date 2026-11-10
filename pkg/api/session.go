package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"

	"duanju-desktop/pkg/crypto"
)

var HostPool = []string{
	"api5-normal-lq.fqnovel.com",
	"api5-normal-sinfonlinea.fqnovel.com",
	"api5-normal-sinfonlinec.fqnovel.com",
	"api5-normal-sinfonlineb.fqnovel.com",
	"api5-normal-lf.fqnovel.com",
	"api5-normal-hl.fqnovel.com",
	"api.fqnovel.com",
	"api-lq.fqnovel.com",
	"api-hl.fqnovel.com",
	"api5-normal.fqnovel.com",
	"api3-normal.fqnovel.com",
	"api3-normal-lf.fqnovel.com",
	"api3-normal-lq.fqnovel.com",
	"api3-normal-hl.fqnovel.com",
	"api3-normal-sinfonlinea.fqnovel.com",
}

var DefaultHeaders = map[string]string{
	"accept":                    "application/json; charset=utf-8,application/x-protobuf",
	"x-xs-from-web":             "0",
	"x-vc-bdturing-sdk-version": "4.0.3.cn",
	"lc":                        "101",
	"sdk-version":               "2",
	"passport-sdk-version":      "5051452",
	"content-type":              "application/json; charset=utf-8",
	"x-tt-store-region":         "cn-gd",
	"x-tt-store-region-src":     "uid",
	"x-ss-dp":                   "8662",
	"accept-encoding":           "gzip, deflate, br",
	"user-agent": "com.phoenix.read/73932 (Linux; U; Android 11; zh_CN; " +
		"M2012K11AC; Build/RKQ1.200826.002; Cronet/TTNetVersion:8d40f833 " +
		"2026-03-03 QuicVersion:21ac1950 2025-11-18)",
}

// Session 是一次登录态下的请求上下文。
//
// 零值不可用，用 NewSession 构造。Session 可被多 goroutine 并发使用。
type Session struct {
	mu                   sync.RWMutex
	token                string
	uid                  string
	cookie               string
	credentialGeneration uint64
	device               DeviceContext
	params               map[string]string
	headers              map[string]string

	hostIdx  atomic.Uint32
	client   *http.Client
	registry *Registry
	signer   crypto.Signer
	logf     func(format string, args ...any)
	// scheme 是出站协议，默认 https；测试时可改成 http。
	scheme string
	// Anonymous playback devices retain server challenges across profile forks.
	retainPlaybackState bool
	// The source client identity changes when proxy configuration changes.
	anonymousConnectionSource *http.Client
}

type sessionCredentials struct {
	uid, token, cookie string
	generation         uint64
}

// DeviceContext 是设备指纹，注册与签名都要用。
type DeviceContext struct {
	DeviceID    string `json:"device_id"`
	InstallID   string `json:"install_id"`
	CDID        string `json:"cdid"`
	OpenUDID    string `json:"openudid"`
	AID         string `json:"aid"`
	Channel     string `json:"channel"`
	AppName     string `json:"app_name"`
	Version     string `json:"version_name"`
	VersionCode string `json:"version_code"`
	DeviceToken string `json:"-"`
}

func DefaultDevice() DeviceContext {
	return DeviceContext{
		DeviceID:    "",
		InstallID:   "",
		CDID:        crypto.RandomUUID(),
		OpenUDID:    crypto.RandomHex(8),
		AID:         "8662",
		Channel:     "xiaomi_8662_64",
		AppName:     "novelread",
		Version:     "7.3.9.32",
		VersionCode: "73932",
	}
}

// SessionOption 用于定制 Session。
type SessionOption func(*Session)

// WithHTTPClient 替换底层 http.Client（测试注入用）。
func WithHTTPClient(c *http.Client) SessionOption {
	return func(s *Session) { s.client = c }
}

// WithLogger 注入日志函数。
func WithLogger(f func(string, ...any)) SessionOption {
	return func(s *Session) { s.logf = f }
}

// WithScheme 覆盖出站协议（仅测试使用）。
func WithScheme(scheme string) SessionOption {
	return func(s *Session) { s.scheme = scheme }
}

// NewSession 构造一个无登录态的会话。
func NewSession(opts ...SessionOption) *Session {
	s := &Session{
		device:   DefaultDevice(),
		params:   map[string]string{},
		headers:  map[string]string{},
		registry: DefaultRegistry,
		client:   newHTTPClient(""),
		logf:     func(string, ...any) {},
		scheme:   "https",
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// newHTTPClient 构造带超时与连接复用的客户端。
func newHTTPClient(proxyURL string) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// SetProxy 设置出站代理；空字符串表示直连。
func (s *Session) SetProxy(proxyURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = newHTTPClient(proxyURL)
}

// SetDevice 覆盖设备指纹。
func (s *Session) SetDevice(d DeviceContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.device = d
}

// DeviceContext 返回当前设备指纹副本。
func (s *Session) DeviceContext() DeviceContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.device
}

// SetToken 设置登录凭证。
func (s *Session) SetToken(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = t
	s.credentialGeneration++
}

// Token 返回当前凭证。
func (s *Session) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// SetUID 设置用户 ID。
func (s *Session) SetUID(u string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uid = u
	s.credentialGeneration++
}

// UID 返回当前用户 ID。
func (s *Session) UID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uid
}

// SetCookieString 覆盖 Cookie 头。
func (s *Session) SetCookieString(c string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie = c
	s.credentialGeneration++
}

// SetAccount atomically replaces credentials and invalidates older responses.
func (s *Session) SetAccount(uid, token, cookie string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uid, s.token, s.cookie = uid, token, cookie
	s.credentialGeneration++
}

func (s *Session) credentials() sessionCredentials {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sessionCredentials{uid: s.uid, token: s.token, cookie: s.cookie, generation: s.credentialGeneration}
}

// CookieString 返回当前 Cookie 头。
func (s *Session) CookieString() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cookie
}

// Fork isolates passport responses until an authenticated account is verified.
func (s *Session) Fork() *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := NewSession()
	n.device, n.token, n.uid, n.cookie = s.device, s.token, s.uid, s.cookie
	n.credentialGeneration = s.credentialGeneration
	n.client, n.registry, n.signer, n.scheme = s.client, s.registry, s.signer, s.scheme
	n.logf = s.logf
	for k, v := range s.params {
		n.params[k] = v
	}
	for k, v := range s.headers {
		n.headers[k] = v
	}
	return n
}

func (s *Session) MergeCookieString(value string) {
	r := &http.Request{Header: make(http.Header)}
	r.Header.Set("Cookie", value)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeCookiesLocked(r.Cookies())
}

// SetSigner 注入签名器（测试时可换成空实现）。
func (s *Session) SetSigner(sg crypto.Signer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signer = sg
}

// SetParam 注册一个会拼进每次查询串的全局参数。
func (s *Session) SetParam(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params[k] = v
}

// SetHeader sets a session-local header, for example the persisted profile UA.
func (s *Session) SetHeader(k, v string) { s.mu.Lock(); defer s.mu.Unlock(); s.headers[k] = v }

// AppParams 返回全局参数的副本。
func (s *Session) AppParams() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.params))
	for k, v := range s.params {
		out[k] = v
	}
	return out
}

// pickHost 按 action 决定主机。<0 表示轮询。
func (s *Session) pickHost(a *API) string {
	if a.Host != "" {
		return a.Host
	}
	n := uint32(len(HostPool))
	if n == 0 {
		return ""
	}
	i := s.hostIdx.Add(1) - 1
	return HostPool[i%n]
}

// buildURL 组装完整 URL。
func (s *Session) buildURL(a *API, host string, query url.Values) string {
	scheme := s.scheme
	if scheme == "" {
		scheme = "https"
	}
	if a.Route == RouteUpdate {
		// 更新服务地址由配置注入，默认走占位值。
		host = updateHost
		if host == "" {
			host = "localhost:8080"
			scheme = "http"
		}
	}
	if host == "" {
		host = HostPool[0]
	}
	u := url.URL{Scheme: scheme, Host: host, Path: a.Path, RawQuery: encodeQuery(query)}
	return u.String()
}

func encodeQuery(query url.Values) string {
	return strings.ReplaceAll(strings.ReplaceAll(query.Encode(), "+", "%20"), "%2A", "*")
}

// updateHost 由配置层在启动时注入。
var updateHost string

// SetUpdateHost 注入更新服务器地址（形如 host[:port]，不含 scheme）。
func SetUpdateHost(h string) { updateHost = h }

// Call 以 GET 语义调用接口，params 会拼进查询串。
func (s *Session) Call(ctx context.Context, action string, params map[string]any) (*Result, error) {
	a, ok := s.registry.Get(action)
	if !ok {
		return nil, fmt.Errorf("api: 未注册的 action %q", action)
	}
	creds := s.credentials()
	return s.sendReadRequest(ctx, a, s.requestQuery(creds.uid, params), nil, "", true, creds)
}

// CallJSONEndpoint supports inline JSON APIs whose path contains an object ID.
// It preserves the explicit wire body without changing shared registry entries.
func (s *Session) CallJSONEndpoint(ctx context.Context, endpoint API, body any) (*Result, error) {
	if endpoint.Path == "" || endpoint.Path[0] != '/' {
		return nil, errors.New("api: 无效接口路径")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	creds := s.credentials()
	return s.sendReadRequest(ctx, &endpoint, s.requestQuery(creds.uid, nil), b, "application/json; charset=utf-8", false, creds)
}

// CallBody 是现有调用方的表单便捷入口。公共参数放在 query，
// params 只编码进表单体。预编码二进制请求可使用 CallRawWithQuery。
func (s *Session) CallBody(ctx context.Context, action string, params map[string]any) (*Result, error) {
	a, ok := s.registry.Get(action)
	if !ok {
		return nil, fmt.Errorf("api: 未注册的 action %q", action)
	}
	form := url.Values{}
	for k, v := range params {
		form.Set(k, toStr(v))
	}
	creds := s.credentials()
	return s.sendReadRequest(ctx, a, s.requestQuery(creds.uid, nil), []byte(form.Encode()), "application/x-www-form-urlencoded", true, creds)
}

// CallJSON 以 JSON 对象作为请求体调用。
//
// method 分流很关键：GET 接口把参数拼进查询串，body 必须为空；
// POST 接口才把 JSON 放进请求体。全部塞进 body 会让 GET 拿不到参数，
// 服务端返回空体（实测 feed 就是这样丢的）。
func (s *Session) CallJSON(ctx context.Context, action string, body any) (*Result, error) {
	// The model endpoint has its own plain-body player profile. Routing generic
	// callers through the same path avoids signing a gzipped content request.
	if action == "multi_video_model" {
		return s.CallPlaybackJSON(ctx, body, "")
	}
	a, ok := s.registry.Get(action)
	if !ok {
		return nil, fmt.Errorf("api: 未注册的 action %q", action)
	}
	if a.Method == "GET" {
		params, err := queryObject(body)
		if err != nil {
			return nil, fmt.Errorf("api[%s]: 查询参数格式错误: %w", action, err)
		}
		creds := s.credentials()
		return s.sendReadRequest(ctx, a, s.requestQuery(creds.uid, params), nil, "", true, creds)
	}

	// 报告 03 的 CallJSON 先序列化调用方业务对象，再单独 mergeParams /
	// encodeQuery；不能把设备和公共参数塞进业务 JSON，也不能丢失结构体输入。
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("api[%s]: 序列化请求体失败: %w", action, err)
	}
	creds := s.credentials()
	return s.sendReadRequest(ctx, a, s.requestQuery(creds.uid, nil), raw, "", true, creds)
}

func queryObject(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Session) requestQuery(uid string, params map[string]any) url.Values {
	q := url.Values{}
	for k, v := range s.mergeParams(uid, params) {
		q.Set(k, toStr(v))
	}
	return q
}

// CallRaw 用原始字节体调用，供 multipart / 二进制场景使用。
func (s *Session) CallRaw(ctx context.Context, action, contentType string, body []byte) (*Result, error) {
	a, ok := s.registry.Get(action)
	if !ok {
		return nil, fmt.Errorf("api: 未注册的 action %q", action)
	}
	q := url.Values{}
	return s.sendRequest(ctx, a, q, body, contentType, true, s.credentials())
}

// CallRawWithQuery sends an already encoded body and explicit query exactly as
// supplied. No public defaults or request gzip are added; this is intended for
// protocol envelopes such as TTEncrypt that have their own encoding and query.
func (s *Session) CallRawWithQuery(ctx context.Context, action, contentType string, query url.Values, body []byte) (*Result, error) {
	a, ok := s.registry.Get(action)
	if !ok {
		return nil, fmt.Errorf("api: 未注册的 action %q", action)
	}
	q := make(url.Values, len(query))
	for k, values := range query {
		q[k] = append([]string(nil), values...)
	}
	return s.sendRequest(ctx, a, q, body, contentType, false, s.credentials())
}

// mergeParams 把全局参数、设备参数、业务参数合成一张扁平表。
func (s *Session) mergeParams(uid string, extra map[string]any) map[string]any {
	out := make(map[string]any, 32)
	d := s.DeviceContext()
	out["device_id"] = d.DeviceID
	out["iid"] = d.InstallID
	out["cdid"] = d.CDID
	out["aid"] = d.AID
	out["channel"] = d.Channel
	out["app_name"] = d.AppName
	out["version_name"] = d.Version
	out["version_code"] = d.VersionCode
	if uid != "" {
		out["user_id"] = uid
	}
	// Explicit session settings override device defaults, and per-call values
	// override both. Empty explicit values are intentional and must be retained.
	for k, v := range s.AppParams() {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	if _, ok := out["_rticket"]; !ok {
		out["_rticket"] = time.Now().UnixMilli()
	}
	return out
}

func (s *Session) sendRequest(ctx context.Context, a *API, query url.Values, body []byte, contentType string, allowCompression bool, creds sessionCredentials) (result *Result, callErr error) {
	host := s.pickHost(a)
	target := s.buildURL(a, host, query)
	started := time.Now()
	defer func() {
		status, size, code, logID := 0, 0, 0, ""
		if result != nil {
			status, size, code, logID = result.Envelope.HTTPCode, len(result.Raw), result.Envelope.Code, result.Envelope.LogID
		}
		outcome := "ok"
		var empty *EmptyResponseError
		switch {
		case errors.Is(callErr, context.Canceled):
			outcome = "canceled"
		case errors.As(callErr, &empty):
			outcome = "empty"
		case callErr != nil:
			outcome = "failed"
		case code != 0:
			outcome = "business"
		}
		// Log response metadata only, never URLs, identities, credentials or body.
		if s.logf != nil {
			s.logf("api action=%s host=%s http=%d bytes=%d code=%d outcome=%s elapsed_ms=%d log_id=%s", a.Action, host, status, size, code, outcome, time.Since(started).Milliseconds(), logID)
		}
	}()

	payload := body
	compressed := false
	if allowCompression && len(body) > 0 && !isFormContentType(contentType) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err == nil && zw.Close() == nil {
			payload = buf.Bytes()
			compressed = true
		}
	}
	s.mu.RLock()
	signer := s.signer
	client := s.client
	s.mu.RUnlock()
	now := time.Now()
	mode := requestSignMode(a)
	if a.playerWire {
		target += fmt.Sprintf("&ts=%d", now.Unix())
	}
	if prepare, ok := signer.(interface {
		PrepareURL(string, []byte, time.Time, crypto.SignMode) string
	}); ok && a.Route != RouteBase && a.Route != RouteUpdate {
		target = prepare.PrepareURL(target, payload, now, mode)
	}

	req, err := http.NewRequestWithContext(ctx, string(a.Method), target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("api[%s]: 构造请求失败: %w", a.Action, err)
	}
	for k, v := range DefaultHeaders {
		req.Header.Set(k, v)
	}
	s.mu.RLock()
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	s.mu.RUnlock()
	if a.playerWire {
		if token := s.DeviceContext().DeviceToken; token != "" {
			req.Header.Set("X-TT-DT", token)
		}
	}
	if contentType != "" {
		req.Header.Set("content-type", contentType)
	}
	if compressed {
		req.Header.Set("content-encoding", "gzip")
	}
	if creds.cookie != "" {
		req.Header.Set("cookie", creds.cookie)
	}
	if creds.token != "" && (a.Route == RouteContent || a.Route == RoutePassport) {
		req.Header.Set("x-tt-token", creds.token)
	}

	// 风控签名：内容族与账号族需要，基础服务与更新服务不需要。
	if signer != nil && a.Route != RouteUpdate && a.Route != RouteBase {
		input := crypto.SignInput{
			URL: target, Mode: mode,
			Action: a.Action,
			Query:  req.URL.RawQuery,
			// The signer must hash the exact transmitted body, including any
			// gzip transform, rather than a different logical JSON representation.
			Body:   payload,
			Device: s.DeviceContext().fingerprint(),
			Token:  creds.token,
			Now:    now,
		}
		if checked, ok := signer.(interface {
			SignChecked(*http.Request, crypto.SignInput) error
		}); ok {
			if err := checked.SignChecked(req, input); err != nil {
				return nil, fmt.Errorf("api[%s]: %w", a.Action, err)
			}
		} else {
			signer.Sign(req, input)
		}
	}

	// Set the request ticket after signing because the signer clears old signature headers.
	if a.Route == RouteContent || a.Route == RoutePassport {
		req.Header.Set("x-ss-req-ticket", fmt.Sprint(now.UnixMilli()))
		if !a.playerWire {
			req.Header.Set("x-reading-request", fmt.Sprintf("%d-%d", now.UnixMilli(), 100000000+rand.IntN(900000000)))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api[%s]: 读取响应失败: %w", a.Action, err)
	}
	defer resp.Body.Close()
	raw, err := decodeBody(resp)
	if err != nil {
		s.applyResponseCredentials(creds.generation, resp.Cookies(), "")
		return nil, fmt.Errorf("api[%s]: 解压响应失败: %w", a.Action, err)
	}
	token := resp.Header.Get("X-TT-Token")
	s.applyResponseCredentials(creds.generation, resp.Cookies(), token)
	res := &Result{Action: a.Action, Raw: raw, ResponseToken: token}
	res.Envelope.HTTPCode = resp.StatusCode
	if resp.StatusCode == http.StatusOK && len(bytes.TrimSpace(raw)) == 0 {
		res.Envelope.LogID = resp.Header.Get("X-Tt-Logid")
		return res, &EmptyResponseError{Action: a.Action}
	}
	if err := json.Unmarshal(raw, &res.Envelope); err != nil {
		if resp.StatusCode >= http.StatusInternalServerError {
			return res, &BizError{Action: a.Action, HTTPCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		// 非 JSON 响应：保留原文，交给调用方判断。
		return res, fmt.Errorf("api[%s]: 解析响应失败: %w", a.Action, err)
	}
	if res.Envelope.LogID == "" {
		res.Envelope.LogID = resp.Header.Get("X-Tt-Logid")
	}
	if resp.StatusCode != http.StatusOK {
		return res, &BizError{
			Action:   a.Action,
			Code:     res.Envelope.Code,
			HTTPCode: resp.StatusCode,
			Message:  res.Envelope.Message,
			LogID:    res.Envelope.LogID,
		}
	}
	return res, nil
}

// EmptyResponseError distinguishes an empty successful HTTP reply from invalid
// JSON and business denials. Callers may recover a read-only device request.
type EmptyResponseError struct{ Action string }

func (e *EmptyResponseError) Error() string {
	return fmt.Sprintf("api[%s]: 服务端未返回内容，请重试", e.Action)
}

func (s *Session) applyResponseCredentials(generation uint64, cookies []*http.Cookie, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.credentialGeneration {
		return
	}
	s.mergeCookiesLocked(cookies)
	if token != "" {
		s.token = token
	}
}

// mergeCookiesLocked applies name-based replacement while retaining unrelated
// cookies. Attribute handling stays with net/http; session storage is a Cookie
// request-header value, not a concatenation of Set-Cookie response attributes.
func (s *Session) mergeCookiesLocked(incoming []*http.Cookie) {
	if len(incoming) == 0 {
		return
	}
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("Cookie", s.cookie)
	list := request.Cookies()
	indices := make(map[string]int, len(list)+len(incoming))
	merged := make([]*http.Cookie, 0, len(list)+len(incoming))
	for _, c := range append(list, incoming...) {
		if i, ok := indices[c.Name]; ok {
			merged[i] = c
		} else {
			indices[c.Name] = len(merged)
			merged = append(merged, c)
		}
	}
	parts := make([]string, 0, len(merged))
	for _, c := range merged {
		pair := (&http.Cookie{Name: c.Name, Value: c.Value, Quoted: c.Quoted}).String()
		if pair != "" {
			parts = append(parts, pair)
		}
	}
	s.cookie = strings.Join(parts, "; ")
}

// decodeBody 按 Content-Encoding 解压响应体。
func decodeBody(resp *http.Response) ([]byte, error) {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	var r io.Reader = resp.Body
	switch enc {
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		r = zr
	case "deflate":
		r = newFlateReader(resp.Body)
	case "br":
		r = brotli.NewReader(resp.Body)
	}
	return io.ReadAll(io.LimitReader(r, 64<<20))
}

// isFormContentType 判断是否为表单类型（表单体不做 gzip）。
func isFormContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "application/x-www-form-urlencoded") ||
		strings.Contains(strings.ToLower(ct), "multipart/form-data")
}

// ErrNoData 表示响应缺少 data 字段。
var ErrNoData = errors.New("api: 响应没有 data 字段")
