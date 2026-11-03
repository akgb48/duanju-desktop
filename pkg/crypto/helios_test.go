package crypto

import (
	"strconv"
	"testing"
)

func TestHeliosMatchesIsolatedProtocolFunction(t *testing.T) {
	for _, v := range loadTTVectors(t).Helios {
		a, err := strconv.ParseInt(v.Args[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		salt, err := strconv.ParseUint(v.Args[1], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		p3, err := strconv.ParseInt(v.Args[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		p4, err := strconv.ParseInt(v.Args[3], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := HeliosWithSalt(int32(a), uint32(salt), p3, p4); got != v.Value {
			t.Errorf("args=%v\ngot  %s\nwant %s", v.Args, got, v.Value)
		}
	}
}
