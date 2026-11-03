package media

import (
	"context"
	"duanju-desktop/pkg/media/cenc"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// VideoCandidate keeps a CDN URL and its content key together. Alternate URLs
// must describe the same definition/file; never append another file mid-stream.
type VideoCandidate struct {
	URL, Referer string
	Key          []byte
}
type VideoCandidatesResolver interface {
	ResolveVideoCandidates(context.Context, string, string, bool) ([]VideoCandidate, error)
}
type openedMedia struct {
	target   string
	key      []byte
	layout   *cenc.MP4Layout
	response *http.Response
}

func (p *Proxy) tryMedia(r *http.Request, c VideoCandidate, strict bool) (*openedMedia, error) {
	if c.URL == "" {
		return nil, errors.New("播放地址为空")
	}
	var layout *cenc.MP4Layout
	var err error
	if len(c.Key) > 0 && r.Method != http.MethodHead {
		layout, err = p.protectedLayout(r.Context(), c.URL, c.Referer, c.Key)
		if err != nil {
			return nil, fmt.Errorf("读取视频信息失败: %w", networkCause(err))
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, c.URL, nil)
	if err != nil {
		return nil, errors.New("播放地址无效")
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	if ir := r.Header.Get("If-Range"); ir != "" && len(c.Key) == 0 {
		req.Header.Set("If-Range", ir)
	}
	applyMediaHeaders(req, c.Referer)
	res, err := p.client.Do(req)
	if err != nil {
		return nil, networkCause(err)
	}
	if strict && res.StatusCode >= 400 && res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		res.Body.Close()
		return nil, fmt.Errorf("线路返回 HTTP %d", res.StatusCode)
	}
	return &openedMedia{target: c.URL, key: c.Key, layout: layout, response: res}, nil
}

// net/url embeds the complete signed URL in errors. Expose the cause only.
func networkCause(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}
func (p *Proxy) openMedia(r *http.Request, res Resolver, videoID, quality string) (*openedMedia, error) {
	multi, ok := res.(VideoCandidatesResolver)
	if !ok {
		var c VideoCandidate
		var err error
		if protected, yes := res.(ProtectedResolver); yes {
			c.URL, c.Referer, c.Key, err = protected.ResolveProtectedVideo(r.Context(), videoID, quality)
		} else {
			c.URL, c.Referer, err = res.ResolveVideo(r.Context(), videoID, quality)
		}
		if err != nil {
			return nil, err
		}
		return p.tryMedia(r, c, false)
	}
	tried := map[string]bool{}
	var last error
	// One initial list and at most one forced metadata refresh, before writing
	// any bytes/headers to the player. Request cancellation stops every attempt.
	for pass := 0; pass < 2; pass++ {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		list, err := multi.ResolveVideoCandidates(r.Context(), videoID, quality, pass == 1)
		if err != nil {
			last = err
			continue
		}
		// Prefer URLs that have not just failed, but retry cooled-down entries
		// when no new address remains after the metadata refresh.
		ordered := make([]VideoCandidate, 0, len(list))
		delayed := []VideoCandidate{}
		for _, c := range list {
			if p.urlCoolingDown(c.URL) {
				delayed = append(delayed, c)
			} else {
				ordered = append(ordered, c)
			}
		}
		ordered = append(ordered, delayed...)
		for _, c := range ordered {
			if tried[c.URL] {
				continue
			}
			tried[c.URL] = true
			if err = r.Context().Err(); err != nil {
				return nil, err
			}
			opened, e := p.tryMedia(r, c, true)
			if e == nil {
				p.markURL(c.URL, false)
				return opened, nil
			}
			last = e
			if err = r.Context().Err(); err != nil {
				return nil, err
			}
			p.markURL(c.URL, true)
		}
	}
	if last == nil {
		last = errors.New("没有可用播放地址")
	}
	return nil, fmt.Errorf("视频线路暂不可用，已尝试备用线路并刷新地址: %w", networkCause(last))
}
func (p *Proxy) urlCoolingDown(target string) bool {
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	return p.now().Before(p.failedURLs[target])
}
func (p *Proxy) markURL(target string, failed bool) {
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	if !failed {
		delete(p.failedURLs, target)
		return
	}
	if p.failedURLs == nil || len(p.failedURLs) > 512 {
		p.failedURLs = map[string]time.Time{}
	}
	p.failedURLs[target] = p.now().Add(45 * time.Second)
}
