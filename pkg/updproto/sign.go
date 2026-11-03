package updproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// TagPackage 是安装包签名标签。
	TagPackage = "hgupd-pkg-v1"
	// TagIndex 是索引文件签名标签。
	TagIndex = "hgupd-index-v1"
	// TagManifest 是清单 armored 串前缀，签名部分用 '.' 分隔。
	TagManifest = "hgupd-manifest-"
)

// 协议错误。
var (
	// ErrBadArmor 表示 armored 串结构不合法。
	ErrBadArmor = errors.New("updproto: 清单格式不合法，缺少 payload 或签名段")
	// ErrBadPrefix 表示 armored 串前缀不匹配。
	ErrBadPrefix = errors.New("updproto: 清单前缀不是 " + TagManifest)
	// ErrBadSignature 表示签名验证失败。
	ErrBadSignature = errors.New("updproto: 签名验证失败，内容可能被篡改")
	// ErrBadKey 表示公钥长度或编码不合法。
	ErrBadKey = errors.New("updproto: 公钥不合法")
	// ErrEmptyPayload 表示 payload 为空。
	ErrEmptyPayload = errors.New("updproto: 载荷为空")
)

// DecodePublicKey 把标准 base64 编码的 32 字节公钥解成 ed25519.PublicKey。
//
// 入参是标准 base64（含 + / = 三种字符），不是 URL-safe 变体。
func DecodePublicKey(b64 string) (ed25519.PublicKey, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, fmt.Errorf("%w: 空串", ErrBadKey)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// 兼容去掉 padding 的写法。
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
		if err != nil {
			return nil, fmt.Errorf("%w: base64 解码失败: %v", ErrBadKey, err)
		}
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: 长度 %d，期望 %d", ErrBadKey, len(raw), ed25519.PublicKeySize)
	}
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(key, raw)
	return key, nil
}

// VerifyPackage 用 pub 验证 sig 是否是 msg 的合法签名。
//
// 直接用 ed25519.Verify，内部会处理 S 值规范化与小阶点检查。
func VerifyPackage(pub ed25519.PublicKey, msg, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: 长度 %d", ErrBadKey, len(pub))
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: 签名长度 %d，期望 %d", ErrBadSignature, len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, msg, sig) {
		return ErrBadSignature
	}
	return nil
}

// OpenManifest 拆开 armored 清单串，验签后返回 payload 明文。
//
// armored 形如：
//
//	hgupd-manifest-<base64(payload)>.<base64(signature)>
//
// 按「最后一个 '.'」切分：base64 标准字符集不含 '.'，但 payload 里可能出现，
// 因此必须从右往左找分隔符。
func OpenManifest(pub ed25519.PublicKey, armored string) ([]byte, error) {
	s := strings.TrimSpace(armored)
	if s == "" {
		return nil, ErrBadArmor
	}
	if !strings.HasPrefix(s, TagManifest) {
		return nil, ErrBadPrefix
	}
	body := s[len(TagManifest):]
	dot := strings.LastIndexByte(body, '.')
	if dot <= 0 || dot == len(body)-1 {
		return nil, ErrBadArmor
	}
	payloadB64 := body[:dot]
	sigB64 := body[dot+1:]

	payload, err := decodeB64Loose(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("%w: payload 解码失败: %v", ErrBadArmor, err)
	}
	sig, err := decodeB64Loose(sigB64)
	if err != nil {
		return nil, fmt.Errorf("%w: 签名解码失败: %v", ErrBadArmor, err)
	}
	if len(payload) == 0 {
		return nil, ErrEmptyPayload
	}
	if err := VerifyPackage(pub, payload, sig); err != nil {
		return nil, err
	}
	return payload, nil
}

// SealManifest 是 OpenManifest 的逆操作，主要用于测试与本地构造清单。
func SealManifest(priv ed25519.PrivateKey, payload []byte) string {
	sig := ed25519.Sign(priv, payload)
	return TagManifest + base64.StdEncoding.EncodeToString(payload) +
		"." + base64.StdEncoding.EncodeToString(sig)
}

// SignPackage 计算 msg 的签名并用标签封装，形如 <tag>:<base64sig>。
func SignPackage(priv ed25519.PrivateKey, tag string, msg []byte) string {
	sig := ed25519.Sign(priv, msg)
	return tag + ":" + base64.StdEncoding.EncodeToString(sig)
}

// VerifyTagged 校验 <tag>:<base64sig> 形式的签名串。
func VerifyTagged(pub ed25519.PublicKey, tag, armored string, msg []byte) error {
	prefix := tag + ":"
	if !strings.HasPrefix(armored, prefix) {
		return fmt.Errorf("updproto: 签名串前缀不是 %q", prefix)
	}
	sig, err := decodeB64Loose(armored[len(prefix):])
	if err != nil {
		return fmt.Errorf("updproto: 签名解码失败: %w", err)
	}
	return VerifyPackage(pub, msg, sig)
}

// decodeB64Loose 依次尝试标准 base64 与无 padding 变体。
func decodeB64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("空串")
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
