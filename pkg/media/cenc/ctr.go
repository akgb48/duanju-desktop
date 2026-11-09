package cenc

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
)

// DecryptCTRSample decrypts a contiguous protected sample region at an encrypted
// byte offset. Clear subsample bytes must be excluded by the caller. dst and src
// may be identical, but must not overlap partially (cipher.Stream semantics).
func DecryptCTRSample(dst, src, key, iv []byte, offset int64) error {
	if len(src) == 0 {
		return nil
	}
	if len(key) != 16 {
		return fmt.Errorf("cenc: 内容密钥长度 %d != 16", len(key))
	}
	if len(iv) != aes.BlockSize {
		return fmt.Errorf("cenc: IV 长度 %d != 16", len(iv))
	}
	if len(dst) < len(src) {
		return fmt.Errorf("cenc: 输出缓冲区过短")
	}
	if offset < 0 {
		return fmt.Errorf("cenc: 偏移不能为负数")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	decryptCTR(dst, src, block, iv, offset)
	return nil
}

func decryptCTR(dst, src []byte, block cipher.Block, iv []byte, offset int64) {
	var counter [16]byte
	copy(counter[:], iv)
	hi := binary.BigEndian.Uint64(counter[:8])
	lo := binary.BigEndian.Uint64(counter[8:])
	advanced := lo + uint64(offset/16)
	if advanced < lo {
		hi++
	}
	binary.BigEndian.PutUint64(counter[:8], hi)
	binary.BigEndian.PutUint64(counter[8:], advanced)
	stream := cipher.NewCTR(block, counter[:])
	if skip := int(offset % 16); skip > 0 {
		var discarded [16]byte
		stream.XORKeyStream(discarded[:skip], discarded[:skip])
	}
	stream.XORKeyStream(dst[:len(src)], src)
}
