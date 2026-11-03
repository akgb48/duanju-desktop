package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/auth"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/model"
	"duanju-desktop/pkg/store"
)

// 本文件是账号与登录绑定方法。

// LoginState 是登录态快照，前端首屏用它决定跳哪个页。
type LoginState struct {
	LoggedIn bool                    `json:"loggedIn"`
	User     *model.User             `json:"user"`
	Device   DeviceRegistrationState `json:"device"`
}

// GetLoginState 返回当前登录态。
func (a *App) loginState() LoginState {
	svc, err := a.service()
	if err != nil {
		return LoginState{}
	}
	a.mu.RLock()
	device := a.deviceState
	a.mu.RUnlock()
	return LoginState{LoggedIn: svc.IsLoggedIn(), User: svc.CurrentUser(), Device: device}
}

// QRLoginStart contains only display data; authentication cookies stay in Go.
type QRLoginStart struct {
	Token  string `json:"token"`
	QRURL  string `json:"qrUrl"`
	Expire int64  `json:"expire"`
}
type QRLoginStatus struct {
	Status  int         `json:"status"`
	Message string      `json:"message"`
	User    *model.User `json:"user"`
}

func (a *App) startQRLogin() (*QRLoginStart, error) {
	a.authMu.Lock()
	a.authGeneration++
	generation := a.authGeneration
	a.qr = nil
	a.authMu.Unlock()
	ctx, cancel := a.callCtx()
	defer cancel()
	q := auth.NewQR()
	code, err := q.Start(ctx)
	if err != nil {
		return nil, err
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if generation != a.authGeneration {
		return nil, errors.New("扫码登录已取消")
	}
	a.qr = q
	return &QRLoginStart{Token: code.Token, QRURL: code.Image, Expire: code.ExpireAt}, nil
}
func (a *App) pollQRLogin() (*QRLoginStatus, error) {
	a.authMu.Lock()
	q, generation := a.qr, a.authGeneration
	a.authMu.Unlock()
	if q == nil {
		return &QRLoginStatus{Status: 3, Message: "二维码已取消，请刷新"}, nil
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	poll, err := q.Poll(ctx)
	if err != nil {
		return nil, err
	}
	st := &QRLoginStatus{Status: map[string]int{"waiting": 0, "scanned": 1, "confirmed": 2, "expired": 3}[poll.State]}
	if poll.State != "confirmed" {
		return st, nil
	}
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	candidate := a.loginCandidate(sess)
	candidate.MergeCookieString(poll.Cookies)
	u, err := hgapi.New(candidate).UserInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("扫码已确认，读取账号资料失败，请重试: %w", err)
	}
	if err = a.commitAccount(candidate, u, generation); err != nil {
		return nil, err
	}
	st.User = u
	return st, nil
}
func normalizeMobile(phone string) (string, error) {
	phone = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(phone))
	if !strings.HasPrefix(phone, "+") {
		phone = "+86" + phone
	}
	digits := strings.TrimPrefix(phone, "+")
	if len(digits) < 7 || len(digits) > 15 {
		return "", errors.New("请填写有效手机号（含国家区号）")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", errors.New("手机号只能包含数字和国家区号")
		}
	}
	if strings.HasPrefix(phone, "+86") && (len(digits) != 13 || digits[2] != '1') {
		return "", errors.New("请填写有效的 11 位大陆手机号")
	}
	return phone, nil
}
func passportParams(mobile string) map[string]any {
	return map[string]any{"account_sdk_source": "app", "passport_support_flow": "captcha,verify", "mix_mode": "1", "mobile": auth.XorEncode(mobile)}
}

// passportData checks the nested passport error code, even for HTTP 200.
func passportData(res *api.Result) (map[string]json.RawMessage, error) {
	var root struct {
		Message   string `json:"message"`
		ErrorCode int    `json:"error_code"`
	}
	_ = json.Unmarshal(res.Raw, &root)
	var data map[string]json.RawMessage
	if err := res.Decode(&data); err != nil {
		return nil, err
	}
	code := jsonText(data["error_code"])
	if root.ErrorCode != 0 || (code != "" && code != "0") || root.Message == "error" {
		return nil, fmt.Errorf("登录服务拒绝请求（%s），请检查验证码或改用扫码登录", code)
	}
	return data, nil
}
func jsonText(b json.RawMessage) string {
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	if string(b) == "null" {
		return ""
	}
	return string(b)
}
func (a *App) sendSMS(phone string) error {
	mobile, err := normalizeMobile(phone)
	if err != nil {
		return err
	}
	sess, err := a.session()
	if err != nil {
		return err
	}
	a.authMu.Lock()
	generation := a.authGeneration
	a.authMu.Unlock()
	ctx, cancel := a.callCtx()
	defer cancel()
	params := passportParams(mobile)
	params["auto_read"] = "0"
	params["unbind_exist"] = "34"
	params["type"] = auth.XorEncode("24")
	candidate := a.loginCandidate(sess)
	res, err := candidate.CallBody(ctx, "send_code", params)
	if err != nil {
		return err
	}
	data, err := passportData(res)
	if err != nil {
		return err
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if generation != a.authGeneration {
		return errors.New("登录操作已取消")
	}
	a.smsPhone = mobile
	a.smsTicket = jsonText(data["mobile_ticket"])
	a.smsSession = candidate
	return nil
}
func (a *App) loginBySMS(phone, code string) (*model.User, error) {
	mobile, err := normalizeMobile(phone)
	if err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	if len(code) < 4 || len(code) > 8 {
		return nil, errors.New("请填写 4 至 8 位短信验证码")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return nil, errors.New("短信验证码只能包含数字")
		}
	}
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	a.authMu.Lock()
	generation := a.authGeneration
	ticket := ""
	candidate := a.loginCandidate(sess)
	if a.smsPhone == mobile {
		ticket = a.smsTicket
		if a.smsSession != nil {
			candidate = a.smsSession.Fork()
		}
	}
	a.authMu.Unlock()
	ctx, cancel := a.callCtx()
	defer cancel()
	params := passportParams(mobile)
	params["code"] = auth.XorEncode(code)
	if ticket != "" {
		params["passport_mfa_retry_tag"] = "1"
		params["sms_code_key"] = auth.XorEncode(ticket)
	}
	res, err := candidate.CallBody(ctx, "sms_login", params)
	if err != nil {
		return nil, err
	}
	data, err := passportData(res)
	if err != nil {
		return nil, err
	}
	if sid := jsonText(data["sessionid"]); sid != "" {
		candidate.MergeCookieString("sessionid=" + sid)
	}
	candidate.SetToken(jsonText(data["token"]))
	if !hasLoginCookie(candidate.CookieString()) && candidate.Token() == "" {
		return nil, errors.New("登录响应缺少会话凭证，请重试或扫码登录")
	}
	u, err := hgapi.New(candidate).UserInfo(ctx)
	if err != nil {
		uid := jsonText(data["user_id_str"])
		if uid == "" {
			uid = jsonText(data["user_id"])
		}
		if uid == "" {
			return nil, errors.New("登录已返回凭证，但尚未取得账号资料，请重试")
		}
		u = &model.User{UserID: uid, Nickname: jsonText(data["screen_name"]), Avatar: jsonText(data["avatar_url"])}
	}
	if err = a.commitAccount(candidate, u, generation); err != nil {
		return nil, err
	}
	return u, nil
}
func hasLoginCookie(value string) bool {
	r := &http.Request{Header: make(http.Header)}
	r.Header.Set("Cookie", value)
	for _, c := range r.Cookies() {
		if (c.Name == "sessionid" || c.Name == "sessionid_ss" || c.Name == "sid_tt") && c.Value != "" {
			return true
		}
	}
	return false
}

func (a *App) loginCandidate(sess *api.Session) *api.Session {
	c := sess.Fork()
	c.SetUID("")
	c.SetToken("")
	if d, err := a.readDeviceRecord(); err == nil {
		c.SetCookieString(d.Cookies)
		return c
	}
	r := &http.Request{Header: make(http.Header)}
	r.Header.Set("Cookie", c.CookieString())
	var deviceCookies []string
	for _, cookie := range r.Cookies() {
		switch cookie.Name {
		case "sessionid", "sessionid_ss", "sid_tt", "sid_guard", "uid_tt", "uid_tt_ss", "sid_ucp_v1", "ssid_ucp_v1", "multi_sids":
			continue
		}
		deviceCookies = append(deviceCookies, cookie.Name+"="+cookie.Value)
	}
	c.SetCookieString(strings.Join(deviceCookies, "; "))
	return c
}
func (a *App) commitAccount(candidate *api.Session, u *model.User, generation uint64) error {
	if u == nil || u.UserID == "" {
		return errors.New("登录响应缺少账号 ID")
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if generation != a.authGeneration {
		return errors.New("登录操作已取消，请重新登录")
	}
	if a.st == nil {
		return errors.New("账号存储尚未就绪")
	}
	d := candidate.DeviceContext()
	if err := a.st.SaveAccount(context.Background(), &store.Account{UserID: u.UserID, Token: candidate.Token(), Cookie: candidate.CookieString(), DeviceID: d.DeviceID, InstallID: d.InstallID, CDID: d.CDID, OpenUDID: d.OpenUDID, Nickname: u.Nickname, Avatar: u.Avatar}); err != nil {
		return err
	}
	if d, err := a.readDeviceRecord(); err == nil {
		candidate.MergeCookieString(d.Cookies)
	}
	a.sess.SetCookieString(candidate.CookieString())
	a.sess.SetToken(candidate.Token())
	a.sess.SetUID(u.UserID)
	a.svc.SetLoggedIn(true, u)
	a.smsPhone, a.smsTicket = "", ""
	a.smsSession = nil
	return nil
}
func (a *App) restoreAccount() error {
	if a.st == nil {
		return nil
	}
	record, err := a.st.LoadAccount(context.Background())
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.UserID == "" || (!hasLoginCookie(record.Cookie) && record.Token == "") {
		return a.st.ClearAccount(context.Background())
	}
	a.sess.MergeCookieString(record.Cookie)
	a.sess.SetToken(record.Token)
	a.sess.SetUID(record.UserID)
	a.svc.SetLoggedIn(true, &model.User{UserID: record.UserID, Nickname: record.Nickname, Avatar: record.Avatar})
	return nil
}

// Logout invalidates pending replies and deletes the stored account.
func (a *App) Logout() error {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	a.authGeneration++
	a.qr = nil
	a.smsPhone, a.smsTicket = "", ""
	a.smsSession = nil
	if a.st != nil {
		if err := a.st.ClearAccount(context.Background()); err != nil {
			return err
		}
	}
	sess, err := a.session()
	if err != nil {
		return err
	}
	sess.SetToken("")
	sess.SetUID("")
	sess.SetCookieString("")
	if d, e := a.readDeviceRecord(); e == nil {
		sess.MergeCookieString(d.Cookies)
	}
	if svc, e := a.service(); e == nil {
		svc.SetLoggedIn(false, nil)
	}
	return nil
}

// RefreshUser 主动刷新用户资料。
func (a *App) refreshUser() (*model.User, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.RefreshUser(ctx)
}

// GetUserProfile 返回当前用户资料。
func (a *App) userProfile() (*model.User, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	if u := svc.CurrentUser(); u != nil {
		return u, nil
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.RefreshUser(ctx)
}

// SetDeviceFingerprint 允许设置页覆盖设备指纹（调试用）。
func (a *App) setDeviceFingerprint(deviceID, installID, cdid string) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	d := sess.DeviceContext()
	if deviceID != "" {
		d.DeviceID = deviceID
	}
	if installID != "" {
		d.InstallID = installID
	}
	if cdid != "" {
		d.CDID = cdid
	}
	sess.SetDevice(d)
	return nil
}

// GetDeviceFingerprint 读取当前设备指纹。
func (a *App) deviceFingerprint() (any, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	return sess.DeviceContext(), nil
}

// EnsureLoggedIn 在需要登录的操作前给出统一错误。
func (a *App) ensureLoggedIn() error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	if !svc.IsLoggedIn() {
		return fmt.Errorf("请先登录")
	}
	return nil
}

// Login 用手机号 + 验证码登录。
func (a *App) Login(phone, code string) (*model.User, error) {
	return a.loginBySMS(phone, code)
}

// Status 返回登录态快照。
func (a *App) Status() (LoginState, error) {
	return a.loginState(), nil
}

// SendCode 发送短信验证码。
func (a *App) SendCode(phone string) error {
	return a.sendSMS(phone)
}

// QRLoginStart 申请扫码会话。region 为空时用默认区域。
func (a *App) QRLoginStart(region string) (*QRLoginStart, error) {
	return a.startQRLogin()
}

// QRLoginPoll 查询一次扫码状态。
func (a *App) QRLoginPoll() (*QRLoginStatus, error) {
	return a.pollQRLogin()
}

// QRLoginCancel 放弃当前扫码会话。
func (a *App) QRLoginCancel() error {
	a.authMu.Lock()
	a.authGeneration++
	a.qr = nil
	a.authMu.Unlock()
	return nil
}

// LoginRegion 是一个可登录区域。
type LoginRegion struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// LoginRegions 返回可用登录区域列表。
func (a *App) LoginRegions() ([]LoginRegion, error) {
	return []LoginRegion{{Code: "cn", Name: "中国大陆"}}, nil
}

// RetryDeviceRegister 重新执行设备注册。
func (a *App) RetryDeviceRegister() error {
	return a.ensureDeviceRegistration(true)
}
