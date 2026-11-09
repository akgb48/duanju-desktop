package media

import (
	"context"
	"io"
	"net/http"
)

// WarmVideo fills only the first bounded range and the protected MP4 metadata.
// It shares raw bytes with real playback without buffering a whole movie.
func (p *Proxy) WarmVideo(ctx context.Context, videoID, quality string) error {
	p.mu.RLock()
	res := p.resolver
	p.mu.RUnlock()
	if res == nil {
		return ErrNoResolver
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL(videoID, quality), nil)
	if err != nil {
		return err
	}
	r.Header.Set("Range", "bytes=0-2097151")
	opened, err := p.openMedia(r, res, videoID, quality)
	if err != nil {
		return err
	}
	defer opened.response.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(opened.response.Body, mediaRangeBytes))
	return err
}
