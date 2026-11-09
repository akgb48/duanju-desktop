package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProtectedRangesReuseRawHeaderWithoutDoubleDecryption(t *testing.T) {
	file, plain, key, offset := proxyCENCFixture(t)
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.ServeContent(w, r, "v.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer origin.Close()
	p := NewProxy("", &protectedTestResolver{fakeResolver: fakeResolver{videoURL: origin.URL}, key: key})
	for i := 0; i < 4; i++ {
		r := httptest.NewRequest("GET", "/media/v/720p", nil)
		r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset+7, offset+30))
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), plain[7:31]) {
			t.Fatalf("request %d: HTTP %d, wrong plaintext %x", i, w.Code, w.Body.Bytes())
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("header and overlapping media ranges made %d CDN requests, want 1", hits.Load())
	}
}

func TestMediaCacheRangesSeekAndOpenEndedRequests(t *testing.T) {
	file := make([]byte, 5<<20)
	for i := range file {
		file[i] = byte(i*31 + i/256)
	}
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.ServeContent(w, r, "v.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer origin.Close()
	p := NewProxy("", &fakeResolver{videoURL: origin.URL})
	check := func(rg string, start, end int, wantHits int32) {
		t.Helper()
		r := httptest.NewRequest("GET", "/media/v/720p", nil)
		r.Header.Set("Range", rg)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), file[start:end]) || hits.Load() != wantHits {
			t.Fatalf("%s: HTTP %d, length %d, hits %d (want %d)", rg, w.Code, w.Body.Len(), hits.Load(), wantHits)
		}
		if w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end-1, len(file)) {
			t.Fatalf("incorrect range header: %s", w.Header().Get("Content-Range"))
		}
	}
	check("bytes=0-262143", 0, 262144, 1)
	check("bytes=100-1000", 100, 1001, 1)
	check("bytes=4194304-", 4194304, len(file), 2)
	check("bytes=4194310-4194400", 4194310, 4194401, 2)
	check("bytes=0-", 0, len(file), 3) // Cache must not truncate an open-ended stream.
	check("bytes=1500000-1600000", 1500000, 1600001, 3)
	check("bytes=3000000-3100000", 3000000, 3100001, 3) // Later streamed chunks are cached too.
}

func TestMediaCacheBoundsAndRequestIsolation(t *testing.T) {
	var c mediaRangeCache
	for i := 0; i < 80; i++ {
		size := mediaRangeBytes
		if i >= 20 {
			size = 128
		}
		c.put(&cachedMediaRange{url: fmt.Sprintf("https://cdn.example/%d", i), total: int64(size), header: make(http.Header), data: make([]byte, size)})
		if c.bytes > mediaCacheBytes || len(c.entries) > mediaCacheItems {
			t.Fatalf("cache exceeded its budget: %d bytes, %d entries", c.bytes, len(c.entries))
		}
	}
	r := httptest.NewRequest("GET", "https://cdn.example/79", nil)
	r.Header.Set("Range", "bytes=4-7")
	if c.get(r) == nil {
		t.Fatal("recent range was evicted")
	}
	r.Header.Set("If-Range", `"new-file"`)
	if c.get(r) != nil {
		t.Fatal("conditional Range must reach the CDN")
	}
	r.Header.Del("If-Range")
	r.URL.RawQuery = "signature=other"
	if c.get(r) != nil {
		t.Fatal("different signed URL shared cached bytes")
	}
	r.URL.RawQuery = ""
	for _, e := range c.entries {
		e.used = time.Now().Add(-mediaCacheTTL - time.Second)
	}
	if c.get(r) != nil {
		t.Fatal("expired bytes reused")
	}
}

func TestMediaCacheDoesNotCommitTruncatedResponse(t *testing.T) {
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Content-Range", "bytes 0-99/100")
		w.WriteHeader(206)
		n := 100
		if hits.Add(1) == 1 {
			n = 10
		}
		_, _ = w.Write(make([]byte, n))
	}))
	defer origin.Close()
	p := NewProxy("", nil)
	for i := 0; i < 2; i++ {
		r, _ := http.NewRequest("GET", origin.URL, nil)
		r.Header.Set("Range", "bytes=0-99")
		res, err := p.fetchMedia(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if i == 0 && err == nil || i == 1 && (err != nil || len(body) != 100) {
			t.Fatalf("request %d: read %d bytes, err=%v", i, len(body), err)
		}
	}
	if hits.Load() != 2 {
		t.Fatal("partial response entered the cache")
	}
}

func TestMediaCacheStreamsFirstBytesBeforeTail(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "524288")
		_, _ = w.Write(make([]byte, 32768))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer origin.Close()
	defer close(release)
	p := NewProxy("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", origin.URL, nil)
	res, err := p.fetchMedia(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if _, err := io.ReadFull(res.Body, make([]byte, 64)); err != nil {
		t.Fatalf("cache blocked first bytes on the unfinished response: %v", err)
	}
}

func TestProtectedMetadataConcurrentLoadsAndCanceledWaiter(t *testing.T) {
	file, _, key, _ := proxyCENCFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
			<-release
		}
		http.ServeContent(w, r, "v.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer origin.Close()
	p := NewProxy("", nil)
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() {
			_, err := p.protectedLayout(context.Background(), origin.URL, "", key)
			results <- err
		}()
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := p.protectedLayout(ctx, origin.URL, "", key)
	close(release)
	if err != context.DeadlineExceeded {
		t.Fatalf("waiting metadata caller ignored cancellation: %v", err)
	}
	for i := 0; i < 12; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("concurrent metadata load made %d CDN requests", hits.Load())
	}
}
