package app

import (
	"net/http"
	"testing"
)

func TestDesktopIsReadyBeforeFrontendCreationAndDoesNotBootstrapTwice(t *testing.T) {
	t.Setenv("HG_DATA_DIR", t.TempDir())
	a := NewInitialized()
	t.Cleanup(a.shutdown)
	// No OnStartup call: the first JS integrity check can arrive before Wails'
	// asynchronous callback has completed (or even started).
	r, err := a.Integrity()
	if err != nil || !r.OK {
		t.Fatalf("frontend observed incomplete local startup: %+v %v", r, err)
	}
	if a.st == nil || a.svc == nil || a.proxy == nil {
		t.Fatal("local dependencies were published before they were ready")
	}
	proxy, st, base := a.proxy, a.st, a.proxyBase
	a.bootstrap() // OnStartup must reuse the prepared resources.
	if a.proxy != proxy || a.st != st || a.proxyBase != base {
		t.Fatal("OnStartup recreated local resources")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Get(base + "/audio")
	if err != nil {
		t.Fatalf("reported ready but listener unavailable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("local proxy returned %d", resp.StatusCode)
	}
}
