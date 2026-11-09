package media

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCachedRangeContractAcrossMemoryAndDisk(t *testing.T) {
	for _, layer := range []string{"memory", "disk"} {
		t.Run(layer, func(t *testing.T) {
			entry := &cachedMediaRange{
				url: "https://cdn.example/video", total: 10,
				header: make(http.Header), data: []byte("0123456789"),
			}
			var cache mediaRangeCache
			if layer == "disk" {
				dir := t.TempDir()
				disk := newDiskRangeCache(dir)
				disk.enqueue(entry)
				disk.close()
				cache.disk = newDiskRangeCache(dir)
				t.Cleanup(cache.disk.close)
			} else {
				cache.put(entry)
			}
			for _, tc := range []struct {
				name, method, requestRange, ifRange, contentRange, body string
			}{
				{name: "single-byte", requestRange: "bytes=0-0", contentRange: "bytes 0-0/10", body: "0"},
				{name: "open-ended", requestRange: "bytes=8-", contentRange: "bytes 8-9/10", body: "89"},
				{name: "clamped-end", requestRange: "bytes=8-99", contentRange: "bytes 8-9/10", body: "89"},
				{name: "suffix", requestRange: "bytes=-2"},
				{name: "multiple", requestRange: "bytes=0-1,4-5"},
				{name: "reversed", requestRange: "bytes=9-1"},
				{name: "past-end", requestRange: "bytes=10-"},
				{name: "wrong-unit", requestRange: "items=0-1"},
				{name: "missing-range"},
				{name: "head", method: http.MethodHead, requestRange: "bytes=0-1"},
				{name: "conditional", requestRange: "bytes=0-1", ifRange: `"new-file"`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					method := tc.method
					if method == "" {
						method = http.MethodGet
					}
					req := httptest.NewRequest(method, entry.url, nil)
					req.Header.Set("Range", tc.requestRange)
					req.Header.Set("If-Range", tc.ifRange)
					res := cache.get(req)
					if tc.contentRange == "" {
						if res != nil {
							res.Body.Close()
							t.Fatal("request that requires the CDN was served from cache")
						}
						return
					}
					if res == nil {
						t.Fatal("valid cached range was missed")
					}
					defer res.Body.Close()
					body, err := io.ReadAll(res.Body)
					if err != nil || res.StatusCode != 206 || res.Header.Get("Content-Range") != tc.contentRange || res.ContentLength != int64(len(body)) || string(body) != tc.body {
						t.Fatalf("cached response: status=%d range=%q body=%q err=%v", res.StatusCode, res.Header.Get("Content-Range"), body, err)
					}
				})
			}
		})
	}
}
