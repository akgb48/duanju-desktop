package cenc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"math/rand"
	"testing"
)

func atom(name string, parts ...[]byte) []byte {
	data := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)+8))
	out = append(out, name...)
	return append(out, data...)
}
func u32(v ...uint32) []byte {
	var out []byte
	for _, n := range v {
		out = binary.BigEndian.AppendUint32(out, n)
	}
	return out
}
func testTrack(audio bool, sizes, offsets []uint32, ivs [][]byte, subs [][][2]int) []byte {
	source, encrypted, skip := "hvc1", "encv", 78
	if audio {
		source, encrypted, skip = "mp4a", "enca", 28
	}
	tenc := make([]byte, 24)
	tenc[6] = 1
	tenc[7] = byte(len(ivs[0]))
	sinf := atom("sinf", atom("frma", []byte(source)), atom("schm", append(u32(0), append([]byte("cenc"), u32(0x10000)...)...)), atom("schi", atom("tenc", tenc)))
	entry := atom(encrypted, make([]byte, skip), sinf)
	stsd := atom("stsd", u32(0, 1), entry)
	stsz := atom("stsz", u32(0, 0, uint32(len(sizes))), u32(sizes...))
	stco := atom("stco", u32(0, uint32(len(offsets))), u32(offsets...))
	stsc := atom("stsc", u32(0, 1, 1, 1, 1))
	flags := uint32(0)
	if subs != nil {
		flags = 2
	}
	senc := u32(flags, uint32(len(sizes)))
	for i, iv := range ivs {
		senc = append(senc, iv...)
		if subs != nil {
			senc = binary.BigEndian.AppendUint16(senc, uint16(len(subs[i])))
			for _, p := range subs[i] {
				senc = binary.BigEndian.AppendUint16(senc, uint16(p[0]))
				senc = binary.BigEndian.AppendUint32(senc, uint32(p[1]))
			}
		}
	}
	stbl := atom("stbl", stsd, stsc, stsz, stco, atom("senc", senc), atom("saiz", u32(0)), atom("saio", u32(0)))
	return atom("trak", atom("mdia", atom("minf", stbl)))
}
func mp4Fixture(t *testing.T) (encrypted, want, key []byte) {
	t.Helper()
	key = []byte("0123456789abcdef")
	ivs := [][]byte{{1, 2, 3, 4, 5, 6, 7, 8}, {8, 7, 6, 5, 4, 3, 2, 1}, []byte("0123456789abcdef")}
	subs := [][][2]int{{{5, 17}, {3, 15}}, {{1, 40}}}
	ftyp := atom("ftyp", []byte("isom0000"))
	makeMoov := func(offset uint32) []byte {
		return atom("moov", testTrack(false, []uint32{40, 41}, []uint32{offset, offset + 40}, ivs[:2], subs), testTrack(true, []uint32{20}, []uint32{offset + 81}, ivs[2:], nil))
	}
	moov := makeMoov(0)
	moov = makeMoov(uint32(len(ftyp) + len(moov) + 8))
	plain := make([]byte, 101)
	for i := range plain {
		plain[i] = byte(i*37 + 11)
	}
	ciphertext := append([]byte(nil), plain...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	for i, offset := range []int{0, 40, 81} {
		var iv [16]byte
		copy(iv[:], ivs[i])
		stream := cipher.NewCTR(block, iv[:])
		if i < 2 {
			p := offset
			for _, pair := range subs[i] {
				p += pair[0]
				stream.XORKeyStream(ciphertext[p:p+pair[1]], plain[p:p+pair[1]])
				p += pair[1]
			}
		} else {
			stream.XORKeyStream(ciphertext[offset:], plain[offset:])
		}
	}
	encrypted = bytes.Join([][]byte{ftyp, moov, atom("mdat", ciphertext)}, nil)
	want = bytes.Join([][]byte{ftyp, moov, atom("mdat", plain)}, nil)
	end := len(ftyp) + len(moov)
	for from, to := range map[string]string{"encv": "hvc1", "enca": "mp4a", "sinf": "free", "senc": "free", "saiz": "free", "saio": "free"} {
		for start := 0; start < end; {
			index := bytes.Index(want[start:end], []byte(from))
			if index < 0 {
				break
			}
			index += start
			copy(want[index:index+4], to)
			start = index + 4
		}
	}
	return
}
func TestMP4CENCTracksSubsamplesAndArbitraryRanges(t *testing.T) {
	encrypted, want, key := mp4Fixture(t)
	layout, err := ReadMP4Layout(bytes.NewReader(encrypted), int64(len(encrypted)), key)
	if err != nil {
		t.Fatal(err)
	}
	if layout.SampleRegions() != 4 {
		t.Fatalf("encrypted region count=%d", layout.SampleRegions())
	}
	full := append([]byte(nil), encrypted...)
	if err = layout.Transform(full, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full, want) {
		t.Fatal("MP4 plaintext or fixed-size header patches differ")
	}
	random := rand.New(rand.NewSource(123))
	for i := 0; i < 200; i++ {
		start := random.Intn(len(encrypted))
		length := 1 + random.Intn(len(encrypted)-start)
		part := append([]byte(nil), encrypted[start:start+length]...)
		if err = layout.Transform(part, int64(start)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(part, want[start:start+length]) {
			t.Fatalf("range %d:%d does not match plaintext", start, start+length)
		}
	}
}
func TestMP4RejectsMalformedAuxiliaryAndSampleBounds(t *testing.T) {
	encrypted, _, key := mp4Fixture(t)
	for _, kind := range []string{"senc", "stco", "stsz"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]byte(nil), encrypted...)
			index := bytes.Index(bad, []byte(kind))
			switch kind {
			case "senc":
				binary.BigEndian.PutUint32(bad[index+8:], 99)
			case "stco":
				binary.BigEndian.PutUint32(bad[index+12:], uint32(len(bad)+100))
			case "stsz":
				binary.BigEndian.PutUint32(bad[index+16:], uint32(len(bad)))
			}
			if _, err := ReadMP4Layout(bytes.NewReader(bad), int64(len(bad)), key); err == nil {
				t.Fatal("malformed encrypted layout accepted")
			}
		})
	}
	if _, err := ReadMP4Layout(bytes.NewReader(encrypted[:20]), int64(len(encrypted)), key); err == nil {
		t.Fatal("truncated metadata accepted")
	}
}
