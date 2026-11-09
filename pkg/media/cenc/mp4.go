package cenc

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

type mp4Box struct {
	start, size, header int
	kind                string
}
type patch struct {
	offset int64
	data   []byte
}
type protectedSegment struct {
	offset, size, encryptedOffset int64
	iv                            [16]byte
}

// MP4Layout maps absolute file ranges to per-sample AES-CTR counters. The
// source box and sample sizes are preserved, so arbitrary HTTP Range works.
type MP4Layout struct {
	block    cipher.Block
	patches  []patch
	segments []protectedSegment
	Size     int64
}

func (l *MP4Layout) SampleRegions() int { return len(l.segments) }
func be32(b []byte) uint32              { return binary.BigEndian.Uint32(b) }
func boxes(b []byte, start, end int) ([]mp4Box, error) {
	if start < 0 || end > len(b) || start > end {
		return nil, fmt.Errorf("cenc: MP4 子块边界无效")
	}
	var out []mp4Box
	for p := start; p < end; {
		if end-p < 8 {
			return nil, fmt.Errorf("cenc: MP4 块头被截断")
		}
		n := uint64(be32(b[p:]))
		h := 8
		if n == 1 {
			if end-p < 16 {
				return nil, io.ErrUnexpectedEOF
			}
			n = binary.BigEndian.Uint64(b[p+8:])
			h = 16
		}
		if n == 0 {
			n = uint64(end - p)
		}
		if n < uint64(h) || n > uint64(end-p) {
			return nil, fmt.Errorf("cenc: MP4 块 %q 长度无效", b[p+4:p+8])
		}
		out = append(out, mp4Box{p, int(n), h, string(b[p+4 : p+8])})
		p += int(n)
	}
	return out, nil
}
func child(b []byte, parent mp4Box, kind string) (mp4Box, bool, error) {
	xs, e := boxes(b, parent.start+parent.header, parent.start+parent.size)
	if e != nil {
		return mp4Box{}, false, e
	}
	for _, x := range xs {
		if x.kind == kind {
			return x, true, nil
		}
	}
	return mp4Box{}, false, nil
}
func payload(b []byte, x mp4Box) []byte { return b[x.start+x.header : x.start+x.size] }
func require(b []byte, x mp4Box, n int) ([]byte, error) {
	p := payload(b, x)
	if len(p) < n {
		return nil, io.ErrUnexpectedEOF
	}
	return p, nil
}

// ReadMP4Layout reads only box headers and moov metadata. Fragmented movies,
// CBC schemes and external auxiliary tables are rejected explicitly.
func ReadMP4Layout(r io.ReaderAt, size int64, key []byte) (*MP4Layout, error) {
	if len(key) != 16 {
		return nil, ErrInvalidKey
	}
	if size < 8 {
		return nil, io.ErrUnexpectedEOF
	}
	var moov []byte
	var moovOffset int64
	var media [][2]int64
	for p, visited := int64(0), 0; p+8 <= size; visited++ {
		if visited > 10000 {
			return nil, fmt.Errorf("cenc: MP4 顶层块过多")
		}
		var h [16]byte
		if _, e := r.ReadAt(h[:8], p); e != nil {
			return nil, e
		}
		n := int64(be32(h[:]))
		hs := int64(8)
		kind := string(h[4:8])
		if n == 1 {
			if _, e := r.ReadAt(h[8:], p+8); e != nil {
				return nil, e
			}
			big := binary.BigEndian.Uint64(h[8:])
			if big > uint64(size) {
				return nil, fmt.Errorf("cenc: MP4 大块越界")
			}
			n = int64(big)
			hs = 16
		}
		if n == 0 {
			n = size - p
		}
		if n < hs || n > size-p {
			return nil, fmt.Errorf("cenc: MP4 顶层块越界")
		}
		if kind == "moof" {
			return nil, fmt.Errorf("cenc: 尚未支持 fragmented MP4")
		}
		if kind == "mdat" {
			media = append(media, [2]int64{p + hs, p + n})
		}
		if kind == "moov" {
			if moov != nil {
				return nil, fmt.Errorf("cenc: 多个 moov 不受支持")
			}
			if n > 32<<20 {
				return nil, fmt.Errorf("cenc: moov 超出 32 MiB 限制")
			}
			moov = make([]byte, n)
			if _, e := r.ReadAt(moov, p); e != nil {
				return nil, e
			}
			moovOffset = p
		}
		p += n
	}
	if moov == nil {
		return nil, fmt.Errorf("cenc: MP4 缺少 moov")
	}
	roots, e := boxes(moov, 0, len(moov))
	if e != nil {
		return nil, e
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("cenc: moov 格式无效")
	}
	tracks, e := boxes(moov, roots[0].header, len(moov))
	if e != nil {
		return nil, e
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	l := &MP4Layout{block: block, Size: size}
	for _, track := range tracks {
		if track.kind != "trak" {
			continue
		}
		if e = l.readTrack(moov, track, moovOffset, media); e != nil {
			return nil, e
		}
	}
	if len(l.segments) == 0 {
		return nil, fmt.Errorf("cenc: 未找到受保护的 MP4 sample")
	}
	sort.Slice(l.segments, func(i, j int) bool { return l.segments[i].offset < l.segments[j].offset })
	for i := 1; i < len(l.segments); i++ {
		if l.segments[i].offset < l.segments[i-1].offset+l.segments[i-1].size {
			return nil, fmt.Errorf("cenc: sample 范围重叠")
		}
	}
	return l, nil
}

func (l *MP4Layout) readTrack(b []byte, track mp4Box, base int64, media [][2]int64) error {
	stbl := track
	for _, kind := range []string{"mdia", "minf", "stbl"} {
		x, ok, e := child(b, stbl, kind)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		stbl = x
	}
	stsd, ok, e := child(b, stbl, "stsd")
	if e != nil {
		return e
	}
	if !ok {
		return nil
	}
	if _, e = require(b, stsd, 8); e != nil {
		return e
	}
	entries, e := boxes(b, stsd.start+stsd.header+8, stsd.start+stsd.size)
	if e != nil {
		return e
	}
	ivSize := -1
	var constantIV []byte
	protectedDesc := 0
	for index, entry := range entries {
		if entry.kind != "encv" && entry.kind != "enca" {
			continue
		}
		skip := 78
		if entry.kind == "enca" {
			skip = 28
			p, e := require(b, entry, 28)
			if e != nil {
				return e
			}
			version := binary.BigEndian.Uint16(p[8:10])
			if version != 0 {
				return fmt.Errorf("cenc: 音频 sample entry 版本 %d 不受支持", version)
			}
		}
		xs, e := boxes(b, entry.start+entry.header+skip, entry.start+entry.size)
		if e != nil {
			return e
		}
		var sinf mp4Box
		found := false
		for _, x := range xs {
			if x.kind == "sinf" {
				sinf = x
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("cenc: 加密 sample entry 缺少 sinf")
		}
		frma, ok, e := child(b, sinf, "frma")
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("cenc: sinf 缺少 frma")
		}
		codec, e := require(b, frma, 4)
		if e != nil {
			return e
		}
		schm, ok, e := child(b, sinf, "schm")
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("cenc: sinf 缺少 schm")
		}
		scheme, e := require(b, schm, 8)
		if e != nil {
			return e
		}
		if string(scheme[4:8]) != "cenc" {
			return fmt.Errorf("cenc: 不支持加密方案 %s", scheme[4:8])
		}
		schi, ok, e := child(b, sinf, "schi")
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("cenc: sinf 缺少 schi")
		}
		tenc, ok, e := child(b, schi, "tenc")
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("cenc: 缺少 tenc")
		}
		p, e := require(b, tenc, 24)
		if e != nil {
			return e
		}
		if p[0] > 1 || p[5] != 0 {
			return fmt.Errorf("cenc: 不支持 tenc pattern encryption")
		}
		if p[6] != 1 {
			continue
		}
		if protectedDesc != 0 {
			return fmt.Errorf("cenc: 多个加密 sample description 不受支持")
		}
		protectedDesc = index + 1
		ivSize = int(p[7])
		if ivSize == 0 {
			if len(p) < 25 {
				return io.ErrUnexpectedEOF
			}
			n := int(p[24])
			if (n != 8 && n != 16) || len(p) < 25+n {
				return fmt.Errorf("cenc: 无效 constant IV")
			}
			constantIV = append([]byte(nil), p[25:25+n]...)
		} else if ivSize != 8 && ivSize != 16 {
			return fmt.Errorf("cenc: IV 长度 %d 不受支持", ivSize)
		}
		l.patches = append(l.patches, patch{base + int64(entry.start+4), append([]byte(nil), codec[:4]...)}, patch{base + int64(sinf.start+4), []byte("free")})
	}
	if protectedDesc == 0 {
		return nil
	}
	get := func(kind string) (mp4Box, error) {
		x, ok, e := child(b, stbl, kind)
		if e != nil {
			return x, e
		}
		if !ok {
			return x, fmt.Errorf("cenc: 加密轨道缺少 %s", kind)
		}
		return x, nil
	}
	sizesBox, e := get("stsz")
	if e != nil {
		return e
	}
	p, e := require(b, sizesBox, 12)
	if e != nil {
		return e
	}
	defaultSize, count := be32(p[4:]), be32(p[8:])
	if count > 1000000 {
		return fmt.Errorf("cenc: sample 数量超限")
	}
	sizes := make([]uint32, count)
	if defaultSize != 0 {
		for i := range sizes {
			sizes[i] = defaultSize
		}
	} else {
		if uint64(count)*4 > uint64(len(p)-12) {
			return io.ErrUnexpectedEOF
		}
		for i := range sizes {
			sizes[i] = be32(p[12+i*4:])
		}
	}
	chunksBox, ok, e := child(b, stbl, "stco")
	if e != nil {
		return e
	}
	width := 4
	if !ok {
		chunksBox, e = get("co64")
		if e != nil {
			return e
		}
		width = 8
	}
	p, e = require(b, chunksBox, 8)
	if e != nil {
		return e
	}
	chunks := int(be32(p[4:]))
	if chunks > 1000000 || uint64(chunks)*uint64(width) > uint64(len(p)-8) {
		return fmt.Errorf("cenc: chunk 表越界")
	}
	offsets := make([]int64, chunks)
	for i := range offsets {
		if width == 4 {
			offsets[i] = int64(be32(p[8+i*4:]))
		} else {
			value := binary.BigEndian.Uint64(p[8+i*8:])
			if value > uint64(l.Size) {
				return fmt.Errorf("cenc: co64 越界")
			}
			offsets[i] = int64(value)
		}
	}
	stsc, e := get("stsc")
	if e != nil {
		return e
	}
	p, e = require(b, stsc, 8)
	if e != nil {
		return e
	}
	n := int(be32(p[4:]))
	if n == 0 || uint64(n)*12 > uint64(len(p)-8) {
		return fmt.Errorf("cenc: stsc 表无效")
	}
	type chunkRule struct{ first, samples, description int }
	rules := make([]chunkRule, n)
	for i := range rules {
		q := p[8+i*12:]
		rules[i] = chunkRule{int(be32(q)), int(be32(q[4:])), int(be32(q[8:]))}
		if rules[i].samples <= 0 || rules[i].description != protectedDesc || rules[i].first < 1 || (i == 0 && rules[i].first != 1) || (i > 0 && rules[i].first <= rules[i-1].first) {
			return fmt.Errorf("cenc: stsc 规则无效或混用 sample description")
		}
	}
	sampleOffsets := make([]int64, count)
	sample, rule := 0, 0
	for chunk, offset := range offsets {
		for rule+1 < len(rules) && rules[rule+1].first <= chunk+1 {
			rule++
		}
		for j := 0; j < rules[rule].samples; j++ {
			if sample >= len(sizes) {
				return fmt.Errorf("cenc: stsc sample 数量超过 stsz")
			}
			end := offset + int64(sizes[sample])
			inside := false
			for _, m := range media {
				if offset >= m[0] && end <= m[1] {
					inside = true
					break
				}
			}
			if !inside {
				return fmt.Errorf("cenc: sample 未处于 mdat 范围")
			}
			sampleOffsets[sample] = offset
			offset = end
			sample++
		}
	}
	if sample != len(sizes) {
		return fmt.Errorf("cenc: stsc sample 数量少于 stsz")
	}
	senc, e := get("senc")
	if e != nil {
		return fmt.Errorf("cenc: 暂不支持缺少 senc 的外部辅助加密表")
	}
	p, e = require(b, senc, 8)
	if e != nil {
		return e
	}
	flags := be32(p[:4]) & 0xffffff
	position := 4
	if flags&1 != 0 {
		if len(p) < 24 {
			return io.ErrUnexpectedEOF
		}
		if uint32(p[4])<<16|uint32(p[5])<<8|uint32(p[6]) != 1 {
			return fmt.Errorf("cenc: senc override 算法不受支持")
		}
		ivSize = int(p[7])
		if ivSize != 8 && ivSize != 16 {
			return fmt.Errorf("cenc: senc IV 长度无效")
		}
		position += 20
	}
	if position+4 > len(p) {
		return io.ErrUnexpectedEOF
	}
	if be32(p[position:]) != count {
		return fmt.Errorf("cenc: senc sample 数量与 stsz 不一致")
	}
	position += 4
	for i, offset := range sampleOffsets {
		var iv [16]byte
		if ivSize == 0 {
			copy(iv[:], constantIV)
		} else {
			if position+ivSize > len(p) {
				return io.ErrUnexpectedEOF
			}
			copy(iv[:], p[position:position+ivSize])
			position += ivSize
		}
		add := func(start, length, encryptedOffset int64) {
			if length > 0 {
				l.segments = append(l.segments, protectedSegment{start, length, encryptedOffset, iv})
			}
		}
		if flags&2 == 0 {
			add(offset, int64(sizes[i]), 0)
			continue
		}
		if position+2 > len(p) {
			return io.ErrUnexpectedEOF
		}
		pairs := int(binary.BigEndian.Uint16(p[position:]))
		position += 2
		if pairs == 0 {
			add(offset, int64(sizes[i]), 0)
			continue
		}
		used, encrypted := int64(0), int64(0)
		for j := 0; j < pairs; j++ {
			if position+6 > len(p) {
				return io.ErrUnexpectedEOF
			}
			clear, ciphered := int64(binary.BigEndian.Uint16(p[position:])), int64(be32(p[position+2:]))
			position += 6
			used += clear
			if clear+ciphered > int64(sizes[i]) || used+ciphered > int64(sizes[i]) {
				return fmt.Errorf("cenc: subsample 越界")
			}
			add(offset+used, ciphered, encrypted)
			used += ciphered
			encrypted += ciphered
		}
		if used != int64(sizes[i]) {
			return fmt.Errorf("cenc: subsample 总长度不匹配")
		}
	}
	if position != len(p) {
		return fmt.Errorf("cenc: senc 含未解析的尾部数据")
	}
	l.patches = append(l.patches, patch{base + int64(senc.start+4), []byte("free")})
	for _, kind := range []string{"saiz", "saio"} {
		if x, ok, e := child(b, stbl, kind); e != nil {
			return e
		} else if ok {
			l.patches = append(l.patches, patch{base + int64(x.start+4), []byte("free")})
		}
	}
	return nil
}

// Transform decrypts a file range in place, including partial samples and
// ranges starting in the middle of an AES block or a metadata patch.
func (l *MP4Layout) Transform(data []byte, offset int64) error {
	if offset < 0 || int64(len(data)) > l.Size-offset {
		return fmt.Errorf("cenc: 请求范围越界")
	}
	end := offset + int64(len(data))
	index := sort.Search(len(l.segments), func(i int) bool { return l.segments[i].offset+l.segments[i].size > offset })
	for ; index < len(l.segments); index++ {
		s := l.segments[index]
		if s.offset >= end {
			break
		}
		start, stop := s.offset, s.offset+s.size
		if start < offset {
			start = offset
		}
		if stop > end {
			stop = end
		}
		part := data[start-offset : stop-offset]
		decryptCTR(part, part, l.block, s.iv[:], s.encryptedOffset+start-s.offset)
	}
	for _, p := range l.patches {
		start, stop := p.offset, p.offset+int64(len(p.data))
		if start < offset {
			start = offset
		}
		if stop > end {
			stop = end
		}
		if start < stop {
			copy(data[start-offset:stop-offset], p.data[start-p.offset:stop-p.offset])
		}
	}
	return nil
}
