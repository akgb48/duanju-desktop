package api

import (
	"compress/flate"
	"io"
)

// newFlateReader 包一层 flate，支持 raw deflate（无 zlib 头）。
func newFlateReader(r io.Reader) io.Reader {
	return flate.NewReader(r)
}
