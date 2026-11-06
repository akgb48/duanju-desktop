package hgapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
)

type temporaryProviderFixture struct {
	*recoveryProviderFixture
	registerTemporary func(context.Context) (*api.Session, error)
	temporaryCalls    int
}

func (p *temporaryProviderFixture) RegisterTemporaryPlaybackSession(ctx context.Context) (*api.Session, error) {
	p.temporaryCalls++
	return p.registerTemporary(ctx)
}

func TestEmptyPlaybackUsesTemporaryDeviceAndRetainsSavedPool(t *testing.T) {
	var calls []string
	savedReady := false
	client, saved := raceFixture(t, []string{"one.example", "two.example", "three.example"}, []string{"saved"}, func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("device_id")
		calls = append(calls, id)
		if id == "temporary" {
			_, cookieErr := r.Cookie("sessionid")
			if r.URL.Query().Get("iid") != "temporary-install" || r.Header.Get("X-TT-Token") != "" || !errors.Is(cookieErr, http.ErrNoCookie) {
				t.Error("temporary request lost its own IID or inherited account credentials")
			}
			var body struct {
				IDs map[string][]string `json:"mixed_video_id_map"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.IDs["1004"]) > 0 {
				return raceResponse(`{"code":101000,"message":"type mismatch"}`), nil
			}
			if len(body.IDs["1001"]) != 1 {
				t.Error("temporary device did not retry the requested video type")
			}
		} else if !savedReady {
			return raceResponse(`{"code":0,"data":{}}`), nil
		}
		return raceResponse(`{"code":0,"data":{"episode":{"video_list":{"v":{"main_url":"https://media.example/episode.mp4"}}}}}`), nil
	})
	client.sess.SetDevice(api.DeviceContext{DeviceID: "main"})
	client.sess.SetAccount("user", "account-token", "sessionid=account")
	saved.preferred = "saved"
	temporary := client.sess.NewAnonymousSession()
	temporary.SetDevice(api.DeviceContext{DeviceID: "temporary", InstallID: "temporary-install"})
	provider := &temporaryProviderFixture{recoveryProviderFixture: saved, registerTemporary: func(context.Context) (*api.Session, error) {
		return temporary, nil
	}}
	client.SetPlaybackSessionProvider(provider)
	body := videoModelParams("episode", 1004)

	res, err := client.callPlayback(context.Background(), body)
	if err != nil || res == nil {
		t.Fatalf("temporary device failed to recover playback: %v", err)
	}
	if len(calls) != playbackFastRaceBudget+2 || calls[0] != "saved" || calls[1] != "main" || calls[3] != "temporary" || calls[4] != "temporary" {
		t.Fatalf("unexpected request order: %v", calls)
	}
	if provider.temporaryCalls != 1 || saved.registrations != 0 || saved.preferred != "saved" || len(saved.entries) != 1 {
		t.Fatal("temporary device changed the saved pool")
	}
	if client.sess.Token() != "account-token" || client.sess.CookieString() != "sessionid=account" {
		t.Fatal("temporary recovery changed the primary account")
	}

	savedReady = true
	if _, err := client.callPlayback(context.Background(), body); err != nil || provider.temporaryCalls != 1 {
		t.Fatalf("could not use a saved device during temporary cooldown: %v", err)
	}
}

func TestTemporaryPlaybackRespectsRecoveryDelay(t *testing.T) {
	p := temporaryPlayback{gate: make(chan struct{}, 1), failures: 5}
	provider := &temporaryProviderFixture{registerTemporary: func(context.Context) (*api.Session, error) {
		return nil, errors.New("offline")
	}}
	_, err := p.fetch(context.Background(), provider, nil)
	var cooldown *PlaybackRegistrationCooldown
	if !errors.As(err, &cooldown) || time.Until(cooldown.Until) < temporaryPlaybackMaxDelay-time.Second {
		t.Fatalf("temporary registration failure did not return the bounded backoff: %v", err)
	}
	if _, err := p.fetch(context.Background(), provider, nil); !errors.As(err, &cooldown) || provider.temporaryCalls != 1 {
		t.Fatalf("a second request bypassed backoff: calls=%d err=%v", provider.temporaryCalls, err)
	}
}
