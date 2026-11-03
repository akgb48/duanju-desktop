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

	"hongguo-desktop/pkg/api"
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
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, playbackRaceBudget); err != nil {
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
	if len(p.failed) != 0 || c.sparePlaybackRoutes("slow").retired {
		t.Fatal("canceled loser was treated as a failed device")
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
	_, err := c.racePlaybackSessions(ctx, p, p.entries, map[string]any{}, playbackRaceBudget)
	if !errors.Is(err, context.DeadlineExceeded) || peak.Load() != 4 {
		t.Fatalf("parallelism/cancellation mismatch: peak=%d error=%v", peak.Load(), err)
	}
	until := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if ended.Load() != 4 || len(p.failed) != 0 || p.registrations != 0 {
		t.Fatal("cancellation leaked requests, deleted devices or triggered registration")
	}
}

func TestPoolRaceUsesFiniteProbeBudgetWithoutDeletingUnconfirmedDevices(t *testing.T) {
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
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, body, playbackRaceBudget); err == nil {
		t.Fatal("empty envelope won race")
	}
	if calls.Load() != 30 || len(p.failed) != 0 {
		t.Fatalf("probe budget exceeded or partial host failure deleted a device: calls=%d failed=%d", calls.Load(), len(p.failed))
	}
}

func TestPoolRaceConfirmsDistinctEmptyHostsAcrossProbes(t *testing.T) {
	c, p := raceFixture(t, []string{"one.example", "two.example", "three.example", "four.example"}, []string{"bad"}, func(*http.Request) (*http.Response, error) {
		return raceResponse(""), nil
	})
	c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, 2)
	if len(p.failed) != 0 {
		t.Fatal("one or two empty hosts deleted a device")
	}
	c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, 2)
	if !c.sparePlaybackRoutes("bad").retired || len(p.failed) != 1 {
		t.Fatal("confirmed failed device was retained")
	}
}

func TestPoolRaceWaitsForInFlightHealthyHostBeforeDeletingDevice(t *testing.T) {
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
	if _, err := c.racePlaybackSessions(context.Background(), p, p.entries, map[string]any{}, 6); err != nil {
		t.Fatal(err)
	}
	if len(p.failed) != 0 || c.sparePlaybackRoutes("recovering").retired || p.preferred != "recovering" {
		t.Fatal("late healthy response lost its device to earlier empty responses")
	}
}
