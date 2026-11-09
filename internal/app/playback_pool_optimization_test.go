package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/register"
)

func TestPlaybackPoolUnchangedWinnerDoesNotWriteAndRetriesFailedCheckpoint(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	t.Cleanup(p.stop)
	p.PlaybackSessionUsed("")
	if _, err := os.Stat(p.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("healthy primary session created an unnecessary pool file")
	}
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		return &register.Response{Identity: register.Identity{DeviceID: "1234567", InstallID: "7654321"}}, nil
	}
	entry, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.PlaybackSessionUsed(entry.ID)
	before, _ := os.ReadFile(p.path)
	before = append(before, '\n') // Valid JSON whitespace detects a replacement.
	if err := os.WriteFile(p.path, before, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		p.PlaybackSessionUsed(entry.ID)
	}
	after, err := os.ReadFile(p.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unchanged winner rewrote the pool file")
	}
	path := p.path
	p.path = filepath.Join(t.TempDir(), "missing", "pool.json")
	entry.Session.SetToken("updated-device-challenge")
	p.PlaybackSessionUsed(entry.ID)
	if !p.dirty {
		t.Fatal("failed checkpoint lost its pending state")
	}
	p.path = path
	p.PlaybackSessionUsed(entry.ID)
	restored := newPlaybackSessionPool(filepath.Dir(path), p.main)
	t.Cleanup(restored.stop)
	_, entries := restored.PlaybackSessions()
	if p.dirty || len(entries) != 1 || entries[0].Session.Token() != "updated-device-challenge" {
		t.Fatal("next successful playback did not retry the failed checkpoint")
	}
}

func TestPlaybackRegistrationStarterCanCancelWithoutDiscardingSharedWork(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	t.Cleanup(p.stop)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	p.registerDevice = func(ctx context.Context, _ *register.Profile, _ *http.Client) (*register.Response, error) {
		close(started)
		select {
		case <-release:
			return &register.Response{Identity: register.Identity{DeviceID: "1234567", InstallID: "7654321"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := p.RegisterPlaybackSession(ctx); first <- err }()
	<-started
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("starter did not receive cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("starter waited for the detached 20-second registration")
	}
	p.mu.Lock()
	flight := p.inFlight
	p.mu.Unlock()
	if flight == nil {
		t.Fatal("canceling the starter discarded the shared registration")
	}
	once.Do(func() { close(release) })
	select {
	case <-flight.done:
		if flight.err != nil || flight.entry.Session == nil {
			t.Fatalf("canceled starter discarded registration: %+v %v", flight.entry, flight.err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared registration did not complete")
	}
}

func TestValidatedTemporaryDeviceBecomesReusableSavedReserveAcrossRestart(t *testing.T) {
	oldHosts := api.HostPool
	api.HostPool = []string{"one.example"}
	t.Cleanup(func() { api.HostPool = oldHosts })
	requests := []string{}
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: accountTransport(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("device_id")
		requests = append(requests, id)
		if id == "primary" {
			return accountResponse(`{"code":0,"data":{}}`), nil
		}
		if _, err := r.Cookie("sessionid"); err == nil || r.Header.Get("X-TT-Token") == "account-token" {
			t.Error("account credentials reached a reserve device")
		}
		var params struct {
			IDs map[string][]string `json:"mixed_video_id_map"`
		}
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			return nil, err
		}
		data := map[string]any{}
		for _, ids := range params.IDs {
			for _, video := range ids {
				data[video] = map[string]any{"video_list": map[string]any{"v": map[string]any{"main_url": "https://media.example/" + video}}}
			}
		}
		body, _ := json.Marshal(map[string]any{"code": 0, "data": data})
		res := accountResponse(string(body))
		res.Header.Set("X-TT-Token", "anonymous-challenge")
		return res, nil
	})}))
	main.SetDevice(api.DeviceContext{DeviceID: "primary"})
	main.SetAccount("user", "account-token", "sessionid=account-cookie")
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, main)
	t.Cleanup(p.stop)
	registrations := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		registrations++
		return &register.Response{Identity: register.Identity{DeviceID: "1234567", InstallID: "7654321"}}, nil
	}
	c := hgapi.New(main)
	c.SetPlaybackSessionProvider(p)
	if err := c.PrefetchVideoModels(context.Background(), []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	preferred, entries := p.PlaybackSessions()
	if preferred == "" || len(entries) != 1 || registrations != 1 || len(p.state.Registrations) != 1 {
		t.Fatal("validated recovery device was not retained under the saved quota")
	}
	if err := c.PrefetchVideoModels(context.Background(), []string{"v2"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[0] != "primary" || requests[1] != "1234567" || requests[2] != "1234567" || registrations != 1 {
		t.Fatalf("next video registered again instead of reusing the winner: %v, registrations=%d", requests, registrations)
	}
	restored := newPlaybackSessionPool(dir, main)
	t.Cleanup(restored.stop)
	got, entries := restored.PlaybackSessions()
	if got != preferred || len(entries) != 1 || entries[0].Session.Token() != "anonymous-challenge" {
		t.Fatal("restart lost the proven reserve or its anonymous challenge")
	}
	c = hgapi.New(main)
	c.SetPlaybackSessionProvider(restored)
	if err := c.PrefetchVideoModels(context.Background(), []string{"v3"}); err != nil || len(requests) != 4 || requests[3] != "1234567" {
		t.Fatalf("restored reserve was not preferred: %v, requests=%v", err, requests)
	}
	if main.Token() != "account-token" || main.CookieString() != "sessionid=account-cookie" {
		t.Fatal("promotion or replay changed the main account")
	}
}

func TestTemporaryPromotionRespectsIntervalQuotaAndDeduplicates(t *testing.T) {
	now := time.Now()
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	t.Cleanup(p.stop)
	p.now = func() time.Time { return now }
	n := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		n++
		id := n
		if n == 4 {
			id = 1
		}
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(10000 + id), InstallID: fmt.Sprint(20000 + n)}}, nil
	}
	first, _ := p.RegisterTemporaryPlaybackSession(context.Background())
	if !p.PromoteTemporaryPlaybackSession(first) || p.PromoteTemporaryPlaybackSession(first) {
		t.Fatal("promotion was not exactly once")
	}
	second, _ := p.RegisterTemporaryPlaybackSession(context.Background())
	if p.PromoteTemporaryPlaybackSession(second) || len(p.state.Devices) != 1 || len(p.state.Registrations) != 1 {
		t.Fatal("promotion bypassed the five-minute saved-device interval")
	}
	now = now.Add(playbackSuccessInterval)
	p.mu.Lock()
	for len(p.state.Registrations) < playbackRegistrationCap {
		p.state.Registrations = append(p.state.Registrations, now)
	}
	p.mu.Unlock()
	third, _ := p.RegisterTemporaryPlaybackSession(context.Background())
	if p.PromoteTemporaryPlaybackSession(third) || len(p.state.Devices) != 1 {
		t.Fatal("promotion bypassed the rolling 24-hour quota")
	}
	fourth, _ := p.RegisterTemporaryPlaybackSession(context.Background())
	fourth.SetToken("verified-new-challenge")
	if !p.PromoteTemporaryPlaybackSession(fourth) || len(p.state.Devices) != 1 || len(p.state.Registrations) != playbackRegistrationCap {
		t.Fatal("duplicate server identity inflated the reserve/quota")
	}
	_, entries := p.PlaybackSessions()
	if entries[0].Session != fourth || entries[0].Session.Token() != "verified-new-challenge" {
		t.Fatal("duplicate promotion lost the newly validated session")
	}
}
