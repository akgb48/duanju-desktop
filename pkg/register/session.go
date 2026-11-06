package register

import (
	"strconv"

	"duanju-desktop/pkg/api"
)

// ApplySession uses server IDs and the same persisted profile for requests.
func ApplySession(sess *api.Session, p *Profile, id Identity, cookies string) {
	sess.SetDevice(api.DeviceContext{
		DeviceID: id.DeviceID, InstallID: id.InstallID, DeviceToken: id.DeviceToken,
		CDID: p.CDID, OpenUDID: p.OpenUDID, AID: strconv.Itoa(p.AID), Channel: p.Channel,
		AppName: p.AppName, Version: p.VersionName, VersionCode: strconv.Itoa(p.VersionCode),
	})
	for k, v := range map[string]string{
		"ac": p.Access, "device_platform": p.DevicePlatform, "os": "android", "ssmix": "a", "language": p.Language, "host_abi": p.CpuABI, "dragon_device_type": p.DeviceCategory,
		"compliance_status": "0", "need_personal_recommend": "1", "player_so_load": "1", "is_android_pad_screen": "0", "device_type": p.DeviceType, "device_brand": p.DeviceBrand, "os_api": strconv.Itoa(p.OSApi), "os_version": p.OSVersion,
		"manifest_version_code": strconv.Itoa(p.ManifestVersionCode), "resolution": p.QueryResolution, "dpi": strconv.Itoa(p.DPI), "update_version_code": strconv.Itoa(p.UpdateVersionCode), "pv_player": strconv.Itoa(p.VersionCode),
		"passport-sdk-version": "5051452", "use_new_token_expire_rule": "1", "cronet_version": p.CronetVersion, "ttnet_version": p.TTNetVersion, "use_store_region": "1",
	} {
		sess.SetParam(k, v)
	}
	sess.SetHeader("User-Agent", p.UserAgent())
	if cookies != "" {
		sess.MergeCookieString(cookies)
	}
}
