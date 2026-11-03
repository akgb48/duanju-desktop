package hgapi

import (
	"context"
	"encoding/json"
	"errors"
	"duanju-desktop/pkg/api"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type recoveryProviderFixture struct {
	preferred     string
	entries       []PlaybackSessionEntry
	fresh         *api.Session
	registrations int
	regErr        error
	failed        map[string]time.Time
	lists         int
	onList        func(*recoveryProviderFixture)
}

func (p *recoveryProviderFixture) PlaybackSessions() (string, []PlaybackSessionEntry) {
	p.lists++
	if p.onList != nil {
		p.onList(p)
	}
	return p.preferred, p.entries
}
func (p *recoveryProviderFixture) RegisterPlaybackSession(context.Context) (PlaybackSessionEntry, error) {
	p.registrations++
	if p.regErr != nil {
		return PlaybackSessionEntry{}, p.regErr
	}
	e := PlaybackSessionEntry{ID: "anonymous", Session: p.fresh}
	p.entries = append(p.entries, e)
	return e, nil
}
func (p *recoveryProviderFixture) PlaybackSessionUsed(id string) { p.preferred = id }
func (p *recoveryProviderFixture) PlaybackSessionFailed(id string, until time.Time) {
	if p.failed == nil {
		p.failed = make(map[string]time.Time)
	}
	p.failed[id] = until
}

func TestDevicePoolRotatesBeforeRegisteringAndSkipsCoolingDevices(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example", "three.example", "four.example"}
	t.Cleanup(func() { api.HostPool = old })
	stage := 0
	calls := []string{}
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("device_id")
		calls = append(calls, id)
		body := `{"code":0,"data":null}`
		if (stage == 0 && id == "b") || (stage == 1 && id == "c") {
			body = `{"code":0,"data":{"v":{"video_list":{"v":{"main_url":"https://cdn.example/video"}}}}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: tr}))
	main.SetDevice(api.DeviceContext{DeviceID: "main"})
	p := &recoveryProviderFixture{preferred: "a", regErr: errors.New("registration cooling")}
	for _, id := range []string{"a", "b", "c"} {
		s := main.NewAnonymousSession()
		s.SetDevice(api.DeviceContext{DeviceID: id})
		p.entries = append(p.entries, PlaybackSessionEntry{ID: id, Session: s})
	}
	client := New(main)
	client.SetPlaybackSessionProvider(p)
	body := map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"v"}}}
	if _, err := client.callPlayback(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "a,main,b" {
		t.Fatalf("failed to rotate quickly: %s", got)
	}
	if p.preferred != "b" || p.registrations != 0 || len(p.failed) != 0 {
		t.Fatalf("pool state incorrect: %+v", p)
	}
	calls = nil
	stage = 1
	if _, err := client.callPlayback(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "b,main,a,c" || p.preferred != "c" || p.registrations != 0 {
		t.Fatalf("cooling devices were retried or reserve not selected: %s", got)
	}
}

func TestDevicePoolDoesNotHammerEmptySessionsOnNextRequest(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example"}
	t.Cleanup(func() { api.HostPool = old })
	calls := 0
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}))
	p := &recoveryProviderFixture{regErr: errors.New("cooling")}
	spare := main.NewAnonymousSession()
	p.entries = []PlaybackSessionEntry{{ID: "a", Session: spare}}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err == nil {
		t.Fatal("empty accepted")
	}
	calls = 0
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err == nil {
		t.Fatal("cooldown accepted")
	}
	if calls != 0 {
		t.Fatalf("cooling devices hammered %d times", calls)
	}
	// Once cooldown expires, devices become eligible without a new registration.
	c.playback.cooldownUntil = time.Time{}
	c.sparePlaybackRoutes("a").cooldownUntil = time.Time{}
	c.callPlayback(context.Background(), map[string]any{})
	if calls == 0 {
		t.Fatal("devices did not recover eligibility")
	}
}

func TestDevicePoolStillFindsLaterHostWhenRegistrationIsCooling(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example", "three.example", "four.example"}
	t.Cleanup(func() { api.HostPool = old })
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Host == "four.example" {
			body = `{"code":0,"data":{}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	p := &recoveryProviderFixture{regErr: errors.New("cooling")}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

func TestMissingNullAndEmptyModelDataTriggerDeviceFailover(t *testing.T) {
	body := map[string]any{"mixed_video_id_map": map[string][]string{"1004": {"video"}}}
	for _, raw := range []string{`{"code":0}`, `{"code":0,"data":null}`, `{"code":0,"data":{}}`} {
		r := &api.Result{Raw: []byte(raw), Action: "multi_video_model"}
		if err := json.Unmarshal(r.Raw, &r.Envelope); err != nil {
			t.Fatal(err)
		}
		var empty *api.EmptyResponseError
		if err := validatePlaybackResult(r, body); !errors.As(err, &empty) {
			t.Fatalf("empty success envelope was not switchable: %s %v", raw, err)
		}
	}
}

type playbackTestTimeout struct{}

func (playbackTestTimeout) Error() string   { return "network timeout" }
func (playbackTestTimeout) Timeout() bool   { return true }
func (playbackTestTimeout) Temporary() bool { return true }

func TestNetworkTimeoutsRotateBeforeSweepingAllHosts(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example", "three.example", "four.example"}
	t.Cleanup(func() { api.HostPool = old })
	calls := []string{}
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("device_id")
		calls = append(calls, id)
		if id == "main" {
			return nil, playbackTestTimeout{}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`))}, nil
	})
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: tr}))
	main.SetDevice(api.DeviceContext{DeviceID: "main"})
	spare := main.NewAnonymousSession()
	spare.SetDevice(api.DeviceContext{DeviceID: "reserve"})
	p := &recoveryProviderFixture{entries: []PlaybackSessionEntry{{ID: "reserve", Session: spare}}, regErr: errors.New("must not register")}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "main,reserve" || p.registrations != 0 {
		t.Fatalf("timeout consumed reserve's deadline: %s", got)
	}
}
func TestPlaybackRecoversWithIsolatedSessionAndReusesIt(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example"}
	t.Cleanup(func() { api.HostPool = old })
	var calls []string
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		id := r.URL.Query().Get("device_id")
		calls = append(calls, id+":"+r.URL.Host)
		body := ""
		if id == "spare" {
			if r.Header.Get("Cookie") != "" || r.Header.Get("X-TT-Token") != "" {
				t.Fatal("account leaked into anonymous request")
			}
			body = `{"code":0,"data":{}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: tr}))
	main.SetDevice(api.DeviceContext{DeviceID: "main"})
	main.SetCookieString("sessionid=account")
	main.SetToken("account-token")
	spare := main.NewAnonymousSession()
	spare.SetDevice(api.DeviceContext{DeviceID: "spare"})
	p := &recoveryProviderFixture{fresh: spare}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if p.registrations != 1 || p.preferred != "anonymous" || len(calls) != 3 {
		t.Fatalf("recovery sequence %v registrations=%d", calls, p.registrations)
	}
	calls = nil
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "spare:one.example" || p.registrations != 1 {
		t.Fatalf("working session not reused: %v", calls)
	}
	if main.Token() != "account-token" || main.CookieString() != "sessionid=account" || main.DeviceContext().DeviceID != "main" {
		t.Fatal("recovery mutated main account or device")
	}
}
func TestPlaybackBusinessDenialDoesNotRegisterAnotherDevice(t *testing.T) {
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":403,"message":"denied"}`))}, nil
	})}))
	p := &recoveryProviderFixture{}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	_, err := c.callPlayback(context.Background(), map[string]any{})
	var business *api.BizError
	if !errors.As(err, &business) || p.registrations != 0 {
		t.Fatalf("business denial triggered registration: %v %d", err, p.registrations)
	}
}

func TestPoolRechecksDevicesRegisteredDuringPlaybackFailure(t *testing.T) {
	testLatePoolDevice(t, false)
}

func TestPoolWaitsBrieflyAndUsesBackgroundDeviceWithoutExtraRegistration(t *testing.T) {
	testLatePoolDevice(t, true)
}

func testLatePoolDevice(t *testing.T, wait bool) {
	old := api.HostPool
	api.HostPool = []string{"one.example"}
	t.Cleanup(func() { api.HostPool = old })
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Query().Get("device_id") == "late" {
			body = `{"code":0,"data":{}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	spare := main.NewAnonymousSession()
	spare.SetDevice(api.DeviceContext{DeviceID: "late"})
	p := &recoveryProviderFixture{regErr: errors.New("registration must not be needed")}
	neededList := 2
	if wait {
		neededList = 3
		p.regErr = &PlaybackRegistrationCooldown{Until: time.Now().Add(30 * time.Millisecond), Reason: "warming"}
	}
	p.onList = func(p *recoveryProviderFixture) {
		if p.lists == neededList {
			p.entries = append(p.entries, PlaybackSessionEntry{ID: "late", Session: spare})
		}
	}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.callPlayback(ctx, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	expected := 0
	if wait {
		expected = 1
	}
	if p.preferred != "late" || p.registrations != expected {
		t.Fatal("new background device missed or redundant registration attempted")
	}
}

func TestEmptySpareIsNotRetriedAfterRetirementInSameRequest(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example"}
	t.Cleanup(func() { api.HostPool = old })
	spareCalls := 0
	main := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("device_id") == "bad" {
			spareCalls++
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}))
	spare := main.NewAnonymousSession()
	spare.SetDevice(api.DeviceContext{DeviceID: "bad"})
	p := &recoveryProviderFixture{preferred: "bad", entries: []PlaybackSessionEntry{{ID: "bad", Session: spare}}, regErr: errors.New("cooling")}
	c := New(main)
	c.SetPlaybackSessionProvider(p)
	c.callPlayback(context.Background(), map[string]any{})
	if spareCalls != 1 || p.failed["bad"].IsZero() {
		t.Fatal("retired spare was reused by full host recovery")
	}
}
