package crypto

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"strconv"
)

func HeliosWithSalt(a int32, salt uint32, p3, p4 int64) string {
	material := make([]byte, 4, 32)
	binary.LittleEndian.PutUint32(material, salt)
	material = strconv.AppendInt(material, p4, 10)
	digest := md5.Sum(material)
	var ascii [32]byte
	hex.Encode(ascii[:], digest[:])
	words := [4]uint64{}
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(ascii[i*8:])
	}
	var keys [35]uint64
	keys[0] = words[0]
	l := make([]uint64, 3, 37)
	copy(l, words[1:])
	for round := 0; round < 34; round++ {
		v := (bits.RotateLeft64(l[round], -8) + keys[round]) ^ uint64(round)
		l = append(l, v)
		keys[round+1] = bits.RotateLeft64(keys[round], 3) ^ v
	}
	plain := []byte(fmt.Sprintf("%d-%d-%d", a, p3, p4))
	pad := 16 - len(plain)%16
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	out := make([]byte, 4+len(plain))
	binary.LittleEndian.PutUint32(out, salt)
	for offset := 0; offset < len(plain); offset += 16 {
		y := binary.LittleEndian.Uint64(plain[offset:])
		x := binary.LittleEndian.Uint64(plain[offset+8:])
		for round := 0; round < 34; round++ {
			x = (bits.RotateLeft64(x, -8) + y) ^ keys[round]
			y = bits.RotateLeft64(y, 3) ^ x
		}
		binary.LittleEndian.PutUint64(out[4+offset:], y)
		binary.LittleEndian.PutUint64(out[4+offset+8:], x)
	}
	return base64.StdEncoding.EncodeToString(out)
}
