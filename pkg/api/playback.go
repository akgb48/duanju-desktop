package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"duanju-desktop/pkg/crypto"
)

const playbackUserAgent = "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"

func (s *Session) playbackSession(installID string) *Session {
	p := s.Fork()
	d := p.DeviceContext()
	if installID != "" {
		d.InstallID = installID
	}
	d.Version, d.VersionCode, d.Channel = "7.1.3.32", "71332", "update_64"
	p.SetDevice(d)
	for key, value := range map[string]string{
		"version_name": d.Version, "version_code": d.VersionCode, "manifest_version_code": d.VersionCode, "update_version_code": d.VersionCode,
		"channel": d.Channel, "device_brand": "Redmi", "device_type": "25053RT47C", "os_version": "16", "os_api": "36", "resolution": "1280*2772", "dpi": "520",
		"ac": "wifi", "device_platform": "android", "os": "android", "ssmix": "a", "language": "zh",
	} {
		p.SetParam(key, value)
	}
	p.SetHeader("User-Agent", playbackUserAgent)
	p.SetHeader("X-TT-Store-Region", "cn")
	if signer, ok := p.signer.(*crypto.DefaultSigner); ok {
		clone := *signer
		clone.Profile = crypto.MobilePlaybackCryptoProfile()
		p.SetSigner(&clone)
	}
	return p
}

// CallPlaybackJSON uses a full signed playback session and a per-request host.
func (s *Session) CallPlaybackJSON(ctx context.Context, body any, host string) (*Result, error) {
	return s.CallPlaybackJSONWithInstallID(ctx, body, host, "", false)
}

// CallPlaybackJSONWithInstallID scopes the alternate installation ID and its
// cookie to this playback request. The registered identity stays with its session.
func (s *Session) CallPlaybackJSONWithInstallID(ctx context.Context, body any, host, installID string, emptyIID bool) (*Result, error) {
	identity := PlaybackIdentity{InstallID: installID}
	if emptyIID {
		empty := ""
		identity.QueryInstallID = &empty
	}
	return s.CallPlaybackJSONWithIdentity(ctx, body, host, identity)
}

// PlaybackIdentity separates the playback session's installation cookie from
// query-only probes. A non-nil QueryInstallID may explicitly send an empty IID.
type PlaybackIdentity struct {
	InstallID      string
	QueryInstallID *string
}

func (s *Session) CallPlaybackJSONWithIdentity(ctx context.Context, body any, host string, identity PlaybackIdentity) (*Result, error) {
	base, ok := s.registry.Get("multi_video_model")
	if !ok {
		return nil, errors.New("api: 播放接口未注册")
	}
	a := *base
	a.Host = host
	a.playerWire = true
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	p := s.playbackSession(identity.InstallID)
	beforeCookie := p.CookieString()
	if iid := p.DeviceContext().InstallID; iid != "" {
		cookies := []string{(&http.Cookie{Name: "install_id", Value: iid}).String()}
		cookieRequest := &http.Request{Header: http.Header{"Cookie": {beforeCookie}}}
		for _, name := range []string{"passport_csrf_token", "passport_csrf_token_default"} {
			if _, err := cookieRequest.Cookie(name); err == http.ErrNoCookie {
				cookies = append(cookies, name+"=")
			}
		}
		p.MergeCookieString(strings.Join(cookies, "; "))
	}
	creds := p.credentials()
	q := p.requestQuery(creds.uid, nil)
	if identity.QueryInstallID != nil {
		q.Set("iid", *identity.QueryInstallID)
	}
	// Keep only the mobile player query fields before signing.
	for name := range q {
		switch name {
		case "_rticket", "ac", "aid", "app_name", "channel", "device_brand",
			"device_id", "device_platform", "device_type", "dpi", "iid", "language",
			"manifest_version_code", "os", "os_api", "os_version", "resolution",
			"ssmix", "update_version_code", "version_code", "version_name":
		default:
			q.Del(name)
		}
	}
	// The player endpoint signs and sends the plain JSON bytes.
	res, err := p.sendRequest(ctx, &a, q, raw, "", false, creds)
	// The wire profile is temporary; anonymous device challenges are not. Keep
	// response state even on an empty body, without overwriting a newer update
	// or copying an anonymous response into the user's signed-in main session.
	s.mu.Lock()
	if identity.InstallID == "" && s.retainPlaybackState && s.credentialGeneration == creds.generation && s.device.DeviceID == p.DeviceContext().DeviceID {
		if cookie := p.CookieString(); s.cookie == beforeCookie && cookie != creds.cookie {
			s.cookie = cookie
		}
		if s.token == creds.token {
			s.token = p.Token()
		}
	}
	s.mu.Unlock()
	return res, err
}
