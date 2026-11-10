package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestContentReadSwitchesHostAndRetainsResponseChallenge(t *testing.T) {
	previous := HostPool
	HostPool = []string{"empty.example", "working.example"}
	t.Cleanup(func() { HostPool = previous })
	calls := 0
	var logs []string
	s := NewSession(WithLogger(func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }), WithHTTPClient(&http.Client{Transport: playbackPortTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		header := http.Header{}
		body := ""
		if calls == 1 {
			if r.URL.Host != "empty.example" {
				t.Error("unexpected initial host")
			}
			header.Set("Set-Cookie", "ttreq=challenge-secret")
			header.Set("X-Tt-Token", "response-secret")
			header.Set("X-Tt-Logid", "empty-log")
		} else {
			challenge, err := r.Cookie("ttreq")
			if r.URL.Host != "working.example" || err != nil || challenge.Value != "challenge-secret" || r.Header.Get("X-Tt-Token") != "response-secret" {
				t.Error("retry did not use the next host and updated anonymous challenge")
			}
			body = `{"code":0,"data":{"items":[1]}}`
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	s.SetCookieString("sessionid=account-secret")
	res, err := s.CallJSON(context.Background(), "search", map[string]any{"query": "private-query"})
	if err != nil || res == nil || calls != 2 {
		t.Fatalf("content read did not recover: calls=%d err=%v", calls, err)
	}
	joined := strings.Join(logs, "\n")
	for _, secret := range []string{"account-secret", "response-secret", "challenge-secret", "private-query"} {
		if strings.Contains(joined, secret) {
			t.Fatal("request credentials or parameters leaked into diagnostics")
		}
	}
	if !strings.Contains(joined, "http=200 bytes=0 code=0 outcome=empty") || !strings.Contains(joined, "log_id=empty-log") {
		t.Fatal("empty response lost its diagnostic status")
	}
}

func TestReadRetryIsBoundedAndWritesAreNotReplayed(t *testing.T) {
	for _, action := range []string{"search", "category_landpage", "comment_add", "sms_login"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			s, _ := newTestSession(t, func(http.ResponseWriter, *http.Request) { calls++ })
			res, err := s.CallJSON(context.Background(), action, map[string]any{})
			want := 1
			if action == "search" || action == "category_landpage" {
				want = 3
			}
			var empty *EmptyResponseError
			if calls != want || !errors.As(err, &empty) || res == nil || res.Envelope.HTTPCode != 200 {
				t.Fatalf("incorrect replay policy: calls=%d want=%d result=%v err=%v", calls, want, res, err)
			}
		})
	}
}

func TestReadRetryHonorsCancellationAndAccountChanges(t *testing.T) {
	for _, changeAccount := range []bool{false, true} {
		t.Run(fmt.Sprint(changeAccount), func(t *testing.T) {
			calls := 0
			var session *Session
			session, _ = newTestSession(t, func(http.ResponseWriter, *http.Request) {
				calls++
				if changeAccount {
					session.SetAccount("new-user", "new-token", "new-cookie=value")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := session.CallJSON(ctx, "feed", nil)
			if err == nil || calls != 1 || (!changeAccount && !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("read retry ignored cancellation or account replacement: calls=%d err=%v", calls, err)
			}
		})
	}
}
