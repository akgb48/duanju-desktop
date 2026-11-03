package cenc

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestDeriveContentKeyProtocolMachineVectors(t *testing.T) {
	var fixture struct {
		Vectors []struct {
			Input  string
			KeyHex string
		}
	}
	b, err := os.ReadFile("testdata/key-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Vectors) == 0 {
		t.Fatal("missing content-key test vectors")
	}
	for _, v := range fixture.Vectors {
		got, err := DeriveContentKey(v.Input)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != v.KeyHex {
			t.Fatalf("got %x want %s", got, v.KeyHex)
		}
	}
}

func TestDecryptSpadeURLProtocolMachineVectors(t *testing.T) {
	var fixture struct {
		Vectors []struct{ Input, SeedHex, PlainHex string }
	}
	b, err := os.ReadFile("testdata/url-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixture.Vectors {
		seed, err := hex.DecodeString(v.SeedHex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecryptSpadeURL(v.Input, seed)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString([]byte(got)) != v.PlainHex {
			t.Fatalf("got %x want %s", got, v.PlainHex)
		}
	}
}

func TestSeedBase64Normalization(t *testing.T) {
	for _, s := range []string{`"-_8"`, " \t-_8\n", `""-_8""`, "+/8="} {
		got, err := DecodeSeed(s)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte{251, 255}) {
			t.Fatalf("%q -> %x", s, got)
		}
	}
	for _, s := range []string{"", " \n", `""`} {
		got, err := DecodeSeed(s)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty seed: %x %v", got, err)
		}
	}
	if _, err := DecodeSeed("!"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}

func TestDeriveContentKeyRejectsMalformedInput(t *testing.T) {
	for _, b := range [][]byte{nil, {1}, {1, 2}, {255, 0, 0}, make([]byte, 34)} {
		_, err := DeriveContentKey(base64.StdEncoding.EncodeToString(b))
		if err == nil {
			t.Fatalf("accepted invalid key %x", b)
		}
	}
	if _, err := DeriveContentKey("%"); err == nil {
		t.Fatal("invalid encoding accepted")
	}
}

func TestSpadeEnvelopeValidation(t *testing.T) {
	if s, err := DecryptSpadeURL("\t ", nil); err != nil || s != "" {
		t.Fatalf("empty input: %q %v", s, err)
	}
	for _, b := range [][]byte{{1, 2, 3, 4}, {0, 0, 1, 0, 0}, {0xa8, 99, 1, 0, 0}} {
		_, err := DecryptSpadeURL(base64.StdEncoding.EncodeToString(b), nil)
		if !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("invalid envelope %x error=%v", b, err)
		}
	}
}

func TestCleanEscapedURL(t *testing.T) {
	got := CleanEscapedURL(` https:\/\/example.test\/a?x=1\u0026y=2&amp;z=%2F `)
	if got != "https://example.test/a?x=1&y=2&z=%2F" {
		t.Fatal(got)
	}
}

func FuzzSpadeInputs(f *testing.F) {
	for _, s := range []string{"", "!", "AA", "EwATRlQAE0dbARxGVAEcSVUPEkhaDxJHWwATSFoAE0lZDw"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		DeriveContentKey(s)
		DecryptSpadeURL(s, []byte("synthetic-seed"))
	})
}
