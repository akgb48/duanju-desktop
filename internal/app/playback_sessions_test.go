package app

import (
	"context"
	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/register"
	"encoding/json"
	"errors"
	"fmt"
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
func TestPlaybackPoolPacesFailedAttemptsAcrossRestart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return nil, errors.New("offline")
	}
	ctx := context.Background()
	if _, err := p.RegisterPlaybackSession(ctx); err == nil || calls != 1 {
		t.Fatalf("expected one failed registration: calls=%d err=%v", calls, err)
	}
	restored := newPlaybackSessionPool(dir, p.main)
	restored.now, restored.registerDevice = p.now, p.registerDevice
	now = now.Add(playbackAttemptInterval - time.Second)
	_, err := restored.RegisterPlaybackSession(ctx)
	var cooldown *hgapi.PlaybackRegistrationCooldown
	if !errors.As(err, &cooldown) || calls != 1 {
		t.Fatalf("restart bypassed pacing after failure: calls=%d err=%v", calls, err)
	}
	now = now.Add(time.Second)
	restored.RegisterPlaybackSession(ctx)
	if calls != 2 {
		t.Fatal("attempt not permitted after cooldown")
	}
}

func TestPlaybackPoolAppliesRollingQuotaAndEvictsOldestAcrossRestart(t *testing.T) {
	started := time.Now()
	now := started
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return &register.Response{Identity: register.Identity{DeviceID: fmt.Sprint(300000 + calls), InstallID: fmt.Sprint(400000 + calls)}}, nil
	}
	ctx := context.Background()
	var ids []string
	for range playbackRegistrationCap {
		entry, err := p.RegisterPlaybackSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, entry.ID)
		now = now.Add(playbackSuccessInterval)
	}
	p.PlaybackSessionUsed(ids[0])
	restored := newPlaybackSessionPool(dir, p.main)
	restored.now, restored.registerDevice = p.now, p.registerDevice
	_, err := restored.RegisterPlaybackSession(ctx)
	var cooldown *hgapi.PlaybackRegistrationCooldown
	if !errors.As(err, &cooldown) || !cooldown.Until.Equal(started.Add(playbackQuotaWindow)) || calls != playbackRegistrationCap {
		t.Fatalf("rolling quota did not survive restart: calls=%d err=%v", calls, err)
	}
	now = started.Add(playbackQuotaWindow)
	entry, err := restored.RegisterPlaybackSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preferred, entries := restored.PlaybackSessions()
	if preferred != "" || len(entries) != playbackPoolCapacity || entries[0].ID != ids[1] || entries[len(entries)-1].ID != entry.ID {
		t.Fatalf("oldest entry was not evicted when the rolling quota opened: pool=%d", len(entries))
	}
}

func TestPlaybackPoolKeepsSuccessfulChallenge(t *testing.T) {
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

}

func TestPlaybackReserveStopsInFlightRegistrationOnShutdown(t *testing.T) {
	p := newPlaybackSessionPool(t.TempDir(), api.NewSession())
	entered := make(chan struct{})
	p.registerDevice = func(ctx context.Context, _ *register.Profile, _ *http.Client) (*register.Response, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go p.RegisterPlaybackSession(context.Background())
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
	now = now.Add(playbackSuccessInterval)
	second, err := p.RegisterPlaybackSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, entries := p.PlaybackSessions()
	if first.ID != second.ID || len(entries) != 1 || !p.state.LastRegistration.Equal(now) {
		t.Fatal("duplicate device counted as an independent reserve or pacing record lost")
	}
	if len(p.state.Registrations) != 2 {
		t.Fatal("successful duplicate registrations did not count toward the rolling quota")
	}
}

func TestRegistrationThrottleSurvivesRestart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := newPlaybackSessionPool(dir, api.NewSession())
	p.now = func() time.Time { return now }
	calls := 0
	p.registerDevice = func(context.Context, *register.Profile, *http.Client) (*register.Response, error) {
		calls++
		return nil, &register.HTTPStatusError{StatusCode: 429, RetryAfter: time.Hour}
	}
	p.RegisterPlaybackSession(context.Background())
	now = now.Add(time.Minute)
	p.RegisterPlaybackSession(context.Background())
	if calls != 1 {
		t.Fatal("registration rejection triggered device replacement")
	}
	restored := newPlaybackSessionPool(dir, api.NewSession())
	restored.now = func() time.Time { return now }
	restored.registerDevice = p.registerDevice
	restored.RegisterPlaybackSession(context.Background())
	if calls != 1 {
		t.Fatal("restart bypassed registration backoff")
	}
	now = now.Add(time.Hour)
	restored.RegisterPlaybackSession(context.Background())
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
		now = now.Add(playbackSuccessInterval)
	}
	original, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		playbackSessionState
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(original, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Preferred = p.state.Devices[0].ID
	legacy.Devices[0]["needsRecovery"] = true
	legacy.Devices[1]["cooldownUntil"] = now.Add(-time.Hour)
	legacy.Devices[2]["cooldownUntil"] = time.Time{}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	restored := newPlaybackSessionPool(filepath.Dir(p.path), p.main)
	preferred, entries := restored.PlaybackSessions()
	if len(entries) != 1 || preferred != "" {
		t.Fatal("legacy failed devices retained or preferred failed device survived")
	}
	var persisted playbackSessionState
	b, err = os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Devices) != 1 || strings.Contains(string(b), "cooldownUntil") || strings.Contains(string(b), "needsRecovery") {
		t.Fatal("legacy cleanup was not persisted")
	}
}
