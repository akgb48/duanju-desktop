package crypto

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSignSetsAllHeaders 验证 9 个签名头全部写入。
func TestSignSetsAllHeaders(t *testing.T) {
	sg := NewDefaultSigner()
	req, _ := http.NewRequest(http.MethodPost, "https://api.test/reading/x", bytes.NewReader([]byte(`{"a":1}`)))

	sg.Sign(req, SignInput{
		Action: "feed",
		Query:  "aid=8662",
		Body:   []byte(`{"a":1}`),
		Device: DeviceFingerprint{
			DeviceID: "111", InstallID: "222", CDID: "333", OpenUDID: "444", AID: "8662",
		},
		Token: "tok",
		Now:   time.Unix(1700000000, 0),
	})

	for _, h := range HeaderNames {
		if req.Header.Get(h) == "" {
			t.Errorf("缺少签名头 %s", h)
		}
	}
	if n := len(HeaderNames); n != 9 {
		t.Errorf("签名头应为 9 个，实际 %d", n)
	}
}

// TestKhronosIsSecondTimestamp 验证时间戳头格式。
func TestKhronosIsSecondTimestamp(t *testing.T) {
	sg := NewDefaultSigner()
	req, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	ts := time.Unix(1700000000, 0)
	sg.Sign(req, SignInput{Now: ts})

	if got := req.Header.Get("X-Khronos"); got != "1700000000" {
		t.Errorf("X-Khronos = %q，期望 1700000000", got)
	}
	if got := req.Header.Get("X-SS-Req-Ticket"); got != "1700000000000" {
		t.Errorf("X-SS-Req-Ticket = %q，期望 1700000000000", got)
	}
}

// TestStubIsBodyMD5 验证 X-SS-Stub 是请求体的 MD5 大写 hex。
func TestStubIsBodyMD5(t *testing.T) {
	sg := NewDefaultSigner()
	body := []byte("hello")
	req, _ := http.NewRequest(http.MethodPost, "https://api.test/x", bytes.NewReader(body))
	sg.Sign(req, SignInput{Body: body, Now: time.Unix(1, 0)})

	// echo -n hello | md5sum → 5d41402abc4b2a76b9719d911017c592
	want := "5D41402ABC4B2A76B9719D911017C592"
	if got := req.Header.Get("X-SS-Stub"); got != want {
		t.Errorf("X-SS-Stub = %q，期望 %q", got, want)
	}
}

// TestSignDeterministic 验证同一输入产出相同签名（可复算）。
func TestSignDeterministic(t *testing.T) {
	sg := NewDefaultSigner()
	sg.Entropy = func() (SignatureEntropy, error) {
		return SignatureEntropy{0x1234, 0x11223344, MedusaEntropy{0x11223344, 0x11223344, 0x11223344, 7, 345}, "0123456789abcdef"}, nil
	}
	in := SignInput{
		Action: "feed", Query: "q=1", Body: []byte("b"),
		Device: DeviceFingerprint{DeviceID: "d", AID: "8662"},
		Token:  "t", Now: time.Unix(1700000000, 0),
	}
	first := map[string]string{}
	for _, h := range HeaderNames {
		req, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
		sg.Sign(req, in)
		// Trace-Id 是随机的，不参与确定性校验。
		if h == "X-TT-Trace-Id" {
			continue
		}
		first[h] = req.Header.Get(h)
	}

	req2, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	sg.Sign(req2, in)
	for h, want := range first {
		if got := req2.Header.Get(h); got != want {
			t.Errorf("%s 不确定: 第一次 %q，第二次 %q", h, want, got)
		}
	}
}

func TestAllSixSigningModesAgainstProtocolOrchestration(t *testing.T) {
	b, e := os.ReadFile("testdata/signer-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Modes []struct {
			Mode   SignMode
			Values map[string]string
		}
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	names := map[string]string{"Khronos": "X-Khronos", "Argus": "X-Argus", "Ladon": "X-Ladon", "Gorgon": "X-Gorgon", "Helios": "X-Helios", "Medusa": "X-Medusa", "SSStub": "X-SS-Stub", "ReqTicket": "X-SS-Req-Ticket", "TraceID": "X-TT-Trace-Id"}
	for _, v := range f.Modes {
		s := NewDefaultSigner()
		s.Entropy = func() (SignatureEntropy, error) {
			return SignatureEntropy{0x1234, 0x11223344, MedusaEntropy{0x11223344, 0x11223344, 0x11223344, 7, 345}, "0123456789abcdef"}, nil
		}
		req, _ := http.NewRequest("POST", "https://example.test/a?aid=8662&x=1", nil)
		req.Header.Set("X-Medusa", "stale")
		req.Header.Set("X-SS-Stub", "stale")
		e := s.SignChecked(req, SignInput{Query: "aid=8662&x=1", Body: []byte(`{"x":1}`), Device: DeviceFingerprint{DeviceID: "7654321098765432101"}, Now: time.Unix(1700000000, 123000000), Mode: v.Mode})
		if e != nil {
			t.Fatal(e)
		}
		for key, want := range v.Values {
			if key == "TraceID" && want != "" {
				want = "00-cfe5687b100123456789abcdef-cfe5687b10012345-01"
			}
			if got := req.Header.Get(names[key]); got != want {
				t.Errorf("mode %d %s got %q want %q", v.Mode, key, got, want)
			}
		}
	}
}

// TestSignNilRequest 验证 nil 请求不 panic。
func TestSignNilRequest(t *testing.T) {
	sg := NewDefaultSigner()
	sg.Sign(nil, SignInput{}) // 不应 panic
}

// TestRandomHelpers 验证随机工具的输出形态。
func TestRandomHelpers(t *testing.T) {
	if s := RandomHex(16); len(s) != 32 {
		t.Errorf("RandomHex(16) 长度 = %d，期望 32", len(s))
	}

	u := RandomUUID()
	if len(u) != 36 || strings.Count(u, "-") != 4 {
		t.Errorf("RandomUUID 形态错误: %q", u)
	}
	if u[14] != '4' {
		t.Errorf("UUID 版本位应为 4，实际 %q", u[14])
	}

	d := RandomDeviceID()
	if len(d) != 20 {
		t.Errorf("RandomDeviceID 长度 = %d，期望 20", len(d))
	}
	for _, c := range d {
		if c < '0' || c > '9' {
			t.Errorf("RandomDeviceID 应为纯数字，出现 %q", c)
		}
	}
}

// TestRandomUUIDUnique 验证 UUID 不重复。
func TestRandomUUIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		u := RandomUUID()
		if seen[u] {
			t.Fatalf("UUID 重复: %s", u)
		}
		seen[u] = true
	}
}

func TestHeaderNamesOrder(t *testing.T) {
	want := []string{
		"X-Khronos", "X-Argus", "X-Ladon", "X-Gorgon", "X-Helios",
		"X-Medusa", "X-SS-Stub", "X-SS-Req-Ticket", "X-TT-Trace-Id",
	}
	for i := range want {
		if HeaderNames[i] != want[i] {
			t.Errorf("第 %d 个头 = %q，期望 %q", i, HeaderNames[i], want[i])
		}
	}
}
