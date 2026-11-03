package cenc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

func TestCTRNISTVectorAndUnalignedOffsets(t *testing.T) {
	// NIST SP 800-38A F.5.1; also used by Go's crypto/cipher test vectors.
	key := mustHex("2b7e151628aed2a6abf7158809cf4f3c")
	iv := mustHex("f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff")
	pt := mustHex("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710")
	ct := mustHex("874d6191b620e3261bef6864990db6ce9806f66b7970fdff8617187bb9fffdff5ae4df3edbd5d35e5b4f09020db03eab1e031dda2fbe03d1792170a0f3009cee")
	for _, offset := range []int{0, 1, 15, 16, 17, 31, 48, 63} {
		got := make([]byte, len(ct)-offset)
		if err := DecryptCTRSample(got, ct[offset:], key, iv, int64(offset)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt[offset:]) {
			t.Fatalf("offset %d got %x", offset, got)
		}
	}
}

func TestCTRCounterCarryAndInPlace(t *testing.T) {
	key := make([]byte, 16)
	iv := mustHex("0000000000000000ffffffffffffffff")
	source := bytes.Repeat([]byte{0xa5}, 67)
	block, _ := aes.NewCipher(key)
	want := make([]byte, len(source))
	cipher.NewCTR(block, iv).XORKeyStream(want, source)
	got := append([]byte(nil), source[17:]...)
	if err := DecryptCTRSample(got, got, key, iv, 17); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want[17:]) {
		t.Fatalf("counter carry mismatch: %x", got)
	}
}

func TestCTRRejectsInvalidArguments(t *testing.T) {
	for _, v := range []struct {
		dst, src, key, iv []byte
		offset            int64
	}{
		{make([]byte, 1), []byte{1}, nil, make([]byte, 16), 0},
		{make([]byte, 1), []byte{1}, make([]byte, 16), nil, 0},
		{nil, []byte{1}, make([]byte, 16), make([]byte, 16), 0},
		{make([]byte, 1), []byte{1}, make([]byte, 16), make([]byte, 16), -1},
	} {
		if err := DecryptCTRSample(v.dst, v.src, v.key, v.iv, v.offset); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	if err := DecryptCTRSample(nil, nil, nil, nil, -1); err != nil {
		t.Fatal("empty sample must be a no-op")
	}
}
