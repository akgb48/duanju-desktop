package media

import (
	"context"
	"crypto/sha256"
	"duanju-desktop/pkg/media/cenc"
	"fmt"
	"io"
	"net/http"
	"time"
)

type metadataReader struct {
	prefix []byte
	size   int64
	fetch  func(int64, int) ([]byte, int64, error)
}

func (r *metadataReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > r.size || int64(len(p)) > r.size-off {
		return 0, io.ErrUnexpectedEOF
	}
	if off+int64(len(p)) <= int64(len(r.prefix)) {
		return copy(p, r.prefix[off:]), nil
	}
	b, total, err := r.fetch(off, len(p))
	if err != nil {
		return 0, err
	}
	if total != r.size {
		return 0, fmt.Errorf("media: MP4 在读取元数据时发生变化")
	}
	n := copy(p, b)
	if n != len(p) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}
func (p *Proxy) protectedLayout(ctx context.Context, target, referer string, key []byte) (*cenc.MP4Layout, error) {
	sum := sha256.Sum256(append([]byte(target), key...))
	cacheKey := fmt.Sprintf("%x", sum[:])
	p.layoutMu.Lock()
	if l := p.layouts[cacheKey]; l != nil {
		p.layoutMu.Unlock()
		return l, nil
	}
	p.layoutMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	fetch := func(offset int64, count int) ([]byte, int64, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+int64(count)-1))
		applyMediaHeaders(req, referer)
		res, err := p.client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer res.Body.Close()
		var total int64
		if res.StatusCode == 206 {
			var start, end int64
			if _, err = fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != offset || end < start || end >= total {
				return nil, 0, fmt.Errorf("media: 无效的 MP4 Range 响应")
			}
		} else if res.StatusCode == 200 && offset == 0 && res.ContentLength > 0 {
			total = res.ContentLength
		} else {
			return nil, 0, fmt.Errorf("media: MP4 元数据 HTTP %d，不支持所需 Range", res.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, int64(count)))
		return b, total, err
	}
	prefix, size, err := fetch(0, 512<<10)
	if err != nil {
		return nil, err
	}
	l, err := cenc.ReadMP4Layout(&metadataReader{prefix, size, fetch}, size, key)
	if err != nil {
		return nil, err
	}
	p.layoutMu.Lock()
	defer p.layoutMu.Unlock()
	if p.layouts == nil {
		p.layouts = map[string]*cenc.MP4Layout{}
	}
	if len(p.layouts) >= 24 {
		p.layouts = map[string]*cenc.MP4Layout{}
	}
	p.layouts[cacheKey] = l
	return l, nil
}
func copyProtected(dst io.Writer, src io.Reader, layout *cenc.MP4Layout, offset int64) error {
	buf := make([]byte, 64<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if e := layout.Transform(buf[:n], offset); e != nil {
				return e
			}
			written, e := dst.Write(buf[:n])
			if e != nil {
				return e
			}
			if written != n {
				return io.ErrShortWrite
			}
			offset += int64(n)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
