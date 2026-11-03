package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"duanju-desktop/pkg/crypto"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSignedPlaybackClonesReferenceProfileAndKeepsAccountState(t *testing.T) {
	calls := 0
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("device_id") != "registered-device" || q.Get("iid") != "registered-install" {
			t.Error("playback replaced registered identity")
		}
		for k, v := range map[string]string{"version_name": "7.1.3.32", "version_code": "71332", "pv_player": "71332", "device_type": "25053RT47C", "device_brand": "Redmi", "os_version": "16", "os_api": "36", "dpi": "520", "resolution": "1280*2772", "channel": "update_64"} {
			if q.Get(k) != v {
				t.Errorf("expected playback parameter %s=%q want %q", k, q.Get(k), v)
			}
		}
		for _, k := range []string{"user_id", "token", "sessionid", "ts", "cronet_version", "from"} {
			if q.Has(k) {
				t.Errorf("unexpected public query field: %s", k)
			}
		}
		if r.Header.Get("Cookie") != "sessionid=account-cookie" || r.Header.Get("X-TT-Token") != "account-token" {
			t.Error("playback clone lost the authorized account session")
		}
		if r.Header.Get("User-Agent") != playbackUserAgent || r.Header.Get("X-TT-Store-Region") != "cn" {
			t.Error("expected playback headers missing")
		}
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Error("signed playback must use expected request gzip")
		}
		raw, _ := io.ReadAll(r.Body)
		sum := md5.Sum(raw)
		if r.Header.Get("X-SS-Stub") != strings.ToUpper(hex.EncodeToString(sum[:])) {
			t.Error("signature does not cover transmitted gzip bytes")
		}
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		b, _ := io.ReadAll(gz)
		if string(b) != `{"episode":"one"}` {
			t.Error("playback business body changed")
		}
		for _, name := range crypto.HeaderNames {
			if r.Header.Get(name) == "" {
				t.Errorf("missing full signature %s", name)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "playback-response-cookie"})
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	signer := crypto.NewDefaultSigner()
	s.SetSigner(signer)
	s.SetDevice(DeviceContext{DeviceID: "registered-device", InstallID: "registered-install", Version: "7.3.9.32", Channel: "xiaomi_8662_64"})
	s.SetParam("device_type", "Xiaomi 14")
	s.SetParam("version_code", "73932")
	s.SetParam("cronet_version", "source-cronet")
	s.SetParam("from", "desktop")
	s.SetCookieString("sessionid=account-cookie")
	s.SetToken("account-token")
	s.SetUID("account-user")
	for i := 0; i < 2; i++ {
		if _, err := s.CallPlaybackJSON(context.Background(), map[string]any{"episode": "one"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("unexpected retries in transport")
	}
	if s.DeviceContext().Version != "7.3.9.32" || s.DeviceContext().Channel != "xiaomi_8662_64" || s.Token() != "account-token" || s.UID() != "account-user" || s.CookieString() != "sessionid=account-cookie" || s.AppParams()["device_type"] != "Xiaomi 14" || signer.Profile.VersionName != "7.3.9.32" {
		t.Error("playback changed the main device, signing profile or account state")
	}
}

func TestAnonymousPlaybackKeepsChallengesAcrossWireProfileForks(t *testing.T) {
	calls := 0
	main, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 && (r.Header.Get("X-TT-Token") != "anonymous-token" || !strings.Contains(r.Header.Get("Cookie"), "ttreq=updated-challenge")) {
			t.Error("second playback request lost anonymous response state")
		}
		if calls == 1 {
			http.SetCookie(w, &http.Cookie{Name: "ttreq", Value: "updated-challenge"})
			w.Header().Set("X-TT-Token", "anonymous-token")
			return
		}
		io.WriteString(w, `{"code":0,"data":{}}`)
	})
	main.SetToken("account-token")
	main.SetCookieString("sessionid=account-cookie")
	spare := main.NewAnonymousSession()
	spare.SetCookieString("ttreq=initial")
	spare.CallPlaybackJSON(context.Background(), map[string]any{}, "") // Empty body still carries a challenge.
	if _, err := spare.CallPlaybackJSON(context.Background(), map[string]any{}, ""); err != nil {
		t.Fatal(err)
	}
	if main.Token() != "account-token" || main.CookieString() != "sessionid=account-cookie" {
		t.Fatal("anonymous challenge mutated account")
	}
}

func TestPlaybackEndpointPortAndBodyParametersStaySeparate(t *testing.T) {
	for _, host := range []string{"api.example", "api.example:443", "api.example:8443"} {
		calls := 0
		main := NewSession(WithHTTPClient(&http.Client{Transport: playbackPortTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "POST" || r.URL.Scheme != "https" || r.URL.Host != host || r.URL.Path != "/novel/player/multi_video_model/v1/" {
				t.Fatal("playback endpoint/method/port changed")
			}
			for _, key := range []string{"port", "host", "mixed_video_id_map", "biz_param", "from", "user_id"} {
				if r.URL.Query().Has(key) {
					t.Fatalf("body or desktop field leaked into signed query: %s", key)
				}
			}
			if r.URL.Query().Get("device_id") != "device" || r.URL.Query().Get("iid") != "install" {
				t.Fatal("registered identity not used")
			}
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(gz)
			gz.Close()
			if string(raw) != `{"mixed_video_id_map":{"1004":["episode"]}}` {
				t.Fatal("JSON model parameters missing or changed")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`))}, nil
		})}))
		main.SetDevice(DeviceContext{DeviceID: "device", InstallID: "install"})
		_, err := main.CallPlaybackJSON(context.Background(), map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"episode"}}}, host)
		if err != nil || calls != 1 {
			t.Fatalf("endpoint request failed: %v", err)
		}
	}
}

type playbackPortTransport func(*http.Request) (*http.Response, error)

func (f playbackPortTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
