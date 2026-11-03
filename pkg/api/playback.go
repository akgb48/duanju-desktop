package api

import (
	"context"
	"encoding/json"
	"fmt"
	"hongguo-desktop/pkg/crypto"
)

const playbackUserAgent = "com.phoenix.read/71532 (Linux; U; Android 9; SM-N9860; Build/PQ3A.190705.10241111;tt-ok/3.12.13.20)"

func (s *Session) playbackSession() *Session {
	p := s.Fork()
	d := p.DeviceContext()
	d.Version, d.VersionCode, d.Channel = "7.1.3.32", "71332", "update_64"
	p.SetDevice(d)
	for key, value := range map[string]string{
		"version_name": "7.1.3.32", "version_code": "71332", "manifest_version_code": "71332", "update_version_code": "71332", "pv_player": "71332",
		"channel": "update_64", "device_brand": "Redmi", "device_type": "25053RT47C", "os_version": "16", "os_api": "36", "resolution": "1280*2772", "dpi": "520",
	} {
		p.SetParam(key, value)
	}
	p.SetHeader("User-Agent", playbackUserAgent)
	p.SetHeader("X-TT-Store-Region", "cn")
	if signer, ok := p.signer.(*crypto.DefaultSigner); ok {
		clone := *signer
		clone.Profile = crypto.HongguoCryptoProfile()
		clone.Profile.Name, clone.Profile.VersionName = "hongguo-playback", "7.1.3.32"
		clone.Profile.Medusa.VersionName = "7.1.3.32"
		p.SetSigner(&clone)
	}
	return p
}

// CallPlaybackJSON uses a full signed playback session and a per-request host.
func (s *Session) CallPlaybackJSON(ctx context.Context, body any, host string) (*Result, error) {
	base, ok := s.registry.Get("multi_video_model")
	if !ok {
		return nil, fmt.Errorf("api: 播放接口未注册")
	}
	a := *base
	a.Host = host
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	p := s.playbackSession()
	beforeCookie, beforeToken := p.CookieString(), p.Token()
	q := p.publicQuery()
	// Playback has neither WithSession nor WithEnv enabled. In particular the
	// desktop-only "from" parameter must not enter this signed mobile query.
	allowed := map[string]bool{}
	for _, name := range []string{"_rticket", "ac", "aid", "app_name", "cdid", "channel", "compliance_status", "device_brand", "device_id", "device_platform", "device_type", "dpi", "dragon_device_type", "host_abi", "iid", "is_android_pad_screen", "language", "manifest_version_code", "need_personal_recommend", "os", "os_api", "os_version", "player_so_load", "pv_player", "resolution", "ssmix", "update_version_code", "version_code", "version_name"} {
		allowed[name] = true
	}
	for name := range q {
		if !allowed[name] {
			q.Del(name)
		}
	}
	res, err := p.send(ctx, &a, q, raw)
	// The wire profile is temporary; anonymous device challenges are not. Keep
	// response state even on an empty body, without overwriting a newer update
	// or copying an anonymous response into the user's signed-in main session.
	s.mu.Lock()
	if s.retainPlaybackState && s.device.DeviceID == p.DeviceContext().DeviceID {
		if s.cookie == beforeCookie {
			s.cookie = p.CookieString()
		}
		if s.token == beforeToken {
			s.token = p.Token()
		}
	}
	s.mu.Unlock()
	return res, err
}
