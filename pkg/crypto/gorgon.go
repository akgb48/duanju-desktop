package crypto

import (
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
)

// GorgonWithNonce 使用显式随机量生成可复现的签名。
func GorgonWithNonce(query string, body []byte, timestamp uint32, nonce uint16) string {
	q := md5.Sum([]byte(query))
	var payload [20]byte
	copy(payload[:4], q[:4])
	if len(body) > 0 {
		b := md5.Sum(body)
		copy(payload[4:8], b[:4])
	}
	copy(payload[12:16], []byte{0, 4, 6, 4})
	binary.BigEndian.PutUint32(payload[16:], timestamp)
	key := []byte{0x4a, 0x40, 0x16, byte(nonce >> 8), 0x47, 0x6c, 1, byte(nonce)}
	cipher, _ := rc4.NewCipher(key) // fixed nonempty key; NewCipher cannot fail.
	cipher.XORKeyStream(payload[:], payload[:])
	for i := range payload {
		next := payload[(i+1)%len(payload)] // the final round uses the modified first byte.
		payload[i] = ^bits.Reverse8(bits.RotateLeft8(payload[i], 4)^next) ^ 0x14
	}
	var result [26]byte
	copy(result[:], []byte{0x84, 4, byte(nonce), byte(nonce >> 8), 0x40, 1})
	copy(result[6:], payload[:])
	return hex.EncodeToString(result[:])
}
