package api

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestAnonymousSessionDoesNotShareAccountStateOrCookieJar(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: http.DefaultTransport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	main := NewSession(WithHTTPClient(client), WithScheme("http"))
	main.SetToken("account")
	main.SetUID("user")
	main.SetCookieString("sessionid=account")
	main.SetHeader("Authorization", "Bearer account")
	main.SetParam("user_id", "user")
	other := main.NewAnonymousSession()
	if other.Token() != "" || other.UID() != "" || other.CookieString() != "" || len(other.params) != 0 || len(other.headers) != 0 || other.signer != nil {
		t.Fatal("anonymous session inherited account context")
	}
	if other.client == client || other.client.Jar != nil || other.client.CheckRedirect != nil || other.client.Transport != client.Transport || other.scheme != "http" {
		t.Fatal("connection configuration was copied unsafely")
	}
	if main.Token() != "account" || main.CookieString() != "sessionid=account" || main.client.Jar != jar {
		t.Fatal("main session mutated")
	}
}

func TestAnonymousConnectionFollowsProxyPortAndDirectReset(t *testing.T) {
	main := NewSession()
	spare := main.NewAnonymousSession()
	spare.SetToken("device-token")
	spare.SetCookieString("ttreq=device-cookie")
	for _, proxy := range []string{"http://127.0.0.1:45123", "http://127.0.0.1:45124", ""} {
		main.SetProxy(proxy)
		spare.SyncAnonymousConnectionFrom(main)
		transport := spare.HTTPClient().Transport.(*http.Transport)
		var got *url.URL
		if transport.Proxy != nil {
			got, _ = transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}})
		}
		if proxy == "" {
			if got != nil {
				t.Fatal("direct reset retained stale proxy port")
			}
		} else if got == nil || got.String() != proxy {
			t.Fatalf("proxy port not synchronized: %v", got)
		}
		if spare.Token() != "device-token" || spare.CookieString() != "ttreq=device-cookie" {
			t.Fatal("connection refresh replaced device challenges")
		}
	}
}

func TestAnonymousConnectionReusesClientUntilSourceChanges(t *testing.T) {
	main := NewSession()
	spare := main.NewAnonymousSession()
	first := spare.HTTPClient()
	for i := 0; i < 100; i++ {
		spare.SyncAnonymousConnectionFrom(main)
		if spare.HTTPClient() != first {
			t.Fatal("unchanged proxy configuration rebuilt the anonymous client")
		}
	}
	main.SetProxy("http://127.0.0.1:45124")
	spare.SyncAnonymousConnectionFrom(main)
	second := spare.HTTPClient()
	if second == first || second == main.HTTPClient() {
		t.Fatal("proxy change was ignored or account client shared")
	}
	spare.SyncAnonymousConnectionFrom(main)
	if spare.HTTPClient() != second {
		t.Fatal("new proxy configuration was rebuilt repeatedly")
	}
}
