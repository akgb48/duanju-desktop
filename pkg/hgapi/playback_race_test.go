package hgapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
)

func raceResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func raceFixture(t *testing.T, hosts []string, deviceIDs []string, transport roundTripFunc) (*Client, *recoveryProviderFixture) {
	t.Helper()
	previous := api.HostPool
	api.HostPool = hosts
	t.Cleanup(func() { api.HostPool = previous })
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: transport}))
	p := &recoveryProviderFixture{regErr: errors.New("must not register during a ready-device race")}
	for _, id := range deviceIDs {
		s := main.NewAnonymousSession()
		s.SetDevice(api.DeviceContext{DeviceID: id})
		p.entries = append(p.entries, PlaybackSessionEntry{ID: id, Session: s})
	}
	return New(main), p
}

func TestPoolRaceHedgesSlowDeviceAndCancelsLoser(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	c, p := raceFixture(t, []string{"one.example"}, []string{"slow", "fast"}, func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Query().Get("device_id") == "slow" {
			close(started)
			<-r.Context().Done()
			close(canceled)
			return nil, r.Context().Err()
		}
		return raceResponse(`{"code":0,"data":{}}`), nil
	})
	start := time.Now()
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, playbackRaceBudget, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second || time.Since(start) < 200*time.Millisecond || requests.Load() != 2 || p.preferred != "fast" {
		t.Fatalf("slow preferred device still blocked ready reserve: elapsed=%s requests=%d winner=%q", time.Since(start), requests.Load(), p.preferred)
	}
	<-started
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("losing request was not canceled")
	}
}

func TestPoolRaceLimitsConcurrencyAndHonorsCancellation(t *testing.T) {
	var active, peak, ended atomic.Int32
	c, p := raceFixture(t, []string{"one.example", "two.example"}, []string{"a", "b", "c", "d", "e"}, func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer active.Add(-1)
		<-r.Context().Done()
		ended.Add(1)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	_, err := c.racePlaybackSessions(ctx, p, p.entries, map[string]any{}, playbackRaceBudget, nil)
	if !errors.Is(err, context.DeadlineExceeded) || peak.Load() != 4 {
		t.Fatalf("parallelism/cancellation mismatch: peak=%d error=%v", peak.Load(), err)
	}
	until := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if ended.Load() != 4 || p.registrations != 0 {
		t.Fatal("cancellation leaked requests, deleted devices or triggered registration")
	}
}

func TestPoolRaceUsesFiniteProbeBudget(t *testing.T) {
	var calls atomic.Int32
	ids := []string{}
	for i := range 20 {
		ids = append(ids, fmt.Sprint(i))
	}
	c, p := raceFixture(t, []string{"one.example", "two.example", "three.example"}, ids, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return raceResponse(`{"code":0,"data":null}`), nil
	})
	body := map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"v"}}}
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, body, playbackRaceBudget, nil); err == nil {
		t.Fatal("empty envelope won race")
	}
	if calls.Load() != 30 {
		t.Fatalf("probe budget exceeded: calls=%d", calls.Load())
	}
}

func TestPoolRaceAcceptsInFlightHealthyHostAfterEmptyResponses(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	c, p := raceFixture(t, []string{"healthy.example", "two.example", "three.example", "four.example"}, []string{"recovering"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "healthy.example" {
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			time.Sleep(30 * time.Millisecond)
			return raceResponse(`{"code":0,"data":{}}`), nil
		}
		if r.URL.Host == "four.example" {
			once.Do(func() { close(release) })
		}
		return raceResponse(""), nil
	})
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, 6, nil); err != nil {
		t.Fatal(err)
	}
	if p.preferred != "recovering" {
		t.Fatal("late healthy response lost its device to earlier empty responses")
	}
}

func TestPoolRaceTriesRegisteredInstallIDBeforeBorrowing(t *testing.T) {
	var calls int
	c, p := raceFixture(t, []string{"one.example", "two.example", "three.example"}, []string{"reserve"}, func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		install, err := r.Cookie("install_id")
		if q.Get("device_id") != "reserve" || err != nil || install.Value != q.Get("iid") {
			t.Error("playback probe sent inconsistent device or installation identity")
		}
		if calls <= 2 {
			if q.Get("iid") != "own-install" {
				t.Error("initial routes did not use the registered install ID")
			}
			return raceResponse(""), nil
		}
		if q.Get("iid") != "alternate-install" {
			t.Error("backup route did not try the alternate install ID")
		}
		res := raceResponse(`{"code":0,"data":{}}`)
		res.Header.Set("Set-Cookie", "install_id=alternate-install")
		res.Header.Set("X-Tt-Token", "temporary-token")
		return res, nil
	})
	session := p.entries[0].Session
	session.SetDevice(api.DeviceContext{DeviceID: "reserve", InstallID: "own-install"})
	session.SetCookieString("install_id=own-install")
	session.SetToken("own-token")
	identities := []api.DeviceContext{session.DeviceContext(), {DeviceID: "alternate", InstallID: "alternate-install"}}
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, playbackRaceBudget, identities); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || session.DeviceContext().InstallID != "own-install" || session.CookieString() != "install_id=own-install" || session.Token() != "own-token" {
		t.Fatal("temporary playback identity changed the stored session")
	}
}
