package updproto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// newKeyPair 生成一对测试密钥。
func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥对: %v", err)
	}
	return pub, priv
}

func TestTagConstants(t *testing.T) {
	if TagPackage != "hgupd-pkg-v1" {
		t.Errorf("TagPackage = %q", TagPackage)
	}
	if TagIndex != "hgupd-index-v1" {
		t.Errorf("TagIndex = %q", TagIndex)
	}
	if TagManifest != "hgupd-manifest-" {
		t.Errorf("TagManifest = %q", TagManifest)
	}
}

func TestDecodePublicKey(t *testing.T) {
	pub, _ := newKeyPair(t)
	b64 := base64.StdEncoding.EncodeToString(pub)

	got, err := DecodePublicKey(b64)
	if err != nil {
		t.Fatalf("DecodePublicKey: %v", err)
	}
	if !bytes.Equal(got, pub) {
		t.Fatalf("公钥不一致: got=%x want=%x", got, pub)
	}

	// 去掉 padding 也要能解。
	trimmed := strings.TrimRight(b64, "=")
	if got, err := DecodePublicKey(trimmed); err != nil {
		t.Fatalf("无 padding 解码: %v", err)
	} else if !bytes.Equal(got, pub) {
		t.Fatal("无 padding 解码结果不一致")
	}

	// 前后空白要被吃掉。
	if _, err := DecodePublicKey("  " + b64 + "\n"); err != nil {
		t.Fatalf("带空白解码: %v", err)
	}

	// 错误路径
	cases := []struct {
		name string
		in   string
	}{
		{"空串", ""},
		{"非 base64", "!!!not-base64!!!"},
		{"长度不足", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"长度超额", base64.StdEncoding.EncodeToString(make([]byte, 64))},
	}
	for _, c := range cases {
		if _, err := DecodePublicKey(c.in); !errors.Is(err, ErrBadKey) {
			t.Errorf("%s: 期望 ErrBadKey，实际 %v", c.name, err)
		}
	}
}

func TestVerifyPackage(t *testing.T) {
	pub, priv := newKeyPair(t)
	msg := []byte(`{"version":"1.2.17"}`)
	sig := ed25519.Sign(priv, msg)

	if err := VerifyPackage(pub, msg, sig); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}

	// 篡改内容
	tampered := append([]byte(nil), msg...)
	tampered[3] ^= 0x01
	if err := VerifyPackage(pub, tampered, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("篡改内容应 ErrBadSignature，实际 %v", err)
	}

	// 篡改签名
	badSig := append([]byte(nil), sig...)
	badSig[0] ^= 0xff
	if err := VerifyPackage(pub, msg, badSig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("篡改签名应 ErrBadSignature，实际 %v", err)
	}

	// 换密钥
	otherPub, _ := newKeyPair(t)
	if err := VerifyPackage(otherPub, msg, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("换密钥应 ErrBadSignature，实际 %v", err)
	}

	// 长度错误
	if err := VerifyPackage(pub, msg, sig[:16]); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("签名过短应 ErrBadSignature，实际 %v", err)
	}
	if err := VerifyPackage(pub[:8], msg, sig); !errors.Is(err, ErrBadKey) {
		t.Fatalf("公钥过短应 ErrBadKey，实际 %v", err)
	}
}

func TestOpenManifest(t *testing.T) {
	pub, priv := newKeyPair(t)
	payload := []byte(`{"version":"1.2.17","notes":"修复若干问题"}`)
	armored := SealManifest(priv, payload)

	if !strings.HasPrefix(armored, TagManifest) {
		t.Fatalf("armored 缺少前缀: %s", armored)
	}
	got, err := OpenManifest(pub, armored)
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload 不一致: got=%s want=%s", got, payload)
	}

	// 换密钥 → 验签失败
	otherPub, _ := newKeyPair(t)
	if _, err := OpenManifest(otherPub, armored); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("换密钥应 ErrBadSignature，实际 %v", err)
	}

	// 篡改 payload 段
	body := strings.TrimPrefix(armored, TagManifest)
	dot := strings.LastIndexByte(body, '.')
	evilPayload := base64.StdEncoding.EncodeToString([]byte(`{"version":"9.9.9"}`))
	evil := TagManifest + evilPayload + body[dot:]
	if _, err := OpenManifest(pub, evil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("替换 payload 应 ErrBadSignature，实际 %v", err)
	}

	// 篡改签名段
	evilSig := TagManifest + body[:dot] + "." + base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if _, err := OpenManifest(pub, evilSig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("替换签名应 ErrBadSignature，实际 %v", err)
	}

	// 结构错误
	if _, err := OpenManifest(pub, ""); !errors.Is(err, ErrBadArmor) {
		t.Errorf("空串应 ErrBadArmor，实际 %v", err)
	}
	if _, err := OpenManifest(pub, "hgupd-pkg-v1xx"); !errors.Is(err, ErrBadPrefix) {
		t.Errorf("前缀错误应 ErrBadPrefix，实际 %v", err)
	}
	if _, err := OpenManifest(pub, TagManifest+"nodot"); !errors.Is(err, ErrBadArmor) {
		t.Errorf("缺分隔符应 ErrBadArmor，实际 %v", err)
	}
	if _, err := OpenManifest(pub, TagManifest+".abc"); !errors.Is(err, ErrBadArmor) {
		t.Errorf("空 payload 应 ErrBadArmor，实际 %v", err)
	}
	if _, err := OpenManifest(pub, TagManifest+"abc."); !errors.Is(err, ErrBadArmor) {
		t.Errorf("空签名应 ErrBadArmor，实际 %v", err)
	}
}

func TestOpenManifestSplitOnLastDot(t *testing.T) {
	// payload 本身含 '.'（JSON 里很常见），必须从右往左切。
	pub, priv := newKeyPair(t)
	payload := []byte(`{"version":"1.2.17","url":"https://example.com/a.b"}`)
	armored := SealManifest(priv, payload)
	got, err := OpenManifest(pub, armored)
	if err != nil {
		t.Fatalf("OpenManifest: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload 不一致: %s", got)
	}
}

func TestSignPackageRoundTrip(t *testing.T) {
	pub, priv := newKeyPair(t)
	digest := []byte("d03a234a51ccb88127c4c094232dbd7cacc56fd5d4786aa7044dcdeba962545d")

	armored := SignPackage(priv, TagPackage, digest)
	if !strings.HasPrefix(armored, TagPackage+":") {
		t.Fatalf("签名串格式错误: %s", armored)
	}
	if err := VerifyTagged(pub, TagPackage, armored, digest); err != nil {
		t.Fatalf("VerifyTagged: %v", err)
	}
	// 内容变了要失败
	if err := VerifyTagged(pub, TagPackage, armored, []byte("other")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("内容不符应 ErrBadSignature，实际 %v", err)
	}
	// 标签不符要失败
	if err := VerifyTagged(pub, TagIndex, armored, digest); err == nil {
		t.Fatal("标签不符应报错")
	}
}

func TestDecodeIndex(t *testing.T) {
	raw := []byte(`{
	  "version": "1.2.17",
	  "minVersion": "1.2.0",
	  "notes": "修复若干问题",
	  "pubDate": 1767225600,
	  "packages": [
	    {"platform":"windows","arch":"amd64","url":"/dl/hgplayer.exe",
	     "sha256":"d03a234a51ccb88127c4c094232dbd7cacc56fd5d4786aa7044dcdeba962545d",
	     "size": 26637824, "signature": "abc"}
	  ]
	}`)
	idx, err := DecodeIndex(raw)
	if err != nil {
		t.Fatalf("DecodeIndex: %v", err)
	}
	if idx.Version != "1.2.17" || idx.MinVersion != "1.2.0" {
		t.Fatalf("版本字段错误: %+v", idx)
	}
	if idx.PubDate != 1767225600 {
		t.Errorf("PubDate = %d", idx.PubDate)
	}
	if len(idx.Packages) != 1 {
		t.Fatalf("Packages 长度 = %d", len(idx.Packages))
	}
	p := idx.Packages[0]
	if p.Platform != "windows" || p.Arch != "amd64" || p.Size != 26637824 {
		t.Fatalf("Package 字段错误: %+v", p)
	}
	// 选择器
	if got, ok := idx.Package("Windows", "AMD64"); !ok || got.URL != "/dl/hgplayer.exe" {
		t.Fatalf("Package 选择失败: %+v ok=%v", got, ok)
	}
	if _, ok := idx.Package("linux", "amd64"); ok {
		t.Fatal("不应选出 linux 包")
	}

	// 往返
	enc, err := EncodeIndex(idx)
	if err != nil {
		t.Fatalf("EncodeIndex: %v", err)
	}
	back, err := DecodeIndex(enc)
	if err != nil {
		t.Fatalf("往返解码: %v", err)
	}
	if back.Version != idx.Version || len(back.Packages) != 1 {
		t.Fatalf("往返不一致: %+v", back)
	}

	// 错误路径
	bad := []struct {
		name string
		in   string
	}{
		{"空", ``},
		{"非法 JSON", `{`},
		{"缺 version", `{"packages":[]}`},
		{"包缺 platform", `{"version":"1.0.0","packages":[{"arch":"amd64","url":"/a"}]}`},
		{"包缺 url", `{"version":"1.0.0","packages":[{"platform":"windows","arch":"amd64"}]}`},
	}
	for _, b := range bad {
		if _, err := DecodeIndex([]byte(b.in)); err == nil {
			t.Errorf("%s: 应报错", b.name)
		}
	}
	if _, err := DecodeIndex(nil); err == nil {
		t.Error("nil 应报错")
	}
}

func TestParseVersion(t *testing.T) {
	ok := []struct {
		in   string
		want Version
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"V1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"1.2.3-beta.1", Version{Major: 1, Minor: 2, Patch: 3, Pre: []string{"beta", "1"}}},
		{"1.2.3+build.5", Version{Major: 1, Minor: 2, Patch: 3, Build: "build.5"}},
		{"1.2.3-rc.1+sha.abc", Version{Major: 1, Minor: 2, Patch: 3, Pre: []string{"rc", "1"}, Build: "sha.abc"}},
		{"1.2", Version{Major: 1, Minor: 2, Patch: 0}},
		{"1", Version{Major: 1}},
		{"0.0.0", Version{}},
	}
	for _, c := range ok {
		got, err := ParseVersion(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got.Major != c.want.Major || got.Minor != c.want.Minor || got.Patch != c.want.Patch {
			t.Errorf("%q 数字段 = %d.%d.%d，期望 %d.%d.%d", c.in,
				got.Major, got.Minor, got.Patch, c.want.Major, c.want.Minor, c.want.Patch)
		}
		if len(got.Pre) != len(c.want.Pre) {
			t.Errorf("%q Pre = %v，期望 %v", c.in, got.Pre, c.want.Pre)
		}
		if got.Build != c.want.Build {
			t.Errorf("%q Build = %q，期望 %q", c.in, got.Build, c.want.Build)
		}
		if got.Raw != c.in {
			t.Errorf("%q Raw = %q", c.in, got.Raw)
		}
	}

	bad := []string{"", "abc", "1.2.3.4", "1..3", "1.-2.3", "1.2.3-"}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("%q: 应报错", in)
		}
	}
}

func TestVersionStringAndIsPre(t *testing.T) {
	v, _ := ParseVersion("v1.2.3-beta.1+sha.abc")
	if got := v.String(); got != "1.2.3-beta.1+sha.abc" {
		t.Errorf("String() = %q", got)
	}
	if !v.IsPre() {
		t.Error("应为预发布版")
	}
	v2, _ := ParseVersion("1.2.3")
	if v2.IsPre() {
		t.Error("正式版不应是预发布")
	}
	if got := v2.String(); got != "1.2.3" {
		t.Errorf("String() = %q", got)
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "v1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.3-beta.1", "1.2.3", -1},
		{"1.2.3", "1.2.3-beta.1", 1},
		{"1.2.3-alpha", "1.2.3-beta", -1},
		{"1.2.3-beta.1", "1.2.3-beta.2", -1},
		{"1.2.3-beta.2", "1.2.3-beta.10", -1},
		{"1.2.3-beta", "1.2.3-beta.1", -1},
		{"1.2.3-1", "1.2.3-alpha", -1},
		{"1.2.3-alpha.1", "1.2.3-alpha.beta", -1},
		{"1.2.17", "1.2.16", 1},
		{"v1.2.17", "1.2.16", 1},
	}
	for _, c := range cases {
		if got := CompareVersion(c.a, c.b); got != c.want {
			t.Errorf("CompareVersion(%q,%q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}

	// 乱码输入不得 panic，按字面量比较。
	_ = CompareVersion("garbage", "1.2.3")
	_ = CompareVersion("", "")
	if got := CompareVersion("abc", "abc"); got != 0 {
		t.Errorf("相同乱码应返回 0，实际 %d", got)
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"1.2.3":        "v1.2.3",
		"v1.2.3":       "v1.2.3",
		"1.2":          "v1.2.0",
		"1":            "v1.0.0",
		" 1.2.3 ":      "v1.2.3",
		"1.2.3-beta.1": "v1.2.3-beta.1",
		"":             "",
		"junk":         "junk",
	}
	for in, want := range cases {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestStructJSONShapes(t *testing.T) {
	// Feedback 的字段名必须与前端 FeedbackItem 一致。
	b, err := json.Marshal(Feedback{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantFeedback := []string{
		"id", "category", "content", "email", "status", "reply",
		"createdAt", "repliedAt", "messages",
	}
	for _, k := range wantFeedback {
		if _, ok := m[k]; !ok {
			t.Errorf("Feedback 缺字段 %q: %s", k, b)
		}
	}

	// FeedbackMsg
	b, _ = json.Marshal(FeedbackMsg{})
	m = nil
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"from", "content", "at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("FeedbackMsg 缺字段 %q: %s", k, b)
		}
	}

	// Notice
	b, _ = json.Marshal(Notice{})
	m = nil
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "level", "text", "url"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Notice 缺字段 %q: %s", k, b)
		}
	}

	// Disable
	b, _ = json.Marshal(Disable{})
	m = nil
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"message", "url"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Disable 缺字段 %q: %s", k, b)
		}
	}

	// FeedbackReply
	b, _ = json.Marshal(FeedbackReply{})
	m = nil
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "from", "content", "at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("FeedbackReply 缺字段 %q: %s", k, b)
		}
	}
}

func TestManifestDecode(t *testing.T) {
	raw := []byte(`{
	  "version":"1.2.17",
	  "minVersion":"1.2.0",
	  "notes":"修复问题",
	  "pubDate":1767225600,
	  "notice":{"id":"n1","level":"info","text":"公告","url":"https://example.com"},
	  "disabled":null,
	  "manifestVersionCode":73932,
	  "files":[
	    {"platform":"windows","arch":"amd64","url":"/dl/a.exe","sha256":"aa","size":10,"signature":"s"}
	  ]
	}`)
	var mf Manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatalf("解析 Manifest: %v", err)
	}
	if mf.Version != "1.2.17" {
		t.Errorf("Version = %q", mf.Version)
	}
	if mf.Notice == nil || mf.Notice.ID != "n1" {
		t.Fatalf("Notice 解析失败: %+v", mf.Notice)
	}
	if mf.Disable != nil {
		t.Errorf("Disable 应为 nil，实际 %+v", mf.Disable)
	}
	if mf.ManifestVersionCode != 73932 {
		t.Errorf("ManifestVersionCode = %d", mf.ManifestVersionCode)
	}
	if len(mf.Files) != 1 || mf.Files[0].Size != 10 {
		t.Fatalf("Files 解析失败: %+v", mf.Files)
	}
}

func TestReleaseDecode(t *testing.T) {
	raw := []byte(`{"version":"1.2.17","changelog":"修复","size":26637824,"publishedAt":1767225600}`)
	var r Release
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("解析 Release: %v", err)
	}
	if r.Version != "1.2.17" || r.Size != 26637824 || r.PublishedAt != 1767225600 {
		t.Fatalf("Release 字段错误: %+v", r)
	}
}
