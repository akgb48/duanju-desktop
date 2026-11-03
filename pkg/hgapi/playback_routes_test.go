package hgapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
)

func TestSignedPlaybackRemembersWorkingHost(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"bad.example", "working.example"}
	t.Cleanup(func() { api.HostPool = old })
	var calls []string
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mode := "raw"
		if r.Header.Get("Content-Encoding") == "gzip" {
			mode = "gzip"
		}
		calls = append(calls, r.URL.Host+":"+mode)
		body := ""
		if mode != "gzip" {
			t.Error("v1.2.19 must not fall back to the old raw unsigned format")
		}
		if r.URL.Host == "working.example" {
			body = `{"code":0,"data":{"ok":true}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	c := New(s)
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected signed requests across two routes: %v", calls)
	}
	calls = nil
	if _, err := c.callPlayback(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "working.example:gzip" {
		t.Fatalf("healthy route not retained: %v", calls)
	}
}

func TestPlaybackStopsForBusinessDenialAndCancellation(t *testing.T) {
	calls := 0
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":403,"message":"denied"}`))}, nil
	})}))
	c := New(s)
	_, err := c.callPlayback(context.Background(), map[string]any{})
	var business *api.BizError
	if calls != 1 || !errors.As(err, &business) {
		t.Fatalf("business denial retried: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.callPlayback(ctx, map[string]any{})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: calls=%d err=%v", calls, err)
	}
}

func TestPlaybackRetriesHTMLGatewayFailureOnAnotherHost(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"gateway.example", "healthy.example"}
	t.Cleanup(func() { api.HostPool = old })
	calls := 0
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		status, body := 200, `{"code":0,"data":{}}`
		if r.URL.Host == "gateway.example" {
			status, body = 502, "<html>Bad Gateway</html>"
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	_, err := New(s).callPlayback(context.Background(), map[string]any{})
	if err != nil || calls != 2 {
		t.Fatalf("gateway failure did not switch host: calls=%d err=%v", calls, err)
	}
}

func TestFullSignedPlaybackTriesHostsBeyondThreeEmptyReplies(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example", "three.example", "four.example"}
	t.Cleanup(func() { api.HostPool = old })
	calls := 0
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := ""
		if r.URL.Host == "four.example" {
			body = `{"code":0,"data":{}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	_, err := New(s).callPlayback(context.Background(), map[string]any{})
	if calls != 4 || err != nil {
		t.Fatalf("full signed playback skipped a usable later host: calls=%d err=%v", calls, err)
	}
}

func TestPlaybackCooldownKeepsEveryHostAvailable(t *testing.T) {
	old := api.HostPool
	api.HostPool = []string{"one.example", "two.example", "three.example"}
	t.Cleanup(func() { api.HostPool = old })
	now := time.Now()
	r := playbackRoutes{badUntil: map[string]time.Time{"one.example": now.Add(time.Minute)}}
	if got := strings.Join(r.order(now), ","); got != "two.example,three.example,one.example" {
		t.Fatalf("cooling host was discarded or not delayed: %s", got)
	}
	for _, host := range api.HostPool {
		r.badUntil[host] = now.Add(time.Minute)
	}
	if got := strings.Join(r.order(now), ","); got != strings.Join(api.HostPool, ",") {
		t.Fatalf("all cooling hosts must remain available: %s", got)
	}
}
