package media

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	mediaRangeBytes = 2 << 20
	mediaCacheBytes = 32 << 20
	mediaCacheItems = 64
	mediaCacheTTL   = 2 * time.Minute
)

// Cache encrypted CDN bytes, before any per-request MP4 transform. Native
// audio/video and the WASM worker can reuse overlapping header/sample ranges.
// The URL includes its signature; another CDN file never shares an entry.
type cachedMediaRange struct {
	url, referer string
	start, total int64
	header       http.Header
	data         []byte
	used         time.Time
}

type mediaRangeCache struct {
	mu      sync.Mutex
	entries []*cachedMediaRange
	bytes   int
	disk    *diskRangeCache
}

func (c *mediaRangeCache) get(req *http.Request) *http.Response {
	return c.lookup(req, false)
}

func (c *mediaRangeCache) lookup(req *http.Request, prefix bool) *http.Response {
	requested, ok := requestedMediaRange(req)
	if !ok {
		return nil
	}
	c.mu.Lock()
	now := time.Now()
	for _, e := range c.entries {
		if e.url != req.URL.String() || e.referer != req.Header.Get("Referer") || now.Sub(e.used) > mediaCacheTTL {
			continue
		}
		if res := e.response(req, requested, prefix); res != nil {
			e.used = now
			c.mu.Unlock()
			return res
		}
	}
	disk := c.disk
	c.mu.Unlock()
	if disk != nil {
		return disk.lookup(req, requested, prefix)
	}
	return nil
}

func (c *mediaRangeCache) put(e *cachedMediaRange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	kept := c.entries[:0]
	for _, old := range c.entries {
		if old.url == e.url && old.referer == e.referer && old.start == e.start && old.total == e.total && len(old.data) >= len(e.data) && now.Sub(old.used) <= mediaCacheTTL {
			return
		}
	}
	for _, old := range c.entries {
		if now.Sub(old.used) > mediaCacheTTL || (old.url == e.url && old.referer == e.referer && old.start == e.start) {
			c.bytes -= len(old.data)
		} else {
			kept = append(kept, old)
		}
	}
	clear(c.entries[len(kept):])
	c.entries = kept
	for len(c.entries) > 0 && (len(c.entries) >= mediaCacheItems || c.bytes+len(e.data) > mediaCacheBytes) {
		oldest := 0
		for i := range c.entries {
			if c.entries[i].used.Before(c.entries[oldest].used) {
				oldest = i
			}
		}
		c.bytes -= len(c.entries[oldest].data)
		copy(c.entries[oldest:], c.entries[oldest+1:])
		c.entries[len(c.entries)-1] = nil
		c.entries = c.entries[:len(c.entries)-1]
	}
	e.used = now
	c.entries = append(c.entries, e)
	c.bytes += len(e.data)
	if c.disk != nil {
		c.disk.enqueue(e)
	}
}

// Record bytes as they are consumed; never read ahead or buffer an entire film
// before returning headers/first bytes to the player.
type mediaCacheBody struct {
	io.ReadCloser
	cache *mediaRangeCache
	entry *cachedMediaRange
	limit int
	end   int64
}

func (b *mediaCacheBody) Read(dst []byte) (int, error) {
	n, err := b.ReadCloser.Read(dst)
	for offset := 0; b.entry != nil && offset < n; {
		e := b.entry
		count := min(n-offset, b.limit-len(e.data))
		e.data = append(e.data, dst[offset:offset+count]...)
		offset += count
		if len(e.data) == b.limit {
			b.cache.put(e)
			b.entry = nil
			start := e.start + int64(len(e.data))
			if start <= b.end {
				b.limit = int(min(int64(mediaRangeBytes), b.end-start+1))
				b.entry = &cachedMediaRange{url: e.url, referer: e.referer, start: start, total: e.total, header: e.header, data: make([]byte, 0, b.limit)}
			}
		}
	}
	if err != nil {
		b.entry = nil
	}
	return n, err
}

func (p *Proxy) fetchMedia(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if res := p.ranges.get(req); res != nil {
		return res, nil
	}
	if prefix := p.ranges.lookup(req, true); prefix != nil {
		cached, total, _ := parseMediaContentRange(prefix)
		requested, _ := requestedMediaRange(req)
		requested.end = requested.endAt(total)
		prefix.ContentLength = setMediaRangeHeaders(prefix.Header, requested, total)
		prefix.Body = &continuedMediaBody{prefix: prefix.Body, proxy: p, request: req, next: cached.end + 1, end: requested.end, total: total, header: prefix.Header.Clone()}
		return prefix, nil
	}
	res, err := p.client.Do(req)
	if err != nil || req.Method != http.MethodGet || req.Header.Get("If-Range") != "" {
		return res, err
	}
	if encoding := res.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return res, nil
	}
	start, end, total := int64(0), res.ContentLength-1, res.ContentLength
	if res.StatusCode == http.StatusPartialContent {
		r, size, ok := parseMediaContentRange(res)
		if !ok {
			return res, nil
		}
		start, end, total = r.start, r.end, size
	} else if res.StatusCode != http.StatusOK {
		return res, nil
	}
	if total <= 0 || end < start {
		return res, nil
	}
	limit := int(min(int64(mediaRangeBytes), end-start+1))
	entry := &cachedMediaRange{url: req.URL.String(), referer: req.Header.Get("Referer"), start: start, total: total, header: res.Header.Clone(), data: make([]byte, 0, limit)}
	res.Body = &mediaCacheBody{ReadCloser: res.Body, cache: &p.ranges, entry: entry, limit: limit, end: end}
	return res, nil
}

// Return warmed bytes immediately. Only open the continuation when the player
// consumes the prefix, preserving cancellation and avoiding a second prefix fetch.
// Continue the same signed URL and require the identical file size/validator.
type continuedMediaBody struct {
	prefix, tail     io.ReadCloser
	proxy            *Proxy
	request          *http.Request
	header           http.Header
	next, end, total int64
	closed           bool
}

func (b *continuedMediaBody) Read(dst []byte) (int, error) {
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	if err := b.request.Context().Err(); err != nil {
		return 0, err
	}
	if b.prefix != nil {
		n, err := b.prefix.Read(dst)
		if err != io.EOF {
			return n, err
		}
		b.prefix.Close()
		b.prefix = nil
		if n > 0 {
			return n, nil
		}
	}
	if b.next > b.end {
		return 0, io.EOF
	}
	if b.tail == nil {
		req := b.request.Clone(b.request.Context())
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", b.next, b.end))
		res, err := b.proxy.fetchMedia(req)
		if err != nil {
			return 0, err
		}
		r, total, ok := parseMediaContentRange(res)
		if !ok || res.StatusCode != http.StatusPartialContent || r.start != b.next || r.end != b.end || total != b.total {
			res.Body.Close()
			b.proxy.markURL(req.URL.String(), true)
			return 0, errors.New("视频缓存续传范围发生变化")
		}
		for _, key := range []string{"ETag", "Last-Modified"} {
			if original := b.header.Get(key); original != "" && res.Header.Get(key) != original {
				res.Body.Close()
				return 0, errors.New("视频缓存续传文件发生变化")
			}
		}
		b.tail = res.Body
	}
	n, err := b.tail.Read(dst)
	b.next += int64(n)
	if err != nil && err != io.EOF {
		b.proxy.markURL(b.request.URL.String(), true)
	}
	if err == io.EOF && b.next <= b.end {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
func (b *continuedMediaBody) Close() error {
	b.closed = true
	if b.prefix != nil {
		b.prefix.Close()
	}
	if b.tail != nil {
		return b.tail.Close()
	}
	return nil
}
