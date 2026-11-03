package crypto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPrivateHashProtocolInstructionVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/private_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	type vector struct {
		DataHex, ValueHex, KeyHex, DigestHex, SuffixHex, URL, BodyHex string
		Nonce                                                         uint32
		Value                                                         int
	}
	var f struct{ SM3, KeyHash, XMXor, Branch []vector }
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	dec := func(s string) []byte {
		x, e := hex.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return x
	}
	check := func(tag string, got []byte, want string) {
		t.Helper()
		if hex.EncodeToString(got) != want {
			t.Errorf("%s: got %x want %s", tag, got, want)
		}
	}
	for _, v := range f.SM3 {
		check("SM3", SM3(dec(v.DataHex)), v.ValueHex)
	}
	for _, v := range f.KeyHash {
		a, b := KeyHash(dec(v.DataHex), v.Nonce)
		check("KeyHash digest", a, v.DigestHex)
		check("KeyHash suffix", b, v.SuffixHex)
	}
	for _, v := range f.XMXor {
		a, e := XMXor(dec(v.DataHex), dec(v.KeyHex))
		if e != nil {
			t.Fatal(e)
		}
		check("XMXor", a, v.ValueHex)
	}
	for _, v := range f.Branch {
		if got := BranchOf(v.URL, dec(v.BodyHex), v.Nonce); got != v.Value {
			t.Errorf("BranchOf=%d want %d", got, v.Value)
		}
	}
}

func TestPrivateMD5V3ProtocolInstructionVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/hashf13-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		MD5V3 []struct {
			DataHex, PermutationHex, ValueHex string
			IVRotation, ConstantRotation      uint32
		}
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	for _, v := range f.MD5V3 {
		d, _ := hex.DecodeString(v.DataHex)
		p, _ := hex.DecodeString(v.PermutationHex)
		got, e := privateMD5V3(d, v.IVRotation, p, v.ConstantRotation)
		if e != nil {
			t.Fatal(e)
		}
		if hex.EncodeToString(got) != v.ValueHex {
			t.Errorf("rotation %d: got %x want %s", v.IVRotation, got, v.ValueHex)
		}
	}
}

func TestHashF13ProtocolInstructionVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/hashf13-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		HashF13 []struct {
			AHex, BHex, CHex, ValueHex string
			Nonce                      uint32
		}
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	branches := map[int]bool{}
	for i, v := range f.HashF13 {
		a, _ := hex.DecodeString(v.AHex)
		b, _ := hex.DecodeString(v.BHex)
		c, _ := hex.DecodeString(v.CHex)
		branches[int(foldPrivate(foldPrivate(foldPrivate(0x20230928, a), b), c)&15)%3] = true
		got, e := HashF13(a, b, c, v.Nonce)
		if e != nil {
			t.Fatal(e)
		}
		if hex.EncodeToString(got) != v.ValueHex {
			t.Errorf("fixture %d: got %x want %s", i, got, v.ValueHex)
		}
	}
	if len(branches) != 3 {
		t.Fatal("fixtures do not cover all three branches")
	}
}
