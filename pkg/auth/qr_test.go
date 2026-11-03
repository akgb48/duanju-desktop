package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func pngBase64(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 100, 100))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestQRProtocolStatesAndCookieRedirect(t *testing.T) {
	img := pngBase64(t)
	state := "1"
	redirect := ""
	authenticated := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/finish" {
			if authenticated {
				http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "test-session", Path: "/"})
			}
			fmt.Fprint(w, "ok")
			return
		}
		v := r.URL.Query()
		for k, want := range map[string]string{"aid": "513755", "account_sdk_source": "web", "p_js_v": "2.4.13", "p_zt": "3.3.5"} {
			if v.Get(k) != want {
				t.Errorf("%s=%q", k, v.Get(k))
			}
		}
		if v.Get("device_id") != "" || r.Header.Get("X-Gorgon") != "" {
			t.Error("Android parameters leaked into web QR")
		}
		if r.Header.Get("Referer") == "" || !strings.Contains(r.UserAgent(), "Windows") {
			t.Error("browser headers missing")
		}
		switch r.URL.Path {
		case "/passport/web/get_qrcode/":
			http.SetCookie(w, &http.Cookie{Name: "qr-attempt", Value: "test", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "success", "data": map[string]any{"error_code": 0, "token": "test-token", "qrcode": "data:image/png;base64," + img, "expire_time": time.Now().Unix() + 120}})
		case "/passport/web/check_qrconnect/":
			if v.Get("token") != "test-token" {
				t.Error("poll lost token")
			}
			if _, e := r.Cookie("qr-attempt"); e != nil {
				t.Error("poll lost cookie jar")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "success", "data": map[string]any{"status": json.RawMessage(state), "redirect_url": redirect}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer s.Close()
	q := newQR(s.URL)
	c, err := q.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Image != img {
		t.Fatal("data URI prefix not removed")
	}
	for _, test := range []struct {
		raw, want string
		auth      bool
		redirect  string
	}{{`1`, "waiting", false, ""}, {`"scanned"`, "scanned", false, ""}, {`2`, "scanned", false, ""}, {`3`, "scanned", false, "/finish"}, {`"confirmed"`, "confirmed", true, "/finish"}, {`5`, "expired", false, ""}} {
		state, authenticated, redirect = test.raw, test.auth, test.redirect
		p, err := q.Poll(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if p.State != test.want {
			t.Fatalf("%s => %s want %s", state, p.State, test.want)
		}
		if p.State == "confirmed" && !strings.Contains(p.Cookies, "sessionid=test-session") {
			t.Fatal("confirmed without cookie")
		}
	}
	q.expires = time.Now().Unix() - 1
	p, err := q.Poll(context.Background())
	if err != nil || p.State != "expired" {
		t.Fatal("local expiry failed")
	}
}
func TestQRRejectsBrokenImagesAndPassportErrors(t *testing.T) {
	for _, body := range []string{`{"message":"error","data":{"error_code":1105}}`, `{"data":{"token":"x","qrcode":"not-an-image","expire_time":9999999999}}`, `{"message":"success","data":{}}`, `not json`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		q := newQR(s.URL)
		if _, err := q.Start(context.Background()); err == nil {
			t.Errorf("accepted invalid QR response")
		}
		s.Close()
	}
	q := NewQR()
	u, _ := url.Parse("https://example.com/steal")
	if q.allowURL(u) == nil {
		t.Fatal("foreign redirect allowed")
	}
	u, _ = url.Parse("http://hongguoduanju.com/")
	if q.allowURL(u) == nil {
		t.Fatal("HTTP downgrade allowed")
	}
}
func TestXorEncoding(t *testing.T) {
	if XorEncode("24") != "3731" || XorEncode("+8613800000000") != "2e3d3334363d3535353535353535" {
		t.Fatal("XOR fixture mismatch")
	}
}
