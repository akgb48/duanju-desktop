package crypto

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

var ttMagic = []byte{'t', 'c', 5, 16, 0, 0}

var ttPepper, _ = hex.DecodeString("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")

func DeriveTTKeyIV(keyStream []byte) (key, iv [aes.BlockSize]byte, err error) {
	if len(keyStream) != 32 {
		return key, iv, fmt.Errorf("ttencrypt: keyStream 长度必须为 32，收到 %d", len(keyStream))
	}
	first := sha512.Sum512(keyStream)
	h := sha512.New()
	h.Write(first[:])
	h.Write(ttPepper)
	derived := h.Sum(nil)
	copy(key[:], derived[:16])
	copy(iv[:], derived[16:32])
	return key, iv, nil
}

// TTGzipCompress uses the sample's gzip level 9 and the Go default gzip header.
func TTGzipCompress(plain []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err = writer.Write(plain); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// TTEncryptV5 encodes the device-register container using fresh keyStream bytes.
// It does not register a device or send any network request.
func TTEncryptV5(plain []byte) ([]byte, error) {
	keyStream := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, keyStream); err != nil {
		return nil, err
	}
	return TTEncryptV5WithKeyStream(plain, keyStream)
}

// TTEncryptV5WithKeyStream provides deterministic input for byte-level fixtures.
func TTEncryptV5WithKeyStream(plain, keyStream []byte) ([]byte, error) {
	compressed, err := TTGzipCompress(plain)
	if err != nil {
		return nil, err
	}
	return TTEncryptCompressedV5WithKeyStream(compressed, keyStream)
}

func TTEncryptCompressedV5WithKeyStream(compressed, keyStream []byte) ([]byte, error) {
	key, iv, err := DeriveTTKeyIV(keyStream)
	if err != nil {
		return nil, err
	}
	digest := sha512.Sum512(compressed)
	content := make([]byte, 0, len(digest)+len(compressed)+aes.BlockSize)
	content = append(content, digest[:]...)
	content = append(content, compressed...)
	pad := aes.BlockSize - len(content)%aes.BlockSize
	content = append(content, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ttMagic)+32+len(content))
	copy(out, ttMagic)
	copy(out[len(ttMagic):], keyStream)
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(out[len(ttMagic)+32:], content)
	return out, nil
}

// TTDecryptV5 verifies the envelope, padding, SHA512 prefix and gzip stream.
// A decompressed output limit prevents an untrusted response from exhausting RAM.
func TTDecryptV5(envelope []byte) ([]byte, error) {
	if len(envelope) < 54 || !bytes.HasPrefix(envelope, ttMagic) {
		return nil, errors.New("ttencrypt: 无效的 V5 信封")
	}
	const header = 6 + 32
	if (len(envelope)-header)%aes.BlockSize != 0 {
		return nil, errors.New("ttencrypt: 密文没有按 AES 块对齐")
	}
	key, iv, err := DeriveTTKeyIV(envelope[6:header])
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(envelope)-header)
	cipher.NewCBCDecrypter(block, iv[:]).CryptBlocks(plain, envelope[header:])
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New("ttencrypt: 无效的 PKCS7 填充")
	}
	for _, value := range plain[len(plain)-pad:] {
		if int(value) != pad {
			return nil, errors.New("ttencrypt: 无效的 PKCS7 填充")
		}
	}
	plain = plain[:len(plain)-pad]
	if len(plain) < sha512.Size {
		return nil, errors.New("ttencrypt: 明文不含摘要")
	}
	digest := sha512.Sum512(plain[sha512.Size:])
	if subtle.ConstantTimeCompare(digest[:], plain[:sha512.Size]) != 1 {
		return nil, errors.New("ttencrypt: 明文 SHA512 校验失败")
	}
	reader, err := gzip.NewReader(bytes.NewReader(plain[sha512.Size:]))
	if err != nil {
		return nil, fmt.Errorf("ttencrypt: gzip 解析失败: %w", err)
	}
	defer reader.Close()
	const limit = 64 << 20
	decoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ttencrypt: gzip 解压失败: %w", err)
	}
	if len(decoded) > limit {
		return nil, errors.New("ttencrypt: 解压结果超过 64 MiB")
	}
	return decoded, nil
}
