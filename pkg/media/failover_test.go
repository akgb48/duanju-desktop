package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type candidatesTestResolver struct {
	fakeResolver
	initial, fresh []VideoCandidate
	refreshes      atomic.Int32
}

func (s *candidatesTestResolver) ResolveVideoCandidates(ctx context.Context, id, quality string, refresh bool) ([]VideoCandidate, error) {
	if refresh {
		s.refreshes.Add(1)
		return s.fresh, nil
	}
	return s.initial, nil
}
func TestProtectedMediaFailsOverBeforeSendingHeadersAndRetainsRange(t *testing.T) {
	file, plain, key, offset := proxyCENCFixture(t)
	var badHits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { badHits.Add(1); http.Error(w, "expired signed URL", 403) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "video.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer good.Close()
	res := &candidatesTestResolver{initial: []VideoCandidate{{URL: bad.URL, Key: key}, {URL: good.URL, Key: key}}}
	p := NewProxy("", res)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/media/v/720p", nil)
		r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset+7, offset+30))
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), plain[7:31]) {
			t.Fatalf("backup did not deliver decrypted range: %d %q", w.Code, w.Body.String())
		}
	}
	if badHits.Load() != 1 {
		t.Fatalf("broken primary repeatedly retried: %d", badHits.Load())
	}
	if res.refreshes.Load() != 0 {
		t.Fatal("refreshed model despite healthy backup")
	}
}
func TestAllFailedRoutesRefreshMetadataOnlyOnce(t *testing.T) {
	var oldHits atomic.Int32
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oldHits.Add(1); http.Error(w, "expired", 403) }))
	defer old.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "video.mp4", time.Unix(1, 0), strings.NewReader("abcdef"))
	}))
	defer good.Close()
	res := &candidatesTestResolver{initial: []VideoCandidate{{URL: old.URL}}, fresh: []VideoCandidate{{URL: good.URL}}}
	p := NewProxy("", res)
	r := httptest.NewRequest("GET", "/media/v/720p", nil)
	r.Header.Set("Range", "bytes=2-4")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "cde" || res.refreshes.Load() != 1 || oldHits.Load() != 1 {
		t.Fatalf("refresh/range failed: %d %q", w.Code, w.Body.String())
	}
	res.fresh = res.initial
	res.refreshes.Store(0)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 502 || res.refreshes.Load() != 1 {
		t.Fatal("failure was not bounded")
	}
}

func TestProtectedStreamFailureAfterMetadataUsesBackup(t *testing.T) {
	file, plain, key, offset := proxyCENCFixture(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-524287" {
			http.Error(w, "unavailable", 503)
			return
		}
		http.ServeContent(w, r, "v.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "v.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer good.Close()
	p := NewProxy("", &candidatesTestResolver{initial: []VideoCandidate{{URL: bad.URL, Key: key}, {URL: good.URL, Key: key}}})
	r := httptest.NewRequest("GET", "/media/v/720p", nil)
	r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset+7, offset+30))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), plain[7:31]) {
		t.Fatal("stream-open failure did not switch safely")
	}
}

type mediaTransport func(*http.Request) (*http.Response, error)

func (f mediaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNetworkFailureUsesBackupAndDoesNotExposeSignedURL(t *testing.T) {
	secretURL := "https://unavailable.invalid/video?signature=PRIVATE"
	res := &candidatesTestResolver{initial: []VideoCandidate{{URL: secretURL}, {URL: "https://healthy.invalid/video"}}}
	p := NewProxy("", res)
	p.client = &http.Client{Transport: mediaTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "unavailable.invalid" {
			return nil, fmt.Errorf("DNS lookup failed")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("video"))}, nil
	})}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/media/v/720p", nil))
	if w.Code != 200 || w.Body.String() != "video" {
		t.Fatal("DNS failure did not switch routes")
	}
	res.initial = res.initial[:1]
	res.fresh = res.initial
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/media/v/720p", nil))
	if w.Code != 502 || strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), "https://") {
		t.Fatal("signed URL leaked through player error")
	}
}
func TestCanceledPlaybackDoesNotRefreshOrTryAlternatives(t *testing.T) {
	res := &candidatesTestResolver{initial: []VideoCandidate{{URL: "https://unused.invalid/video"}}}
	p := NewProxy("", res)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/media/v/720p", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if res.refreshes.Load() != 0 {
		t.Fatal("canceled player refreshed addresses")
	}
}
