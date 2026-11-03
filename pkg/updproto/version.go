package updproto

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// IndexFile 是更新索引文件（Index-v1）的 JSON 结构。
//
// 服务端在 /api/v1/check 返回该结构，客户端验签后据此决定是否下载。
type IndexFile struct {
	// Version 是最新版本号，语义化格式，可带 v 前缀。
	Version string `json:"version"`
	// MinVersion 是允许自更新到 Version 的最低当前版本。
	// 低于它必须走完整安装包，不能增量。
	MinVersion string `json:"minVersion"`
	// Notes 是版本说明（Markdown 或纯文本）。
	Notes string `json:"notes"`
	// PubDate 是发布时间，Unix 秒。
	PubDate int64 `json:"pubDate"`
	// Packages 是各平台/架构的安装包列表。
	Packages []Package `json:"packages"`
}

// Package 是一个可下载的安装包描述。
type Package struct {
	// Platform 是目标平台，如 windows。
	Platform string `json:"platform"`
	// Arch 是目标架构，如 amd64、arm64。
	Arch string `json:"arch"`
	// URL 是下载地址，可为相对路径。
	URL string `json:"url"`
	// SHA256 是包的十六进制摘要，下载后必须校验。
	SHA256 string `json:"sha256"`
	// Size 是包的字节数，用于预分配与进度显示。
	Size int64 `json:"size"`
	// Signature 是包的 ed25519 签名（base64），签名对象为 SHA256 的原始字节。
	Signature string `json:"signature"`
}

// DecodeIndex 解析索引 JSON。
//
// 只做结构校验，不做验签；验签请用 VerifyTagged / OpenManifest。
func DecodeIndex(raw []byte) (*IndexFile, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("updproto: 索引内容为空")
	}
	var idx IndexFile
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("updproto: 解析索引失败: %w", err)
	}
	if strings.TrimSpace(idx.Version) == "" {
		return nil, fmt.Errorf("updproto: 索引缺少 version")
	}
	for i := range idx.Packages {
		p := &idx.Packages[i]
		if p.Platform == "" || p.Arch == "" {
			return nil, fmt.Errorf("updproto: 第 %d 个安装包缺少 platform/arch", i)
		}
		if p.URL == "" {
			return nil, fmt.Errorf("updproto: 第 %d 个安装包缺少 url", i)
		}
	}
	return &idx, nil
}

// EncodeIndex 把索引序列化成 JSON，便于测试与本地生成。
func EncodeIndex(idx *IndexFile) ([]byte, error) {
	if idx == nil {
		return nil, fmt.Errorf("updproto: EncodeIndex 收到 nil")
	}
	return json.MarshalIndent(idx, "", "  ")
}

// Package 按平台与架构挑选安装包。
func (f *IndexFile) Package(platform, arch string) (*Package, bool) {
	if f == nil {
		return nil, false
	}
	for i := range f.Packages {
		p := &f.Packages[i]
		if strings.EqualFold(p.Platform, platform) && strings.EqualFold(p.Arch, arch) {
			return p, true
		}
	}
	return nil, false
}

// Version 是解析后的语义化版本。
type Version struct {
	// Major / Minor / Patch 是主次修订号。
	Major, Minor, Patch int
	// Pre 是预发布标识，如 beta.1；正式版为空。
	Pre []string
	// Build 是 + 之后的构建元数据，不参与比较。
	Build string
	// Raw 是原始输入串。
	Raw string
}

// String 返回规范化后的版本串（不含 v 前缀）。
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// IsPre 报告是否为预发布版本。
func (v Version) IsPre() bool { return len(v.Pre) > 0 }

// ParseVersion 解析语义化版本，容忍 v 前缀与缺省的次/修订号。
//
// 接受：1.2.3 / v1.2.3 / 1.2 / 1 / 1.2.3-beta.1 / 1.2.3+build.5
func ParseVersion(s string) (Version, error) {
	v := Version{Raw: s}
	t := strings.TrimSpace(s)
	if t == "" {
		return v, fmt.Errorf("updproto: 版本号为空")
	}
	if t[0] == 'v' || t[0] == 'V' {
		t = t[1:]
	}
	// 拆出 + 后的构建元数据。
	if i := strings.IndexByte(t, '+'); i >= 0 {
		v.Build = t[i+1:]
		t = t[:i]
	}
	// 拆出 - 后的预发布段。
	if i := strings.IndexByte(t, '-'); i >= 0 {
		pre := t[i+1:]
		t = t[:i]
		if pre == "" {
			return v, fmt.Errorf("updproto: 预发布段为空: %q", s)
		}
		v.Pre = strings.Split(pre, ".")
	}
	if t == "" {
		return v, fmt.Errorf("updproto: 缺少数字版本号: %q", s)
	}
	parts := strings.Split(t, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("updproto: 版本号段数过多: %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" {
			return v, fmt.Errorf("updproto: 版本号存在空段: %q", s)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("updproto: 版本号段 %q 不是数字: %w", p, err)
		}
		if n < 0 {
			return v, fmt.Errorf("updproto: 版本号段 %q 为负数", p)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// NormalizeVersion 把版本串规范成 v 前缀 + 三段式。
//
// 解析失败时原样返回（去掉首尾空白），便于日志里保留脏数据。
func NormalizeVersion(s string) string {
	v, err := ParseVersion(s)
	if err != nil {
		return strings.TrimSpace(s)
	}
	return "v" + v.String()
}

// CompareVersion 比较两个语义化版本，返回 -1 / 0 / 1。
//
// 规则遵循 semver 2.0：
//   - 主次修订号数字比较；
//   - 有预发布段的版本小于同号正式版；
//   - 预发布段逐段比较，纯数字段按数值、其余按字典序；
//   - 无法解析的串按字面量比较，保证不 panic。
func CompareVersion(a, b string) int {
	va, ea := ParseVersion(a)
	vb, eb := ParseVersion(b)
	if ea != nil || eb != nil {
		return strings.Compare(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	if c := cmpInt(va.Major, vb.Major); c != 0 {
		return c
	}
	if c := cmpInt(va.Minor, vb.Minor); c != 0 {
		return c
	}
	if c := cmpInt(va.Patch, vb.Patch); c != 0 {
		return c
	}
	return cmpPre(va.Pre, vb.Pre)
}

// cmpInt 比较两个整数。
func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpPre 按 semver 规则比较预发布段。
func cmpPre(a, b []string) int {
	// 都无预发布段：相等。
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	// 有预发布段的更小。
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		ai, aerr := strconv.Atoi(a[i])
		bi, berr := strconv.Atoi(b[i])
		switch {
		case aerr == nil && berr == nil:
			if c := cmpInt(ai, bi); c != 0 {
				return c
			}
		case aerr == nil:
			// 数字段优先于字母段。
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a), len(b))
}
