package app

import (
	"context"
	"hongguo-desktop/pkg/model"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWebRPCBoundaryAndPersistence(t *testing.T) {
	t.Setenv("HG_DATA_DIR", t.TempDir())
	a := New()
	h := WebHandler(a, fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}})
	defer a.OnShutdown(context.Background())
	request := func(method, route, body, origin, marker string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:34116"+route, strings.NewReader(body))
		if marker != "" {
			req.Header.Set("X-HG-Local", marker)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	if r := request("POST", "/api/invoke/OnShutdown", "[]", "", "1"); r.Code != 404 {
		t.Fatalf("lifecycle exposed: %d", r.Code)
	}
	if r := request("POST", "/api/invoke/Status", "[]", "https://untrusted.example", "1"); r.Code != 403 {
		t.Fatalf("cross-origin accepted: %d", r.Code)
	}
	if r := request("POST", "/api/invoke/Status", "[]", "", ""); r.Code != 403 {
		t.Fatalf("missing local marker accepted: %d", r.Code)
	}
	if r := request("POST", "/api/invoke/SetFavorite", `["fixture-only",true]`, "", "1"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request("POST", "/api/invoke/Favorites", `["",false]`, "", "1"); !strings.Contains(r.Body.String(), "fixture-only") {
		t.Fatal(r.Body.String())
	}
	memMu.Lock()
	memFavorites = map[string]model.Series{}
	memMu.Unlock()
	if err := a.restoreLocal(); err != nil {
		t.Fatal(err)
	}
	if !a.isFavorite("fixture-only") {
		t.Fatal("favorite was not persisted")
	}
	if r := request("GET", "/series/deep-link", "", "", ""); r.Code != 200 || !strings.Contains(r.Body.String(), "<html>") {
		t.Fatalf("SPA fallback: %d", r.Code)
	}
}
