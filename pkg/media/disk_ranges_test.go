package media

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressiveDiskCacheSurvivesRestartAndRejectsCorruption(t *testing.T) {
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
	dir := t.TempDir()
	read := func(p *Proxy, rg string, want []byte) {
		t.Helper()
		r, _ := http.NewRequest("GET", origin.URL, nil)
		r.Header.Set("Range", rg)
		res, err := p.fetchMedia(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("cached stream: length=%d err=%v", len(data), err)
		}
	}
	p := NewProxy(dir, nil)
	read(p, "bytes=0-", file)
	p.Close() // Drains the bounded async disk queue.
	if hits.Load() != 1 {
		t.Fatalf("initial stream fetched %d times", hits.Load())
	}
	p = NewProxy(dir, nil)
	read(p, "bytes=0-", file)
	read(p, "bytes=3000000-3100000", file[3000000:3100001])
	if hits.Load() != 1 {
		t.Fatalf("restart failed to reuse all cached chunks: hits=%d", hits.Load())
	}
	d := p.ranges.disk
	d.mu.Lock()
	var path string
	for name, info := range d.records {
		if info.Start == 0 {
			path = name
			break
		}
	}
	d.mu.Unlock()
	p.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p = NewProxy(dir, nil)
	defer p.Close()
	read(p, "bytes=100-1000", file[100:1001])
	if hits.Load() != 2 {
		t.Fatal("corrupt persisted bytes were served")
	}
}

func TestWarmedPrefixReturnsBeforeOpeningContinuation(t *testing.T) {
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); http.Error(w, "must not start tail", 500) }))
	defer origin.Close()
	p := NewProxy("", nil)
	defer p.Close()
	p.ranges.put(&cachedMediaRange{url: origin.URL, start: 0, total: 8, header: make(http.Header), data: []byte("warm")})
	req, _ := http.NewRequest("GET", origin.URL, nil)
	req.Header.Set("Range", "bytes=0-")
	res, err := p.fetchMedia(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	dst := make([]byte, 4)
	if _, err = io.ReadFull(res.Body, dst); err != nil || string(dst) != "warm" {
		t.Fatalf("first bytes %q %v", dst, err)
	}
	if hits.Load() != 0 {
		t.Fatal("prefix waited for a new CDN request")
	}
	if _, err = io.ReadAll(res.Body); err == nil {
		t.Fatal("invalid continuation was accepted")
	}
}
