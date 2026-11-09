package media

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type mediaByteRange struct {
	start int64
	end   int64 // -1 means the rest of the file.
}

// Only unconditional, single ranges with an explicit start share cached bytes.
func requestedMediaRange(req *http.Request) (mediaByteRange, bool) {
	if req.Method != http.MethodGet || req.Header.Get("If-Range") != "" {
		return mediaByteRange{}, false
	}
	value, ok := strings.CutPrefix(req.Header.Get("Range"), "bytes=")
	if !ok || strings.Contains(value, ",") {
		return mediaByteRange{}, false
	}
	startText, endText, ok := strings.Cut(value, "-")
	if !ok {
		return mediaByteRange{}, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 {
		return mediaByteRange{}, false
	}
	r := mediaByteRange{start: start, end: -1}
	if endText != "" {
		r.end, err = strconv.ParseInt(endText, 10, 64)
		if err != nil || r.end < start {
			return mediaByteRange{}, false
		}
	}
	return r, true
}

func (r mediaByteRange) endAt(total int64) int64 {
	if r.end < 0 || r.end >= total {
		return total - 1
	}
	return r.end
}

func (r mediaByteRange) cachedEnd(start, size, total int64, prefix bool) (int64, bool) {
	end := r.endAt(total)
	if r.start < start || end < r.start || r.start-start >= size || (!prefix && end-start >= size) {
		return 0, false
	}
	return min(end, start+size-1), true
}

func parseMediaContentRange(res *http.Response) (r mediaByteRange, total int64, ok bool) {
	_, err := fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &r.start, &r.end, &total)
	ok = err == nil && r.start >= 0 && r.end >= r.start && r.end < total && r.end-r.start+1 == res.ContentLength
	return
}

func setMediaRangeHeaders(header http.Header, r mediaByteRange, total int64) int64 {
	length := r.end - r.start + 1
	header.Set("Content-Length", strconv.FormatInt(length, 10))
	header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", r.start, r.end, total))
	return length
}

func (e *cachedMediaRange) response(req *http.Request, requested mediaByteRange, prefix bool) *http.Response {
	end, ok := requested.cachedEnd(e.start, int64(len(e.data)), e.total, prefix)
	if !ok {
		return nil
	}
	header := e.header.Clone()
	length := setMediaRangeHeaders(header, mediaByteRange{requested.start, end}, e.total)
	return &http.Response{
		StatusCode: http.StatusPartialContent, Header: header, Request: req,
		ContentLength: length,
		Body:          io.NopCloser(bytes.NewReader(e.data[requested.start-e.start : end-e.start+1])),
	}
}
