package app

import (
	"context"
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/auth"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/model"
	"duanju-desktop/pkg/service"
	"duanju-desktop/pkg/store"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type accountTransport func(*http.Request) (*http.Response, error)

func (f accountTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func accountTestApp(t *testing.T, f accountTransport) *App {
	t.Helper()
	a := New()
	a.ctx = context.Background()
	a.dataDir = t.TempDir()
	a.sess = api.NewSession(api.WithHTTPClient(&http.Client{Transport: f}))
	a.sess.SetCookieString("odin_tt=device-cookie")
	a.svc = service.New(hgapi.New(a.sess))
	var err error
	a.st, err = store.Open(filepath.Join(a.dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.st.Close() })
	return a
}
func accountResponse(body string, cookies ...string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	for _, c := range cookies {
		h.Add("Set-Cookie", c)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}
func TestSMSFormsAndPersistentAccount(t *testing.T) {
	requests := 0
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Path == "/passport/account/info/v2/" {
			if !strings.Contains(r.Header.Get("Cookie"), "sessionid=test-session") {
				t.Error("account lookup missing credentials")
			}
			return accountResponse(`{"message":"success","data":{"user_id_str":"9007199254740993123","screen_name":"测试账号","avatar_url":"https://example.com/avatar.jpg"}}`), nil
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Content-Encoding") != "" {
			t.Error("incorrect passport wire format")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		v := r.PostForm
		if v.Get("mobile") != auth.XorEncode("+8613800000000") || v.Get("mix_mode") != "1" || v.Get("passport_support_flow") != "captcha,verify" {
			t.Error("passport fields incorrect")
		}
		if v.Get("mobile") == "13800000000" || v.Get("code") == "123456" {
			t.Error("unencrypted credentials")
		}
		if strings.Contains(r.URL.RawQuery, "mobile=") {
			t.Error("phone leaked into query")
		}
		switch r.URL.Path {
		case "/passport/mobile/send_code/v1/":
			if v.Get("type") != auth.XorEncode("24") || v.Get("auto_read") != "0" || v.Get("unbind_exist") != "34" {
				t.Error("send-code fields incorrect")
			}
			return accountResponse(`{"message":"success","data":{"error_code":0,"mobile_ticket":"test-ticket"}}`, "sms_attempt=test-attempt; Path=/"), nil
		case "/passport/mobile/sms_login/":
			if v.Get("code") != auth.XorEncode("123456") || v.Get("sms_code_key") != auth.XorEncode("test-ticket") {
				t.Error("code/ticket not encoded")
			}
			if !strings.Contains(r.Header.Get("Cookie"), "sms_attempt=test-attempt") {
				t.Error("send-code cookies lost")
			}
			return accountResponse(`{"message":"success","data":{"error_code":0,"user_id_str":"9007199254740993123"}}`, "sessionid=test-session; Path=/"), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	if err := a.SendCode("138 0000-0000"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.sess.CookieString(), "sms_attempt") {
		t.Fatal("unverified cookies committed")
	}
	u, err := a.Login("+8613800000000", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if u.UserID != "9007199254740993123" || u.Nickname != "测试账号" || !a.svc.IsLoggedIn() {
		t.Fatal("account not committed")
	}
	if !strings.Contains(a.sess.CookieString(), "odin_tt=device-cookie") {
		t.Fatal("device cookie lost")
	}
	if requests != 3 {
		t.Fatalf("requests=%d", requests)
	}
	restored := New()
	restored.sess = api.NewSession()
	restored.svc = service.New(hgapi.New(restored.sess))
	restored.st = a.st
	if err = restored.restoreAccount(); err != nil {
		t.Fatal(err)
	}
	if !restored.svc.IsLoggedIn() || restored.svc.CurrentUser().UserID != u.UserID || restored.sess.UID() != u.UserID {
		t.Fatal("account not restored")
	}
	if err = a.Logout(); err != nil {
		t.Fatal(err)
	}
	if a.svc.IsLoggedIn() || hasLoginCookie(a.sess.CookieString()) {
		t.Fatal("logout retained credentials")
	}
	if _, err = a.st.LoadAccount(context.Background()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("logout retained disk account")
	}
}
func TestSMSRejectsInvalidInputAndFalseSuccess(t *testing.T) {
	calls := 0
	body := `{"message":"error","data":{"error_code":1105,"description":"bad code"}}`
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return accountResponse(body, "sessionid=unverified; Path=/"), nil
	})
	for _, phone := range []string{"", "123", "abc", "+861280000000", "+86<script>"} {
		if err := a.SendCode(phone); err == nil {
			t.Fatalf("accepted invalid phone %q", phone)
		}
	}
	if _, err := a.Login("13800000000", "abc"); err == nil {
		t.Fatal("accepted invalid code")
	}
	if calls != 0 {
		t.Fatal("invalid input reached network")
	}
	if err := a.SendCode("13800000000"); err == nil {
		t.Fatal("passport error treated as SMS sent")
	}
	if _, err := a.Login("13800000000", "123456"); err == nil {
		t.Fatal("passport error logged in")
	}
	body = `{"message":"success","data":{"error_code":0}}`
	// A successful HTTP response and cookie do not establish an account identity.
	if _, err := a.Login("13800000000", "123456"); err == nil {
		t.Fatal("empty identity treated as login success")
	}
	if a.svc.IsLoggedIn() || hasLoginCookie(a.sess.CookieString()) {
		t.Fatal("failed login polluted global session")
	}
}
func TestCanceledLoginCannotCommit(t *testing.T) {
	a := accountTestApp(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unexpected request") })
	c := a.sess.Fork()
	c.MergeCookieString("sessionid=test")
	if err := a.QRLoginCancel(); err != nil {
		t.Fatal(err)
	}
	if err := a.commitAccount(c, &model.User{UserID: "test-user"}, 0); err == nil {
		t.Fatal("canceled attempt committed")
	}
	if a.svc.IsLoggedIn() || hasLoginCookie(a.sess.CookieString()) {
		t.Fatal("canceled attempt changed session")
	}
	if _, err := a.st.LoadAccount(context.Background()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("canceled attempt persisted")
	}
}

func TestPreviousAccountCannotMakeAnEmptySMSResponseSuccessful(t *testing.T) {
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		return accountResponse(`{"message":"success","data":{"error_code":0}}`), nil
	})
	a.sess.MergeCookieString("sessionid=previous-session")
	a.sess.SetToken("previous-token")
	a.sess.SetUID("previous-user")
	a.svc.SetLoggedIn(true, &model.User{UserID: "previous-user"})
	if _, err := a.Login("13800000000", "123456"); err == nil {
		t.Fatal("old account credentials made empty login successful")
	}
	if a.svc.CurrentUser().UserID != "previous-user" {
		t.Fatal("failed attempt replaced the previous account")
	}
}

type testQRSession struct{ state string }

func (q testQRSession) Poll(context.Context) (*auth.Poll, error) {
	p := &auth.Poll{State: q.state}
	if q.state == "confirmed" {
		p.Cookies = "sessionid=test-qr-session"
	}
	return p, nil
}
func TestQRConfirmationVerifiesAccountBeforeCommit(t *testing.T) {
	lookupOK := false
	cancelReply := false
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.Header.Get("Cookie"), "sessionid=test-qr-session") {
			t.Error("QR credentials not used for lookup")
		}
		if !lookupOK {
			return accountResponse(`{"message":"error","data":{"error_code":8}}`), nil
		}
		return accountResponse(`{"message":"success","data":{"user_id_str":"qr-user","name":"扫码账号"}}`), nil
	})
	for _, state := range []string{"waiting", "scanned", "expired"} {
		a.qr = testQRSession{state}
		if _, err := a.QRLoginPoll(); err != nil {
			t.Fatal(err)
		}
		if a.svc.IsLoggedIn() {
			t.Fatal("unconfirmed QR logged in")
		}
	}
	a.qr = testQRSession{"confirmed"}
	if _, err := a.QRLoginPoll(); err == nil {
		t.Fatal("failed account lookup treated as login")
	}
	if hasLoginCookie(a.sess.CookieString()) {
		t.Fatal("failed QR leaked cookies")
	}
	lookupOK = true
	st, err := a.QRLoginPoll()
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != 2 || st.User == nil || st.User.Nickname != "扫码账号" || !a.svc.IsLoggedIn() {
		t.Fatal("confirmed QR not committed")
	}
	if err = a.Logout(); err != nil {
		t.Fatal(err)
	}
	a.qr = testQRSession{"confirmed"}
	// Cancel while an account-info response is in flight.
	a.sess = api.NewSession(api.WithHTTPClient(&http.Client{Transport: accountTransport(func(r *http.Request) (*http.Response, error) {
		cancelReply = true
		_ = a.QRLoginCancel()
		return accountResponse(`{"data":{"user_id_str":"late-user"}}`), nil
	})}))
	if _, err = a.QRLoginPoll(); err == nil || !cancelReply {
		t.Fatal("in-flight canceled QR was accepted")
	}
	if a.svc.IsLoggedIn() {
		t.Fatal("canceled QR restored login")
	}
}
