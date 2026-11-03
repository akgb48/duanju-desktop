package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Signer 为出站请求补齐风控头。
type Signer interface {
	Sign(req *http.Request, in SignInput)
}

// SignInput 是签名所需的全部上下文。
type SignInput struct {
	URL    string
	Mode   SignMode
	Action string
	Query  string
	Body   []byte
	Device DeviceFingerprint
	Token  string
	Now    time.Time
}

// DeviceFingerprint 是签名用到的设备字段。
type DeviceFingerprint struct {
	DeviceID  string
	InstallID string
	CDID      string
	OpenUDID  string
	AID       string
}

var HeaderNames = []string{
	"X-Khronos",
	"X-Argus",
	"X-Ladon",
	"X-Gorgon",
	"X-Helios",
	"X-Medusa",
	"X-SS-Stub",
	"X-SS-Req-Ticket",
	"X-TT-Trace-Id",
}

// RandomHex 返回 n 字节随机数的 hex 串。
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}

// RandomUUID 返回 RFC 4122 v4 UUID。
func RandomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RandomDeviceID 返回形如 20 位十进制数字的设备号。
func RandomDeviceID() string {
	const digits = "0123456789"
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}
