package crypto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestAESV3MatchesSampleConstructorAndBlock(t *testing.T) {
	data, err := os.ReadFile("testdata/aesv3-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Vectors []struct {
			Key, Keys, Plain, Cipher string
			Nonce                    uint32
		}
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixtures.Vectors {
		key, _ := hex.DecodeString(v.Key)
		plain, _ := hex.DecodeString(v.Plain)
		a, err := newAESV3(key, v.Nonce)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(a.keys[:]); got != v.Keys {
			t.Fatalf("nonce=%x keys got %s want %s", v.Nonce, got, v.Keys)
		}
		out, err := a.block(plain)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(out); got != v.Cipher {
			t.Errorf("nonce=%x plain=%s got=%s want=%s", v.Nonce, v.Plain, got, v.Cipher)
		}
	}
}

func TestAESV3CarrierMatchesSample(t *testing.T) {
	data, err := os.ReadFile("testdata/aesv3-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Carriers []struct {
			Key, IV, Plain, Cipher string
			Nonce                  uint32
		}
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Carriers) != 12 {
		t.Fatal("missing source carrier vectors")
	}
	for _, v := range fixtures.Carriers {
		key, _ := hex.DecodeString(v.Key)
		plain, _ := hex.DecodeString(v.Plain)
		iv, _ := hex.DecodeString(v.IV)
		a, err := newAESV3(key, v.Nonce)
		if err != nil {
			t.Fatal(err)
		}
		out, err := a.encrypt(plain, iv)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(out); got != v.Cipher {
			t.Errorf("nonce=%x len=%d got=%s want=%s", v.Nonce, len(plain), got, v.Cipher)
		}
	}
}
