package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type protectedTestResolver struct {
	fakeResolver
	key []byte
}

func (r *protectedTestResolver) ResolveProtectedVideo(ctx context.Context, id, quality string) (string, string, []byte, error) {
	u, ref, err := r.ResolveVideo(ctx, id, quality)
	return u, ref, r.key, err
}
func proxyCENCFixture(t *testing.T) (file, plain, key []byte, offset int) {
	t.Helper()
	box := func(name string, parts ...[]byte) []byte {
		p := bytes.Join(parts, nil)
		b := binary.BigEndian.AppendUint32(nil, uint32(len(p)+8))
		b = append(b, name...)
		return append(b, p...)
	}
	ints := func(v ...uint32) []byte {
		var b []byte
		for _, x := range v {
			b = binary.BigEndian.AppendUint32(b, x)
		}
		return b
	}
	key = []byte("0123456789abcdef")
	iv := []byte("12345678")
	tenc := make([]byte, 24)
	tenc[6] = 1
	tenc[7] = 8
	entry := box("encv", make([]byte, 78), box("sinf", box("frma", []byte("hvc1")), box("schm", ints(0), []byte("cenc"), ints(0x10000)), box("schi", box("tenc", tenc))))
	makeMoov := func(start uint32) []byte {
		stbl := box("stbl", box("stsd", ints(0, 1), entry), box("stsc", ints(0, 1, 1, 1, 1)), box("stsz", ints(0, 64, 1)), box("stco", ints(0, 1, start)), box("senc", ints(0, 1), iv))
		return box("moov", box("trak", box("mdia", box("minf", stbl))))
	}
	moov := makeMoov(0)
	offset = len(moov) + 8
	moov = makeMoov(uint32(offset))
	plain = make([]byte, 64)
	for i := range plain {
		plain[i] = byte(i*13 + 2)
	}
	encrypted := append([]byte(nil), plain...)
	block, _ := aes.NewCipher(key)
	counter := make([]byte, 16)
	copy(counter, iv)
	cipher.NewCTR(block, counter).XORKeyStream(encrypted, encrypted)
	file = append(moov, box("mdat", encrypted)...)
	return
}
func TestProtectedProxyPreservesRangeAndDecryptsAtAbsoluteOffset(t *testing.T) {
	file, plain, key, offset := proxyCENCFixture(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"encrypted"`)
		http.ServeContent(w, r, "movie.mp4", time.Unix(1, 0), bytes.NewReader(file))
	}))
	defer origin.Close()
	res := &protectedTestResolver{fakeResolver: fakeResolver{videoURL: origin.URL}, key: key}
	p := NewProxy("", res)
	req := httptest.NewRequest("GET", "/media/v/720p", nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset+7, offset+30))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 206 || w.Header().Get("Content-Length") != "24" || !bytes.Equal(w.Body.Bytes(), plain[7:31]) {
		t.Fatalf("decrypted Range differs: HTTP %d %q %x", w.Code, w.Header().Get("Content-Range"), w.Body.Bytes())
	}
	if w.Header().Get("ETag") == `"encrypted"` {
		t.Fatal("encrypted representation ETag forwarded for plaintext")
	}
	full := httptest.NewRecorder()
	p.ServeHTTP(full, httptest.NewRequest("GET", "/media/v/720p", nil))
	if full.Code != 200 || !bytes.Equal(full.Body.Bytes()[offset:], plain) || bytes.Contains(full.Body.Bytes()[:offset], []byte("encv")) {
		t.Fatal("full protected response was not decrypted/patched")
	}
}
