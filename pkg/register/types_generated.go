// Device registration request structures.
package register

type Profile struct {
	OpenUDID             string       `json:"openudid"`
	ClientUDID           string       `json:"clientudid"`
	CDID                 string       `json:"cdid"`
	ReqID                string       `json:"req_id"`
	GoogleAID            string       `json:"google_aid"`
	UDID                 string       `json:"udid"`
	SerialNumber         string       `json:"serial_number"`
	BuildSerial          string       `json:"build_serial"`
	MacAddress           string       `json:"mac_address"`
	SimSerialNumber      []string     `json:"sim_serial_number"`
	DeviceBrand          string       `json:"device_brand"`
	DeviceManufacturer   string       `json:"device_manufacturer"`
	DeviceModel          string       `json:"device_model"`
	DeviceType           string       `json:"device_type"`
	DeviceCategory       string       `json:"device_category"`
	DevicePlatform       string       `json:"device_platform"`
	Hardware             string       `json:"hardware"`
	CpuABI               string       `json:"cpu_abi"`
	Resolution           string       `json:"resolution"`
	QueryResolution      string       `json:"query_resolution"`
	DensityDPI           int          `json:"density_dpi"`
	DPI                  int          `json:"dpi"`
	DisplayDensity       string       `json:"display_density"`
	OS                   string       `json:"os"`
	OSVersion            string       `json:"os_version"`
	OSApi                int          `json:"os_api"`
	ROM                  string       `json:"rom"`
	ROMVersion           string       `json:"rom_version"`
	BuildDisplay         string       `json:"build_display"`
	ReleaseBuild         string       `json:"release_build"`
	AID                  int          `json:"aid"`
	AppName              string       `json:"app_name"`
	DisplayName          string       `json:"display_name"`
	Package              string       `json:"package"`
	AppKey               string       `json:"appkey"`
	AppVersion           string       `json:"app_version"`
	AppVersionMinor      string       `json:"app_version_minor"`
	VersionName          string       `json:"version_name"`
	VersionCode          int          `json:"version_code"`
	UpdateVersionCode    int          `json:"update_version_code"`
	ManifestVersionCode  int          `json:"manifest_version_code"`
	Channel              string       `json:"channel"`
	SigHash              string       `json:"sig_hash"`
	GitHash              string       `json:"git_hash"`
	SDKVersion           string       `json:"sdk_version"`
	SDKTargetVersion     int          `json:"sdk_target_version"`
	SDKFlavor            string       `json:"sdk_flavor"`
	CronetVersion        string       `json:"cronet_version"`
	TTNetVersion         string       `json:"ttnet_version"`
	Language             string       `json:"language"`
	Region               string       `json:"region"`
	TzName               string       `json:"tz_name"`
	TzOffset             int          `json:"tz_offset"`
	Timezone             int          `json:"timezone"`
	Carrier              string       `json:"carrier"`
	MccMnc               string       `json:"mcc_mnc"`
	Access               string       `json:"access"`
	Host                 string       `json:"host"`
	OaidMaySupport       bool         `json:"oaid_may_support"`
	Custom               CustomFields `json:"custom"`
	APKFirstInstallTime  int64        `json:"apk_first_install_time"`
	FirstLaunchTimestamp int64        `json:"first_launch_timestamp"`
	NotRequestSender     int          `json:"not_request_sender"`
	GuestMode            int          `json:"guest_mode"`
	IsSystemApp          int          `json:"is_system_app"`
	IPv6List             []string     `json:"ipv6_list"`
}

type CustomFields struct {
	IsAndroidFold int    `json:"is_android_fold"`
	FilterWarn    int    `json:"filter_warn"`
	WebUA         string `json:"web_ua"`
	IsAndroidPad  int    `json:"is_android_pad"`
	ClientIPv4    string `json:"client_ipv4"`
}

type AppConfig struct {
	AID                 int
	AppName             string
	DisplayName         string
	Package             string
	AppKey              string
	VersionName         string
	VersionCode         int
	UpdateVersionCode   int
	ManifestVersionCode int
	Channel             string
	SigHash             string
	SDKVersion          string
	GitHash             string
	CronetVersion       string
	TTNetVersion        string
	SDKTargetVersion    int
	Language            string
	Region              string
	TzName              string
	TzOffset            int
	Timezone            int
	Carrier             string
	MccMnc              string
	Host                string
}

type HardwareTemplate struct {
	DeviceBrand        string
	DeviceManufacturer string
	DeviceModel        string
	DeviceType         string
	Hardware           string
	CpuABI             string
	Resolution         string
	DensityDPI         int
	DPI                int
	DisplayDensity     string
	OSVersion          string
	OSApi              int
	ROM                string
	ROMVersion         string
	BuildSerial        string
	BuildDisplay       string
	ReleaseBuild       string
}

type AppHeader struct {
	Access              string       `json:"access"`
	AID                 int          `json:"aid"`
	APKFirstInstallTime int64        `json:"apk_first_install_time"`
	AppVersion          string       `json:"app_version"`
	AppVersionMinor     string       `json:"app_version_minor"`
	CDID                string       `json:"cdid"`
	Channel             string       `json:"channel"`
	ClientUDID          string       `json:"clientudid"`
	CpuABI              string       `json:"cpu_abi"`
	Custom              CustomFields `json:"custom"`
	DensityDPI          int          `json:"density_dpi"`
	DeviceBrand         string       `json:"device_brand"`
	DeviceCategory      string       `json:"device_category"`
	DeviceManufacturer  string       `json:"device_manufacturer"`
	DeviceModel         string       `json:"device_model"`
	DevicePlatform      string       `json:"device_platform"`
	DisplayDensity      string       `json:"display_density"`
	DisplayName         string       `json:"display_name"`
	GitHash             string       `json:"git_hash"`
	GuestMode           int          `json:"guest_mode"`
	IPv6List            []string     `json:"ipv6_list"`
	IsSystemApp         int          `json:"is_system_app"`
	Language            string       `json:"language"`
	ManifestVersionCode int          `json:"manifest_version_code"`
	NotRequestSender    int          `json:"not_request_sender"`
	OaidMaySupport      bool         `json:"oaid_may_support"`
	OpenUDID            string       `json:"openudid"`
	OS                  string       `json:"os"`
	OSApi               int          `json:"os_api"`
	OSVersion           string       `json:"os_version"`
	Package             string       `json:"package"`
	Region              string       `json:"region"`
	ReleaseBuild        string       `json:"release_build"`
	ReqID               string       `json:"req_id"`
	Resolution          string       `json:"resolution"`
	ROM                 string       `json:"rom"`
	ROMVersion          string       `json:"rom_version"`
	SDKFlavor           string       `json:"sdk_flavor"`
	SDKTargetVersion    int          `json:"sdk_target_version"`
	SDKVersion          string       `json:"sdk_version"`
	SigHash             string       `json:"sig_hash"`
	SimSerialNumber     []string     `json:"sim_serial_number"`
	Timezone            int          `json:"timezone"`
	TzName              string       `json:"tz_name"`
	TzOffset            int          `json:"tz_offset"`
	UpdateVersionCode   int          `json:"update_version_code"`
	VersionCode         int          `json:"version_code"`
}

const userAgentFormat = "%s/%d (Linux; U; Android %s; %s_%s; %s; Build/%s; Cronet/TTNetVersion:%s QuicVersion:462f352c 2026-08-31)"
const webAgentFormat = "Mozilla/5.0 (Linux; Android %s; %s Build/%s; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/120.0.0.0 Mobile Safari/537.36"
