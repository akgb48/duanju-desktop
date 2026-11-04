package media

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
)

func TestImageProxyBlocksLoopbackUpstream(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	}))
	defer upstream.Close()

	proxy := NewProxy("", &fakeResolver{imageURL: upstream.URL})
	defer proxy.Close()
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+url.PathEscape(upstream.URL), nil))
	if rec.Code != http.StatusBadGateway || hits.Load() != 0 {
		t.Fatalf("loopback request reached the upstream: status=%d hits=%d", rec.Code, hits.Load())
	}
}

func TestPublicImageIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "fd00::1", "64:ff9b::a00:1", "2002:7f00:1::"} {
		if publicImageIP(netip.MustParseAddr(ip).Unmap()) {
			t.Errorf("accepted non-public address %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicImageIP(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public address %s", ip)
		}
	}
}

func TestHEICCoverConvertsToJPEGAndCachesWithSignedQuery(t *testing.T) {
	fixture, err := os.ReadFile("testdata/cover.heic")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("signature") != "a+b/c=" {
			t.Errorf("signed query was decoded twice: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Referer") == "" {
			t.Error("missing image referer")
		}
		w.Header().Set("Content-Type", "image/heic")
		_, _ = w.Write(fixture)
	}))
	defer up.Close()
	target := up.URL + "/cover.heic?signature=a%2Bb%2Fc%3D"
	proxy := newProxy(t, &fakeResolver{imageURL: target}, t.TempDir())
	server := httptest.NewServer(proxy)
	defer server.Close()
	var first []byte
	for i := 0; i < 2; i++ {
		resp, err := http.Get(server.URL + RouteImage + url.PathEscape(target))
		if err != nil {
			t.Fatal(err)
		}
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Fatalf("cover is not a browser JPEG: %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body.Bytes())
		}
		img, err := jpeg.Decode(bytes.NewReader(body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 534 {
			t.Fatalf("unexpected cover size: %v", img.Bounds())
		}
		if i == 0 {
			first = append([]byte(nil), body.Bytes()...)
		} else if !bytes.Equal(first, body.Bytes()) || resp.Header.Get("X-Cache") != "HIT" {
			t.Fatal("cache did not preserve converted JPEG")
		}
	}
	if hits != 1 {
		t.Fatalf("cache fetched upstream %d times", hits)
	}
}

func TestImageProxyPreservesEncodedURLKey(t *testing.T) {
	resolver := &fakeResolver{imageURL: "http://127.0.0.1:1/not-fetched"}
	proxy := newProxy(t, resolver, "")
	source := "https://cdn.example/cover.heic?x-signature=a%2Bb%2Fc%3D&x-expires=123"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, RouteImage+url.PathEscape(source), nil))
	if resolver.lastKey != source {
		t.Fatalf("changed signed key: %s", resolver.lastKey)
	}
}

func TestPublicCoverCacheSurvivesSignatureRotationWithoutChangingTransforms(t *testing.T) {
	one := "https://p3-reading-sign.fqnovelpic.com/novel-pic/asset~tplv-shrink:1200:0.heic?lk3s=token&x-expires=123&x-signature=a%2Bb%2Fc%3D"
	two := "https://p3-reading-sign.fqnovelpic.com/novel-pic/asset~tplv-shrink:1200:0.heic?x-signature=other&x-expires=456&lk3s=token"
	p := newProxy(t, &fakeResolver{}, t.TempDir())
	a, _ := p.cachePath(one)
	b, _ := p.cachePath(two)
	if a != b {
		t.Fatal("rotating public-cover signature invalidated decoded cache")
	}
	for _, different := range []string{
		"https://p3-reading-sign.fqnovelpic.com/novel-pic/asset~tplv-shrink:600:0.heic?lk3s=token&x-expires=123&x-signature=x",
		one + "&width=200",
	} {
		path, _ := p.cachePath(different)
		if path == a {
			t.Fatal("different image rendering reused wrong cached pixels")
		}
	}
	private := "https://example.com/private/image?x-signature=one&x-expires=123"
	if imageCacheKey(private) != private {
		t.Fatal("non-public image authorization key changed")
	}
}
