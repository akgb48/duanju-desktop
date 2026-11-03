package api

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// FetchMetadata uses the configured transport without forwarding account
// cookies, authorization, device identifiers or signature headers to a CDN.
func (s *Session) FetchMetadata(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("api: 无效的媒体元数据地址")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultHeaders["user-agent"])
	req.Header.Set("Referer", "https://novel.snssdk.com/")
	req.Header.Set("Accept-Encoding", "gzip")
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("api: fallback_api 返回 HTTP %d", res.StatusCode)
	}
	var r io.Reader = res.Body
	if strings.EqualFold(res.Header.Get("Content-Encoding"), "gzip") {
		z, e := gzip.NewReader(r)
		if e != nil {
			return nil, e
		}
		defer z.Close()
		r = z
	}
	const limit = 8 << 20
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && len(b) > limit {
		return nil, fmt.Errorf("api: 媒体元数据超出大小限制")
	}
	return b, err
}
