package crypto

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type ttHeliosVectors struct {
	Helios []struct {
		Args  []string `json:"args"`
		Value string   `json:"value"`
	} `json:"helios"`
	TT []struct {
		Data     string `json:"dataHex"`
		Stream   string `json:"keyStreamHex"`
		Derived  string `json:"derivedHex"`
		Envelope string `json:"envelopeHex"`
	} `json:"tt"`
}

func loadTTVectors(t *testing.T) ttHeliosVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/tt_helios_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v ttHeliosVectors
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.TT) == 0 || len(v.Helios) == 0 {
		t.Fatal("missing encryption test vectors")
	}
	return v
}

func TestTTEncryptMatchesIsolatedProtocolFunction(t *testing.T) {
	for _, v := range loadTTVectors(t).TT {
		data, _ := hex.DecodeString(v.Data)
		ks, _ := hex.DecodeString(v.Stream)
		got, err := TTEncryptCompressedV5WithKeyStream(data, ks)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != v.Envelope {
			t.Fatalf("source ciphertext mismatch for %d bytes\ngot %x\nwant %s", len(data), got, v.Envelope)
		}
		key, iv, err := DeriveTTKeyIV(ks)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(key[:])+hex.EncodeToString(iv[:]) != v.Derived[:64] {
			t.Fatal("source key/IV mismatch")
		}
	}
}

func TestTTEncryptDecryptContainer(t *testing.T) {
	for _, plain := range [][]byte{nil, []byte(`{"magic_tag":"ss_app_log","header":{"aid":8662,"device_id":"9007199254740993123"}}`), bytes.Repeat([]byte("压缩容器内容"), 2048)} {
		encoded, err := TTEncryptV5WithKeyStream(plain, bytes.Repeat([]byte{0x5a}, 32))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded[:6], []byte{'t', 'c', 5, 16, 0, 0}) {
			t.Fatal("missing sample magic")
		}
		decoded, err := TTDecryptV5(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded, plain) {
			t.Fatal("container mismatch")
		}
		tampered := append([]byte(nil), encoded...)
		tampered[38] ^= 1
		if _, err = TTDecryptV5(tampered); err == nil {
			t.Fatal("corrupt SHA512 prefix accepted")
		}
		if _, err = TTDecryptV5(encoded[:len(encoded)-1]); err == nil {
			t.Fatal("partial block accepted")
		}
	}
}

func TestTTRejectsInvalidKeyStreamAndEnvelope(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := TTEncryptV5WithKeyStream([]byte("{}"), make([]byte, n)); err == nil {
			t.Fatalf("accepted %d-byte keyStream", n)
		}
	}
	for _, v := range [][]byte{nil, make([]byte, 54), append([]byte("tc\x05\x10\x00\x00"), make([]byte, 32)...)} {
		if _, err := TTDecryptV5(v); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}
