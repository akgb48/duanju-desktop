package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"hongguo-desktop/pkg/api"
	"hongguo-desktop/pkg/model"
)

func TestSavedProxyPortIsAppliedOnRestoreAndCanBeCleared(t *testing.T) {
	memMu.RLock()
	previous := memSettings
	memMu.RUnlock()
	t.Cleanup(func() { memMu.Lock(); memSettings = previous; memMu.Unlock() })
	a := &App{sess: api.NewSession()}
	if err := a.bootstrapStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.st.Close() })
	settings := model.DefaultSettings()
	settings.ProxyURL = "http://127.0.0.1:45678"
	raw, _ := json.Marshal(localSnapshot{Settings: settings})
	if err := a.st.SetKV(context.Background(), "local.state.v1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreLocal(); err != nil {
		t.Fatal(err)
	}
	proxy := func() *url.URL {
		tr := a.sess.HTTPClient().Transport.(*http.Transport)
		if tr.Proxy == nil {
			return nil
		}
		u, _ := tr.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}})
		return u
	}
	if u := proxy(); u == nil || u.String() != settings.ProxyURL {
		t.Fatal("saved proxy endpoint was ignored during startup")
	}
	settings.ProxyURL = ""
	if err := a.saveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if proxy() != nil {
		t.Fatal("cleared proxy retained old port")
	}
}
