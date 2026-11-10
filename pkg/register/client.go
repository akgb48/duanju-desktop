package register

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	hgcrypto "duanju-desktop/pkg/crypto"
)

//go:embed templates.json
var templateData []byte

// HTTPStatusError preserves registration backoff without interpreting a denial
// as a bad device identity that should immediately be replaced.
type HTTPStatusError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("register: HTTP %d", e.StatusCode) }

func NewProfile(now time.Time) (*Profile, error) {
	return NewProfileWith(now, "xiaomi_14", "playback_v73")
}

// NewProfileWith keeps the selected hardware and app stable across pool
// registrations, while generating independent local identifiers for each device.
func NewProfileWith(now time.Time, model, app string) (*Profile, error) {
	var tables struct {
		Hardware map[string]HardwareTemplate
		Apps     map[string]AppConfig
	}
	if err := json.Unmarshal(templateData, &tables); err != nil {
		return nil, err
	}
	h, ok := tables.Hardware[model]
	if !ok {
		return nil, fmt.Errorf("register: 未知设备模板 %q", model)
	}
	cfg, ok := tables.Apps[app]
	if !ok {
		return nil, fmt.Errorf("register: 未知应用模板 %q", app)
	}
	p := &Profile{}
	dst := reflect.ValueOf(p).Elem()
	for _, source := range []any{h, cfg} {
		v := reflect.ValueOf(source)
		for i := 0; i < v.NumField(); i++ {
			f := dst.FieldByName(v.Type().Field(i).Name)
			if f.IsValid() && f.Type() == v.Field(i).Type() {
				f.Set(v.Field(i))
			}
		}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	p.OpenUDID = hex.EncodeToString(entropy[:8])
	p.SerialNumber = hex.EncodeToString(entropy[8:16])
	p.UDID = hex.EncodeToString(entropy[16:24])
	p.ClientUDID = hgcrypto.RandomUUID()
	p.CDID = hgcrypto.RandomUUID()
	p.ReqID = hgcrypto.RandomUUID()
	p.GoogleAID = hgcrypto.RandomUUID()
	p.MacAddress = fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", entropy[24], entropy[25], entropy[26], entropy[27], entropy[28])
	p.DevicePlatform = "android"
	p.DeviceCategory = "phone"
	p.OS = "Android"
	p.SDKFlavor = "china"
	p.Access = "wifi"
	p.AppVersion = p.VersionName
	p.QueryResolution = strings.ReplaceAll(p.Resolution, "x", "*")
	p.Resolution = strings.ReplaceAll(p.Resolution, "*", "x")
	p.SimSerialNumber = []string{}
	p.IPv6List = []string{}
	p.OaidMaySupport = p.DeviceBrand != "google"
	p.APKFirstInstallTime = now.Add(-time.Hour).UnixMilli()
	p.FirstLaunchTimestamp = now.UnixMilli()
	p.Custom.WebUA = fmt.Sprintf(webAgentFormat, p.OSVersion, p.DeviceModel, p.BuildDisplay)
	return p, nil
}

func (p *Profile) UserAgent() string {
	return fmt.Sprintf(userAgentFormat, p.Package, p.VersionCode, p.OSVersion, p.Language, p.Region, p.DeviceModel, p.BuildDisplay, p.CronetVersion)
}

func (p *Profile) MarshalContainer(now time.Time) ([]byte, error) {
	if p == nil {
		return nil, errors.New("register: 未设置设备指纹")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var h AppHeader
	if err = json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		MagicTag string    `json:"magic_tag"`
		Header   AppHeader `json:"header"`
		GenTime  int64     `json:"_gen_time"`
	}{"ss_app_log", h, now.UnixMilli()})
}

func (p *Profile) BuildQuery(now time.Time) url.Values {
	n := strconv.Itoa
	return url.Values{
		"tt_data": {"a"}, "ac": {p.Access}, "channel": {p.Channel}, "aid": {n(p.AID)}, "app_name": {p.AppName}, "version_code": {n(p.VersionCode)}, "version_name": {p.VersionName},
		"device_platform": {p.DevicePlatform}, "os": {"android"}, "ssmix": {"a"}, "device_type": {p.DeviceType}, "device_brand": {p.DeviceBrand}, "language": {p.Language}, "os_api": {n(p.OSApi)}, "os_version": {p.OSVersion},
		"openudid": {p.OpenUDID}, "manifest_version_code": {n(p.ManifestVersionCode)}, "resolution": {p.QueryResolution}, "dpi": {n(p.DPI)}, "update_version_code": {n(p.UpdateVersionCode)}, "_rticket": {strconv.FormatInt(now.UnixMilli(), 10)},
		"package": {p.Package}, "first_launch_timestamp": {strconv.FormatInt(p.FirstLaunchTimestamp, 10)}, "last_deeplink_update_version_code": {"0"}, "cpu_support64": {"true"}, "host_abi": {p.CpuABI},
		"is_guest_mode": {"0"}, "app_type": {"normal"}, "minor_status": {"0"}, "appTheme": {"light"}, "is_preinstall": {"0"}, "need_personal_recommend": {"1"}, "is_android_pad": {"0"}, "is_android_fold": {"0"},
		"ts": {strconv.FormatInt(now.Unix(), 10)}, "cdid": {p.CDID}, "md": {"0"}, "cronet_version": {p.CronetVersion}, "ttnet_version": {p.TTNetVersion}, "use_store_region_cookie": {"1"},
	}
}

type Client struct {
	Profile    *Profile
	HTTPClient *http.Client
	BaseURL    string
	Header     http.Header
}
type Prepared struct {
	URL                string
	Payload, Container []byte
	Query              string
	Headers            http.Header
}
type Identity struct {
	DeviceID    string `json:"device_id"`
	InstallID   string `json:"install_id"`
	DeviceToken string `json:"device_token"`
	OdinTT      string `json:"odin_tt,omitempty"`
	StoreRegion string `json:"store_region,omitempty"`
	TTReq       string `json:"ttreq,omitempty"`
}
type Response struct {
	Identity   Identity
	StatusCode int
	Cookies    []*http.Cookie
}

func (c *Client) Prepare(now time.Time) (*Prepared, error) {
	if c.Profile == nil {
		return nil, errors.New("register: 未设置设备指纹")
	}
	b, err := c.Profile.MarshalContainer(now)
	if err != nil {
		return nil, err
	}
	payload, err := hgcrypto.TTEncryptV5(b)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://log.snssdk.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("register: 无效的服务器地址")
	}
	q := c.Profile.BuildQuery(now).Encode()
	headers := http.Header{}
	for k, v := range map[string]string{
		"Accept": "application/json; charset=utf-8,application/x-protobuf", "Accept-Encoding": "gzip", "Content-Type": "application/octet-stream;tt-data=a",
		"Lc": "102", "Sdk-Version": "2", "X-Tt-Store-Region": "cn", "X-Tt-Store-Region-Src": "did", "X-Vc-Bdturing-Sdk-Version": "4.0.3.cn", "User-Agent": c.Profile.UserAgent(),
		"X-Ss-Req-Ticket": strconv.FormatInt(now.UnixMilli(), 10),
	} {
		headers.Set(k, v)
	}
	for k, vs := range c.Header {
		headers[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
	}
	// Digest the TT envelope as transmitted. No second gzip pass is performed.
	sum := md5.Sum(payload)
	headers.Set("X-SS-Stub", strings.ToUpper(hex.EncodeToString(sum[:])))
	var sec [4]byte
	binary.LittleEndian.PutUint32(sec[:], uint32(now.Unix()))
	headers.Set("X-Khronos", strconv.FormatInt(int64(int32(now.Unix())), 10))
	headers.Set("X-Argus", base64.StdEncoding.EncodeToString(sec[:]))
	for k, v := range map[string]string{"Content-Encoding": "gzip", "X-Tt-Request-Tag": "t=0;n=1", "Log-Encode-Type": "gzip", "Passport-Sdk-Settings": "device_transfer_s_0,device_transfer_ab_1", "Passport-Sdk-Version": "60755"} {
		headers.Set(k, v)
	}
	return &Prepared{URL: base + "/service/2/device_register/?" + q, Payload: payload, Container: b, Query: q, Headers: headers}, nil
}

// Register sends a single request; a response is only successful with nonzero
// server-issued decimal IDs. It never converts an empty HTTP 200 into success.
func (c *Client) Register(ctx context.Context) (*Response, error) {
	prepared, err := c.Prepare(time.Now())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prepared.URL, bytes.NewReader(prepared.Payload))
	if err != nil {
		return nil, err
	}
	req.Header = prepared.Headers.Clone()
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("register: 请求失败: %w", err)
	}
	defer res.Body.Close()
	var reader io.Reader = res.Body
	if strings.EqualFold(res.Header.Get("Content-Encoding"), "gzip") {
		z, e := gzip.NewReader(res.Body)
		if e != nil {
			return nil, fmt.Errorf("register: gzip 响应无效: %w", e)
		}
		defer z.Close()
		reader = z
	}
	const limit = 4 << 20
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, errors.New("register: 响应超出大小限制")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var retry time.Duration
		if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
			retry = time.Duration(seconds) * time.Second
		} else if stamp, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
			retry = time.Until(stamp)
		}
		return nil, &HTTPStatusError{StatusCode: res.StatusCode, RetryAfter: retry}
	}
	if len(body) > 6 && bytes.HasPrefix(body, []byte{'t', 'c', 5, 0x10, 0, 0}) {
		body, err = hgcrypto.TTDecryptV5(body)
		if err != nil {
			return nil, err
		}
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("register: 未返回有效 JSON（%d 字节）", len(body))
	}
	if data := raw["data"]; len(data) > 0 {
		var nested map[string]json.RawMessage
		if json.Unmarshal(data, &nested) == nil {
			for k, v := range nested {
				if len(raw[k]) == 0 {
					raw[k] = v
				}
			}
		}
	}
	value := func(keys ...string) string {
		for _, k := range keys {
			v := raw[k]
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
			var n json.Number
			if json.Unmarshal(v, &n) == nil && n != "" {
				return string(n)
			}
		}
		return ""
	}
	id := Identity{DeviceID: value("device_id_str", "device_id"), InstallID: value("install_id_str", "install_id"), DeviceToken: value("device_token"), OdinTT: value("odin_tt"), StoreRegion: value("store_region"), TTReq: value("ttreq")}
	valid := func(s string) bool {
		if s == "" {
			return false
		}
		nonzero := false
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
			nonzero = nonzero || r != '0'
		}
		return nonzero
	}
	if !valid(id.DeviceID) || !valid(id.InstallID) {
		return nil, fmt.Errorf("register: 服务端未分配有效设备 ID（message=%s）", value("message", "description"))
	}
	return &Response{Identity: id, StatusCode: res.StatusCode, Cookies: res.Cookies()}, nil
}
