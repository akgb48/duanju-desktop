package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/register"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaybackPoolSharesRegistrationAndKeepsAccountSeparate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "device.json"), []byte("main-device-sentinel"), 0600)
	main := api.NewSession()
	main.SetToken("account-token")
	main.SetUID("account-user")
	main.SetCookieString("sessionid=account-cookie")
	p := newPlaybackSessionPool(dir, main)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p.registerDevice = func(ctx context.Context, profile *register.Profile, httpc *http.Client) (*register.Response, error) {
		calls.Add(1)
		close(entered)
		<-release
		return &register.Response{Identity: register.Identity{DeviceID: "123456789", InstallID: "987654321", StoreRegion: "cn-tj"}, Cookies: []*http.Cookie{{Name: "sessionid", Value: "unexpected-account"}, {Name: "ttreq", Value: "device-challenge"}}}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := p.RegisterPlaybackSession(context.Background()); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.RegisterPlaybackSession(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled waiter: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	preferred, entries := p.PlaybackSessions()
	if preferred != "" || len(entries) != 1 || calls.Load() != 1 {
		t.Fatalf("registration shared incorrectly: %q %d %d", preferred, len(entries), calls.Load())
	}
	s := entries[0].Session
	if s.Token() != "" || s.UID() != "" || strings.Contains(s.CookieString(), "account") || !strings.Contains(s.CookieString(), "ttreq=device-challenge") {
		t.Fatal("anonymous device inherited account or lost challenge")
	}
	if main.Token() != "account-token" || main.UID() != "account-user" || main.CookieString() != "sessionid=account-cookie" {
		t.Fatal("main account changed")
	}
	sentinel, _ := os.ReadFile(filepath.Join(dir, "device.json"))
	if string(sentinel) != "main-device-sentinel" {
		t.Fatal("main device file replaced")
	}
	p.PlaybackSessionUsed(entries[0].ID)
	restored := newPlaybackSessionPool(dir, main)
	got, items := restored.PlaybackSessions()
	if got != entries[0].ID || len(items) != 1 {
		t.Fatal("preferred session did not persist")
	}
	if _, err := restored.RegisterPlaybackSession(context.Background()); err == nil {
		t.Fatal("restart bypassed successful registration interval")
	}
}
func TestPlaybackPoolPacesFailedAttemptsWithoutDailyQuota(t *testing.T) {
	now := time.Now()
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return nil, errors.New("offline")
	}
	p.RegisterPlaybackSession(context.Background())
	now = now.Add(10 * time.Second)
	p.RegisterPlaybackSession(context.Background())
	if calls != 1 {
		t.Fatal("failed attempts retried without cooldown")
	}
	now = now.Add(21 * time.Second)
	p.RegisterPlaybackSession(context.Background())
	if calls != 2 {
		t.Fatal("attempt not permitted after cooldown")
	}
}

func TestPlaybackPoolReplenishesBeyondLegacyQuotaAcrossRestart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	main := api.NewSession()
	p := newPlaybackSessionPool(dir, main)
	legacy := map[string]any{"version": 1, "devices": []any{}, "registrations": make([]time.Time, 40)}
	for i := range legacy["registrations"].([]time.Time) {
		legacy["registrations"].([]time.Time)[i] = now.Add(-time.Hour)
	}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(p.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	configure := func(pool *playbackSessionPool) {
		pool.now = func() time.Time { return now }
		pool.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
			calls++
			return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(100000 + calls), InstallID: fmt.Sprint(200000 + calls)}}, nil
		}
	}
	configure(p)
	for i := 0; i < 85; i++ {
		p.maintain(context.Background())
		_, entries := p.PlaybackSessions()
		if len(entries) != 1 || calls != i+1 {
			t.Fatalf("replenishment stopped at replacement %d: calls=%d ready=%d", i, calls, len(entries))
		}
		p.PlaybackSessionFailed(entries[0].ID, now.Add(time.Minute))
		if i == 42 {
			p = newPlaybackSessionPool(dir, main)
			configure(p)
			p.maintain(context.Background())
			if calls != i+1 {
				t.Fatal("restart reset pacing instead of only removing the old quota")
			}
		}
		now = now.Add(playbackSuccessInterval + time.Second)
	}
	b, _ = os.ReadFile(p.path)
	if strings.Contains(string(b), `"registrations"`) {
		t.Fatal("obsolete daily-quota records were not removed on save")
	}
}

func TestPlaybackPoolKeepsHealthyDevicesBeyondOldStorageCap(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(300000 + calls), InstallID: fmt.Sprint(400000 + calls)}}, nil
	}
	first := ""
	for i := 0; i < 45; i++ {
		entry, err := p.RegisterPlaybackSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = entry.ID
			p.PlaybackSessionUsed(first)
		}
		now = now.Add(playbackSuccessInterval + time.Second)
	}
	restored := newPlaybackSessionPool(dir, p.main)
	preferred, entries := restored.PlaybackSessions()
	if preferred != first || len(entries) != 45 {
		t.Fatalf("healthy device evicted by the old storage cap: preferred=%q devices=%d", preferred, len(entries))
	}
}

func TestPlaybackPoolKeepsSuccessfulChallengeAndDeletesFailedDevice(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	main := api.NewSession()
	p := newPlaybackSessionPool(dir, main)
	p.now = func() time.Time { return now }
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		return &register.Response{Identity: register.Identity{DeviceID: "1234567", InstallID: "7654321"}}, nil
	}
	entry, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry.Session.SetToken("anonymous-challenge-token")
	entry.Session.SetCookieString("ttreq=next; sessionid=must-not-persist")
	_, entries := p.PlaybackSessions()
	if entries[0].Session != entry.Session {
		t.Fatal("runtime session rebuilt and server state lost")
	}
	p.PlaybackSessionUsed(entry.ID)
	restored := newPlaybackSessionPool(dir, main)
	_, entries = restored.PlaybackSessions()
	if len(entries) != 1 || entries[0].Session.Token() != "anonymous-challenge-token" || !strings.Contains(entries[0].Session.CookieString(), "ttreq=next") || strings.Contains(entries[0].Session.CookieString(), "sessionid") {
		t.Fatal("successful device-only challenge did not persist")
	}
	restored.PlaybackSessionFailed(entry.ID, now.Add(time.Minute))
	restored.PlaybackSessionUsed(entry.ID)
	restoredAgain := newPlaybackSessionPool(dir, main)
	preferred, entries := restoredAgain.PlaybackSessions()
	if preferred != "" || len(entries) != 0 || len(restored.sessions) != 0 {
		t.Fatal("failed device survived removal or stale success resurrected it")
	}
	b, _ := os.ReadFile(p.path)
	if strings.Contains(string(b), "anonymous-challenge-token") || strings.Contains(string(b), "1234567") {
		t.Fatal("removed device identity/token retained in device file")
	}
	now = now.Add(time.Minute)
	restoredAgain.now = func() time.Time { return now }
	restoredAgain.registerDevice = p.registerDevice
	if _, err := restoredAgain.RegisterPlaybackSession(context.Background()); err == nil {
		t.Fatal("server reissued retired device and it was accepted")
	}
}

func TestPlaybackReserveFillsGraduallyAndStopsAtTarget(t *testing.T) {
	now := time.Now()
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(100000 + calls), InstallID: fmt.Sprint(200000 + calls)}}, nil
	}
	for i := 0; i < playbackPoolTarget; i++ {
		p.maintain(context.Background())
		p.maintain(context.Background())
		now = now.Add(playbackSuccessInterval + time.Second)
	}
	_, entries := p.PlaybackSessions()
	if len(entries) != playbackPoolTarget || calls != playbackPoolTarget {
		t.Fatalf("unbounded reserve: %d entries %d registrations", len(entries), calls)
	}
}

func TestPlaybackReserveStopsInFlightRegistrationOnShutdown(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	entered := make(chan struct{})
	p.registerDevice = func(ctx context.Context, _ *register.Profile, _ *http.Client) (*register.Response, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.start(context.Background())
	<-entered
	done := make(chan struct{})
	go func() { p.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to cancel reserve registration")
	}
}

func TestRepeatedServerDeviceDoesNotInflateReserve(t *testing.T) {
	now := time.Now()
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	p.now = func() time.Time { return now }
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		return &register.Response{Identity: register.Identity{DeviceID: "123456789", InstallID: "987654321"}}, nil
	}
	first, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(playbackSuccessInterval + time.Second)
	second, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, entries := p.PlaybackSessions()
	if first.ID != second.ID || len(entries) != 1 || !p.state.LastRegistration.Equal(now) {
		t.Fatal("duplicate device counted as an independent reserve or pacing record lost")
	}
}

func TestFailedDevicesAreNotCountedAsReadyReserve(t *testing.T) {
	now := time.Now()
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(100000 + calls), InstallID: fmt.Sprint(200000 + calls)}}, nil
	}
	for i := 0; i < playbackPoolTarget; i++ {
		p.maintain(context.Background())
		now = now.Add(playbackSuccessInterval + time.Second)
	}
	_, entries := p.PlaybackSessions()
	failedID := entries[0].ID
	p.PlaybackSessionFailed(failedID, now.Add(30*time.Second))
	// Cooldown expiry permits a probe but does not prove the device healthy.
	now = now.Add(time.Minute)
	p.maintain(context.Background())
	if calls != playbackPoolTarget+1 {
		t.Fatal("expired failed device counted as a proven reserve")
	}
	p.PlaybackSessionUsed(failedID)
	now = now.Add(time.Minute)
	p.maintain(context.Background())
	if calls != playbackPoolTarget+1 {
		t.Fatal("reserve grew despite enough healthy devices")
	}
}

func TestRegistrationThrottleStopsBackgroundDeviceReplacement(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return nil, &register.HTTPStatusError{StatusCode: 429, RetryAfter: time.Hour}
	}
	p.maintain(context.Background())
	now = now.Add(time.Minute)
	p.maintain(context.Background())
	if calls != 1 {
		t.Fatal("registration rejection triggered device replacement")
	}
	restored := newPlaybackSessionPool(dir, api.NewSession())
	restored.now = func() time.Time { return now }
	restored.registerDevice = p.registerDevice
	restored.maintain(context.Background())
	if calls != 1 {
		t.Fatal("restart bypassed registration backoff")
	}
	now = now.Add(time.Hour)
	restored.maintain(context.Background())
	if calls != 2 {
		t.Fatal("retry-after expiry did not restore registration")
	}
}

func TestLegacyFailedDevicesAreRemovedOnLoadAndPersisted(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	now := time.Now()
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(10000 + calls), InstallID: fmt.Sprint(20000 + calls)}}, nil
	}
	for i := 0; i < 3; i++ {
		p.RegisterPlaybackSession(context.Background())
		now = now.Add(time.Minute)
	}
	p.state.Preferred = p.state.Devices[0].ID
	p.state.Devices[0].NeedsRecovery = true
	p.state.Devices[1].CooldownUntil = now.Add(-time.Hour)
	p.saveLocked()
	restored := newPlaybackSessionPool(filepath.Dir(p.path), p.main)
	preferred, entries := restored.PlaybackSessions()
	if len(entries) != 1 || preferred != "" || len(restored.state.RetiredHashes) != 2 {
		t.Fatal("legacy failed devices retained or preferred failed device survived")
	}
	var persisted playbackSessionState
	b, _ := os.ReadFile(p.path)
	json.Unmarshal(b, &persisted)
	if len(persisted.Devices) != 1 {
		t.Fatal("legacy cleanup was not persisted")
	}
}

func TestRemovalWakesReserveWorkerAndPreservesRegistrationLimits(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	now := time.Now()
	p.now = func() time.Time { return now }
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		return &register.Response{Identity: register.Identity{DeviceID: "123456", InstallID: "654321"}}, nil
	}
	entry, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.PlaybackSessionUsed(entry.ID)
	p.PlaybackSessionFailed(entry.ID, now.Add(time.Minute))
	select {
	case <-p.wake:
	default:
		t.Fatal("removal did not wake replenisher")
	}
	_, err = p.RegisterPlaybackSession(context.Background())
	var cooldown *hgapi.PlaybackRegistrationCooldown
	if !errors.As(err, &cooldown) || !cooldown.Until.Equal(now.Add(30*time.Second)) {
		t.Fatal("removal reset registration interval")
	}
}
