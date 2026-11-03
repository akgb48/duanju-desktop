package crypto

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math/bits"
	"strings"
)

// SM3 implements the standard 256-bit SM3 compression used by this sample.
func SM3(data []byte) []byte {
	state := [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}
	message := append([]byte(nil), data...)
	message = append(message, 0x80)
	for len(message)%64 != 56 {
		message = append(message, 0)
	}
	message = binary.BigEndian.AppendUint64(message, uint64(len(data))*8)
	for offset := 0; offset < len(message); offset += 64 {
		var w [68]uint32
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(message[offset+i*4:])
		}
		for i := 16; i < 68; i++ {
			x := w[i-16] ^ w[i-9] ^ bits.RotateLeft32(w[i-3], 15)
			w[i] = x ^ bits.RotateLeft32(x, 15) ^ bits.RotateLeft32(x, 23) ^ bits.RotateLeft32(w[i-13], 7) ^ w[i-6]
		}
		a, b, c, d, e, f, g, h := state[0], state[1], state[2], state[3], state[4], state[5], state[6], state[7]
		for i := 0; i < 64; i++ {
			t := uint32(0x79cc4519)
			ff := a ^ b ^ c
			gg := e ^ f ^ g
			if i >= 16 {
				t = 0x7a879d8a
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}
			s1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(t, i), 7)
			s2 := s1 ^ bits.RotateLeft32(a, 12)
			t1 := ff + d + s2 + (w[i] ^ w[i+4])
			t2 := gg + h + s1 + w[i]
			d, c, b, a = c, bits.RotateLeft32(b, 9), a, t1
			h, g, f, e = g, bits.RotateLeft32(f, 19), e, t2^bits.RotateLeft32(t2, 9)^bits.RotateLeft32(t2, 17)
		}
		for i, v := range [8]uint32{a, b, c, d, e, f, g, h} {
			state[i] ^= v
		}
	}
	out := make([]byte, 32)
	for i, v := range state {
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
	return out
}

func foldPrivate(r uint32, data []byte) uint32 {
	for i, value := range data {
		if i&1 == 0 {
			r = (r >> 4) ^ r ^ (r << 6) ^ uint32(value)
		} else {
			r = ^((r >> 7) ^ r ^ ((r << 12) | uint32(value)))
		}
	}
	return r
}

func sumMD5(data []byte) uint32 {
	r := uint32(0x20220420)
	for i, v := range data {
		if i >= 12 {
			break
		}
		old := r
		if i&1 == 0 {
			r = (r >> 3) ^ r ^ ((old << 7) ^ uint32(v))
		} else {
			r = (r >> 5) ^ r ^ ((old << 11) | uint32(v)) ^ 0xffffffff
		}
	}
	return (r | 4) ^ 0x01000000
}

func privateMD5V3(data []byte, ivRotation uint32, permutation []byte, constantRotation uint32) ([]byte, error) {
	if len(data) < 64 || len(permutation) < 64 {
		return nil, fmt.Errorf("md5V3: 需要 64 字节输入及置换表")
	}
	initial := [4]uint32{0x79e0f2fb, 0xc8b52570, 0xebc2f8cd, 0x7c104d93}
	extra := [4]uint32{0x19be4866, 0xe85986b4, 0xe19b326e, 0x71d1d7d4}
	var state [4]uint32
	for i := range initial {
		initial[i] = bits.RotateLeft32(initial[i], -int(ivRotation&31))
		extra[i] = bits.RotateLeft32(extra[i], -int((ivRotation+6)&31))
		state[i] = initial[i]
	}
	var words [16]uint32
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(data[i*4:])
	}
	shifts := [4][4]int{{7, 12, 17, 22}, {5, 9, 14, 20}, {4, 11, 16, 23}, {6, 10, 15, 21}}
	for i := 0; i < 64; i++ {
		a := (4 - (i & 3)) & 3
		b := (a + 1) & 3
		c := (a + 2) & 3
		d := (a + 3) & 3
		var f uint32
		switch i / 16 {
		case 0:
			f = (state[b] & state[c]) | (^state[b] & state[d])
		case 1:
			f = (state[b] & state[d]) | (state[c] &^ state[d])
		case 2:
			f = state[b] ^ state[c] ^ state[d]
		case 3:
			f = state[c] ^ (state[b] | ^state[d])
		}
		index := int(permutation[i])
		if index >= 16 {
			return nil, fmt.Errorf("md5V3: 无效的置换下标 %d", index)
		}
		k := bits.RotateLeft32(privateMD5Constants[i], -int(constantRotation&31))
		state[a] = state[b] + bits.RotateLeft32(state[a]+f+words[index]+k, shifts[i/16][i&3])
	}
	out := make([]byte, 16, 20)
	for i := range state {
		binary.LittleEndian.PutUint32(out[i*4:], (initial[i]+state[i])^extra[i])
	}
	return binary.LittleEndian.AppendUint32(out, sumMD5(out)), nil
}

// HashF13 is the three-branch private digest. Its selector folds all three
// inputs; the nonce affects branch parameters, not the branch selection.
func HashF13(a, b, c []byte, nonce uint32) ([]byte, error) {
	folded := foldPrivate(foldPrivate(foldPrivate(0x20230928, a), b), c)
	switch (folded & 15) % 3 {
	case 1:
		if len(a) < 16 {
			return nil, fmt.Errorf("hashF13: 第一段输入至少需要 16 字节")
		}
		out := append(append([]byte(nil), a[:16]...), b...)
		return binary.LittleEndian.AppendUint32(out, sumMD5(append(append([]byte(nil), a...), b...))), nil
	case 2:
		index := int(((folded & 13) * 86) >> 8)
		constants := [5]uint32{0x8980f29b, 0xeb549c7f, 0xb08726db, 0xd40cb5e6, 0xe8f559e4}
		seed := nonce + constants[index]
		salt := binary.LittleEndian.AppendUint64(nil, 0xf80b15d49d779684)
		for i := range salt {
			salt[i] = bits.RotateLeft8(salt[i], -int((seed+5)&7))
		}
		data := append(append(append(append([]byte(nil), a...), b...), c...), salt...)
		data = binary.LittleEndian.AppendUint32(data, 0x1a0)
		return privateMD5V3(data, seed, privateMD5Permutations[index*64:(index+1)*64], uint32(byte(seed+1)))
	default:
		mode := int((folded & 15) / 3)
		constants := [6]uint32{0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1}
		seed := nonce + constants[mode]
		salt := binary.LittleEndian.AppendUint32(nil, 0xd76145fa)
		for i := range salt {
			salt[i] = bits.RotateLeft8(salt[i], -int((seed+2)&7))
		}
		data := append(append(append(append([]byte(nil), a...), b...), c...), salt...)
		data = binary.BigEndian.AppendUint64(data, 0x1a0)
		words := make([]uint32, 0, len(data)/4+112)
		for i := 0; i+3 < len(data); i += 4 {
			words = append(words, binary.BigEndian.Uint32(data[i:]))
		}
		if len(words) < 16 {
			return nil, fmt.Errorf("hashF13: SHA 分支至少需要 16 个字")
		}
		for i := 0; i < 112; i++ {
			x, y := words[i+1], words[i+14]
			s0 := bits.RotateLeft32(x, 14) ^ bits.RotateLeft32(x, 25) ^ (x >> 3)
			s1 := bits.RotateLeft32(y, 13) ^ bits.RotateLeft32(y, 15) ^ (y >> 10)
			words = append(words, words[i]+words[i+9]+s0+s1)
		}
		initial := [8]uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 0x3aab4d10, 0xb31d643a, 0xab4fabed, 0x804b3850}
		state := initial
		for i := range initial {
			initial[i] = bits.RotateLeft32(initial[i], -int(seed&31))
			state[i] = initial[i]
		}
		p := privateSHAModes[mode]
		for i := 0; i < p[0]; i++ {
			offset := uint32(i) + constants[mode]
			e := state[p[1]]
			choose := state[p[3]] ^ ((state[p[4]] ^ state[p[3]]) & e)
			sigmaE := bits.RotateLeft32(e, 26) ^ bits.RotateLeft32(e, 21) ^ bits.RotateLeft32(e, 7)
			t1 := words[offset&127] + choose + sigmaE + bits.RotateLeft32(privateSHAConstants[offset&63], -int(uint32(byte(seed+1))&31)) + state[p[5]]
			av, bv, cv := state[p[2]], state[p[6]], state[p[7]]
			t2 := (bits.RotateLeft32(av, 30) ^ bits.RotateLeft32(av, 19) ^ bits.RotateLeft32(av, 10)) + ((av & bv) | ((av | bv) & cv))
			added := state[p[9]]
			last := state[7]
			copy(state[1:], state[:7])
			state[0] = last
			state[p[10]] = t1 + t2
			state[p[8]] = added + t1
		}
		full := make([]byte, 32)
		for i := range state {
			binary.BigEndian.PutUint32(full[4*i:], state[i]+initial[i])
		}
		out := make([]byte, 16, 20)
		for i := range out {
			out[i] = full[i] ^ full[i+16]
		}
		return binary.LittleEndian.AppendUint32(out, sumMD5(out)), nil
	}
}

func BranchOf(rawURL string, body []byte, nonce uint32) int {
	var digest [16]byte
	if len(body) > 0 {
		digest = md5.Sum(body)
	}
	query := ""
	if pos := strings.IndexByte(rawURL, '?'); pos >= 0 {
		query = rawURL[pos+1:]
	}
	r := foldPrivate(0x20230928, SM3([]byte(query)))
	r = foldPrivate(r, digest[:])
	var seed [4]byte
	binary.LittleEndian.PutUint32(seed[:], nonce)
	r = foldPrivate(r, seed[:])
	return int(r&15) % 3
}

func KeyHash(data []byte, nonce uint32) (digest, suffix []byte) {
	message := make([]byte, 0, 2*len(data)+4)
	message = append(message, data...)
	message = binary.LittleEndian.AppendUint32(message, nonce)
	message = append(message, data...)
	digest = SM3(message)
	x := ((nonce >> 5) & 0x7f800) | (nonce >> 24)
	b := (nonce >> 16) & 255
	x ^= b >> 5
	x = ^(b ^ x)
	suffix = binary.LittleEndian.AppendUint32(nil, x)
	return digest, suffix
}

// XMXor reproduces xmxor's reversed keyed bytes and subsequent rolling edits.
func XMXor(data, key []byte) ([]byte, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("xmxor: 输入至少需要 3 字节")
	}
	if len(key) < 30 {
		return nil, fmt.Errorf("xmxor: 密钥至少需要 30 字节")
	}
	out := make([]byte, len(data))
	for i, value := range data {
		index := (i * 4) & 28
		k0, k1 := key[index], key[index+1]
		x := ^(bits.RotateLeft8(value, 4) + k0) ^ k1
		x = bits.RotateLeft8(x, 3)
		out[len(data)-i-1] = ^((k1 + x) ^ k0)
	}
	n := len(out)
	last := out[n-1]
	first := out[0]
	difference := out[n-2] ^ last
	out[0] += ^difference
	out[1] += out[n-1] ^ out[0] ^ 0xfe
	out[2] += (difference - first) ^ bits.RotateLeft8(out[1], 3) ^ 2
	for i := 0; i < n-4; i++ {
		out[i+3] += ^(out[i+1] ^ bits.RotateLeft8(out[i+2], 3) ^ byte(i+3))
	}
	out[n-1] ^= out[n-2]
	sum := byte(0)
	for _, value := range out[1:] {
		sum += value
	}
	out[0] = (out[0] ^ out[1]) + sum
	return out, nil
}
