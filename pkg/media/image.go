package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/gen2brain/heic"
)

var nonPublicImageNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")

// dialPublicImage connects to the checked IP so DNS changes cannot redirect
// the connection to a local service. Redirects use this same transport.
func dialPublicImage(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{KeepAlive: 30 * time.Second}
	for _, address := range addresses {
		ip := address.Unmap()
		if !publicImageIP(ip) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("图片地址未解析到公网 IP")
}

func publicImageIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Is6() && !globalIPv6.Contains(ip) {
		return false
	}
	for _, blocked := range nonPublicImageNetworks {
		if blocked.Contains(ip) {
			return false
		}
	}
	return true
}

// Public novel covers use immutable asset paths. Rotating access signatures
// do not change their pixels; retain every rendering/query option in the key.
// The outbound signed URL is always left untouched.
func imageCacheKey(key string) string {
	u, err := url.Parse(key)
	if err != nil || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".fqnovelpic.com") || !strings.HasPrefix(u.Path, "/novel-pic/") {
		return key
	}
	q := u.Query()
	if !q.Has("x-signature") || !q.Has("x-expires") {
		return key
	}
	q.Del("x-signature")
	q.Del("x-expires")
	u.RawQuery = q.Encode()
	return u.String()
}

// A built-in WASM decoder keeps covers independent of system HEIF extensions.
// Limit simultaneous decodes so a large grid cannot allocate one module per img.
var imageDecodeSlots = make(chan struct{}, 2)

func isHEIC(data []byte, contentType string) bool {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return false
	}
	size := int(binary.BigEndian.Uint32(data[:4]))
	if size > len(data) {
		size = len(data)
	}
	if size > 512 {
		size = 512
	}
	for i := 8; i+4 <= size; i += 4 {
		switch string(data[i : i+4]) {
		case "avif", "avis":
			return false
		case "heic", "heix", "hevc", "hevx", "heim", "heis":
			return true
		}
	}
	kind := strings.ToLower(strings.Split(contentType, ";")[0])
	return kind == "image/heic" || kind == "image/heif"
}

func browserImage(ctx context.Context, data []byte, contentType string) ([]byte, string, error) {
	if !isHEIC(data, contentType) {
		return data, imageContentType(data), nil
	}
	select {
	case imageDecodeSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-imageDecodeSlots }()
	config, err := heic.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("读取 HEIC 图片信息: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 32768 || config.Height > 32768 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return nil, "", fmt.Errorf("HEIC 图片尺寸超出限制")
	}
	img, err := heic.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("解码 HEIC 图片: %w", err)
	}
	var out bytes.Buffer
	if err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", err
	}
	return out.Bytes(), "image/jpeg", nil
}

func imageContentType(data []byte) string {
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		size := int(binary.BigEndian.Uint32(data[:4]))
		if size > len(data) {
			size = len(data)
		}
		if size > 512 {
			size = 512
		}
		for i := 8; i+4 <= size; i += 4 {
			if brand := string(data[i : i+4]); brand == "avif" || brand == "avis" {
				return "image/avif"
			}
		}
	}
	header := data
	if len(header) > 512 {
		header = header[:512]
	}
	text := strings.TrimSpace(string(header))
	if strings.HasPrefix(text, "<svg") || (strings.HasPrefix(text, "<?xml") && strings.Contains(text, "<svg")) {
		return "image/svg+xml"
	}
	return http.DetectContentType(data)
}
