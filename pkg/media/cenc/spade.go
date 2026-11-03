package cenc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

var (
	ErrInvalidKey      = errors.New("cenc: 无效的 spade_a")
	ErrInvalidEnvelope = errors.New("cenc: 无效的 spade URL 信封")
)

var spadePepper = mustHex("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")

func mustHex(s string) []byte {
	v, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return v
}

func decodeSpadeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	if rem := len(s) & 3; rem != 0 {
		s += strings.Repeat("=", 4-rem)
	}
	return base64.StdEncoding.DecodeString(s)
}

func DecodeSeed(s string) ([]byte, error) {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if s == "" {
		return nil, nil
	}
	return decodeSpadeBase64(s)
}

// DeriveContentKey decodes spade_a and recovers its 16-byte content key. The
// source copy skips the first encoded byte; the output then skips its first
// transformed byte. These are distinct offsets in the source assembly.
func DeriveContentKey(s string) ([]byte, error) {
	b, err := decodeSpadeBase64(s)
	if err != nil {
		return nil, err
	}
	if len(b) < 3 {
		return nil, fmt.Errorf("%w: 长度 %d < 3", ErrInvalidKey, len(b))
	}
	x := int(b[0] ^ b[1] ^ b[2])
	n := len(b) - x + 0x2f
	if n > len(b)-1 {
		n = len(b) - 1
	}
	if n < 33 {
		return nil, fmt.Errorf("%w: 派生长度 %d < 33", ErrInvalidKey, n)
	}
	source := b[1 : 1+n]
	out := make([]byte, n)
	for i, v := range source {
		prior := byte(0xf6)
		if i == 1 {
			prior = 0x55
		} else if i >= 2 {
			prior = source[i-2]
		}
		out[i] = (v ^ prior) - byte(bits.OnesCount(uint(i))) - 0x15
	}
	key, err := hex.DecodeString(string(out[1:33]))
	if err != nil {
		return nil, fmt.Errorf("cenc: spade_a 派生结果不是 hex: %q", out[1:33])
	}
	return key, nil
}

// DecryptSpadeURL decrypts a base64 spade URL envelope using decoded seed bytes.
// The sample's ABI is (string, []byte), not (string, string): SHA-512 consumes
// seed, while the first string is trimmed, base64-decoded and checked for magic.
func DecryptSpadeURL(encoded string, seed []byte) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	ct, err := decodeSpadeBase64(encoded)
	if err != nil {
		return "", err
	}
	if len(ct) < 5 {
		return "", fmt.Errorf("%w: 长度 %d < 5", ErrInvalidEnvelope, len(ct))
	}
	if ct[0] != 0xa8 || ct[2] != 1 || ct[3] != 0 {
		return "", fmt.Errorf("%w: 头部 %02x %02x %02x %02x", ErrInvalidEnvelope, ct[0], ct[1], ct[2], ct[3])
	}
	n := (len(ct) - 4) &^ 15
	if n == 0 {
		return "", fmt.Errorf("%w: 没有完整块", ErrInvalidEnvelope)
	}
	first := sha512.Sum512(seed)
	h := sha512.New()
	h.Write(first[:])
	h.Write(spadePepper)
	k := h.Sum(nil)
	block, err := aes.NewCipher(k[:16])
	if err != nil {
		return "", fmt.Errorf("cenc: 构造 AES 失败: %w", err)
	}
	pt := make([]byte, n)
	cipher.NewCBCDecrypter(block, k[16:32]).CryptBlocks(pt, ct[4:4+n])
	pad := int(pt[len(pt)-1])
	if pad >= 1 && pad <= 16 && pad <= len(pt) {
		pt = pt[:len(pt)-pad]
	}
	return string(bytes.TrimRight(pt, "\x00")), nil
}

// CleanEscapedURL applies the three literal replacements used by the client.
// It deliberately does not interpret arbitrary HTML entities or percent escapes.
func CleanEscapedURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `\/`, "/")
	s = strings.ReplaceAll(s, `\u0026`, "&")
	return strings.ReplaceAll(s, "&amp;", "&")
}
