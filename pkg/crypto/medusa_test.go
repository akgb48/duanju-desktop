package crypto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestMedusaProtocolInstructionLayersAndEnvelope(t *testing.T) {
	b, e := os.ReadFile("testdata/medusa-vector.json")
	if e != nil {
		t.Fatal(e)
	}
	type fixture struct {
		URL, BodyHex, DeviceID, Value string
		Timestamp                     uint32
		Stages                        []struct{ OutputHex string }
		Randoms                       []uint32
		Ints                          []int
	}
	var f struct {
		fixture
		Additional []fixture
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	body, _ := hex.DecodeString(f.BodyHex)
	entropy := MedusaEntropy{0x11223344, 0x55667788, 0x99aabbcc, 7, 345}
	stages, e := medusaProtos(f.URL, body, f.Timestamp, f.DeviceID, HongguoCryptoProfile(), entropy)
	if e != nil {
		t.Fatal(e)
	}
	for i, v := range stages {
		if hex.EncodeToString(v) != f.Stages[i].OutputHex {
			t.Fatalf("protobuf layer %d differs\ngot %x\nwant %s", i, v, f.Stages[i].OutputHex)
		}
	}
	got, e := BuildMedusaWithEntropy(f.URL, body, f.Timestamp, f.DeviceID, HongguoCryptoProfile(), entropy)
	if e != nil {
		t.Fatal(e)
	}
	if got != f.Value {
		t.Fatalf("Medusa envelope differs\ngot %s\nwant %s", got, f.Value)
	}
	for j, v := range f.Additional {
		body, _ := hex.DecodeString(v.BodyHex)
		entropy := MedusaEntropy{v.Randoms[0], v.Randoms[1], v.Randoms[2], v.Ints[0], v.Ints[1]}
		stages, e := medusaProtos(v.URL, body, v.Timestamp, v.DeviceID, HongguoCryptoProfile(), entropy)
		if e != nil {
			t.Fatal(e)
		}
		for i, p := range stages {
			if hex.EncodeToString(p) != v.Stages[i].OutputHex {
				t.Fatalf("fixture %d layer %d differs", j, i)
			}
		}
		got, e := BuildMedusaWithEntropy(v.URL, body, v.Timestamp, v.DeviceID, HongguoCryptoProfile(), entropy)
		if e != nil {
			t.Fatal(e)
		}
		if got != v.Value {
			t.Errorf("fixture %d envelope differs", j)
		}
	}
}
