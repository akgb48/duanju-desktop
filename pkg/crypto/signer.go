package crypto

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SignMode int

const (
	SignFull SignMode = iota
	SignMedusa
	SignRegister
	SignHelios
	SignPassport
	SignNone
)

type SignatureEntropy struct {
	GorgonNonce uint16
	HeliosSalt  uint32
	Medusa      MedusaEntropy
	TraceRandom string
}
type DefaultSigner struct {
	Profile CryptoAppProfile
	Entropy func() (SignatureEntropy, error)
}

func NewDefaultSigner() *DefaultSigner {
	return &DefaultSigner{Profile: HongguoCryptoProfile(), Entropy: newSignatureEntropy}
}
func newSignatureEntropy() (SignatureEntropy, error) {
	var b [30]byte
	if _, err := rand.Read(b[:]); err != nil {
		return SignatureEntropy{}, err
	}
	var extra [4]byte
	if _, err := rand.Read(extra[:]); err != nil {
		return SignatureEntropy{}, err
	}
	return SignatureEntropy{binary.LittleEndian.Uint16(b[:2]), binary.LittleEndian.Uint32(b[2:6]), MedusaEntropy{binary.LittleEndian.Uint32(b[6:10]), binary.LittleEndian.Uint32(b[10:14]), binary.LittleEndian.Uint32(b[14:18]), int(binary.LittleEndian.Uint16(extra[:2])) % 21, int(binary.LittleEndian.Uint16(extra[2:])) % 2000}, hex.EncodeToString(b[18:26])}, nil
}

func NewTraceID(now time.Time, randomHex string) string {
	trace := fmt.Sprintf("%08x", uint32(now.UnixMilli())) + "10" + randomHex
	return "00-" + trace + "-" + trace[:16] + "-01"
}

// TuneBranch follows the source bounded _rticket adjustment. Preserve URL
// spelling and field order; only the decimal timestamp substring may change.
func TuneBranch(rawURL string, body []byte, timestamp uint32) string {
	if BranchOf(rawURL, body, timestamp) != 1 {
		return rawURL
	}
	i := strings.Index(rawURL, "_rticket=")
	if i < 0 {
		return rawURL
	}
	start := i + 9
	end := start
	for end < len(rawURL) && rawURL[end] >= '0' && rawURL[end] <= '9' {
		end++
	}
	if end == start {
		return rawURL
	}
	value, err := strconv.ParseInt(rawURL[start:end], 10, 64)
	if err != nil {
		return rawURL
	}
	for step := int64(1); step < 256; step++ {
		candidate := rawURL[:start] + strconv.FormatInt(value+step, 10) + rawURL[end:]
		if BranchOf(candidate, body, timestamp) != 1 {
			return candidate
		}
	}
	return rawURL
}

func (s *DefaultSigner) PrepareURL(rawURL string, body []byte, now time.Time, mode SignMode) string {
	if mode == SignNone {
		return rawURL
	}
	return TuneBranch(rawURL, body, uint32(now.Unix()))
}

// Sign preserves the legacy injection interface. Session uses SignChecked to
// propagate entropy/encoding failures before sending a request.
func (s *DefaultSigner) Sign(req *http.Request, in SignInput) { _ = s.SignChecked(req, in) }
func (s *DefaultSigner) SignChecked(req *http.Request, in SignInput) error {
	if req == nil {
		return nil
	}
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	if in.URL == "" {
		in.URL = req.URL.String()
	}
	for _, name := range HeaderNames {
		req.Header.Del(name)
	}
	if in.Mode == SignNone {
		return nil
	}
	if in.Mode < SignFull || in.Mode > SignNone {
		return fmt.Errorf("crypto: 无效签名模式 %d", in.Mode)
	}
	entropy := s.Entropy
	if entropy == nil {
		entropy = newSignatureEntropy
	}
	e, err := entropy()
	if err != nil {
		return fmt.Errorf("crypto: 获取签名随机数失败: %w", err)
	}
	if len(e.TraceRandom) != 16 {
		return fmt.Errorf("crypto: TraceRandom 必须是 16 位 hex")
	}
	if _, err = hex.DecodeString(e.TraceRandom); err != nil {
		return err
	}
	profile := s.Profile
	if profile.AID == 0 {
		profile = HongguoCryptoProfile()
	}
	timestamp := uint32(in.Now.Unix())
	req.Header.Set("X-Khronos", strconv.FormatInt(int64(int32(timestamp)), 10))
	if in.Mode == SignFull || in.Mode == SignMedusa || in.Mode == SignHelios || in.Mode == SignPassport {
		req.Header.Set("X-Helios", HeliosWithSalt(int32(timestamp), e.HeliosSalt, profile.License, profile.AID))
	}
	if in.Mode == SignFull || in.Mode == SignMedusa {
		v, err := BuildMedusaWithEntropy(in.URL, in.Body, timestamp, in.Device.DeviceID, profile, e.Medusa)
		if err != nil {
			return fmt.Errorf("crypto: Medusa 签名失败: %w", err)
		}
		req.Header.Set("X-Medusa", v)
	}
	if in.Mode == SignFull || in.Mode == SignRegister || in.Mode == SignPassport {
		req.Header.Set("X-Gorgon", GorgonWithNonce(in.Query, in.Body, timestamp, e.GorgonNonce))
		sum := md5.Sum(in.Body)
		req.Header.Set("X-SS-Stub", strings.ToUpper(hex.EncodeToString(sum[:])))
		req.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(in.Now.UnixMilli(), 10))
		req.Header.Set("X-TT-Trace-Id", NewTraceID(in.Now, e.TraceRandom))
	}
	if in.Mode == SignFull || in.Mode == SignPassport {
		var a, b [4]byte
		binary.LittleEndian.PutUint32(a[:], timestamp)
		binary.BigEndian.PutUint32(b[:], timestamp)
		req.Header.Set("X-Argus", base64.StdEncoding.EncodeToString(a[:]))
		req.Header.Set("X-Ladon", base64.StdEncoding.EncodeToString(b[:]))
	}
	return nil
}
