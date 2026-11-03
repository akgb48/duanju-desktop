package api

import (
	"encoding/json"
	"fmt"
	"strconv"

	"duanju-desktop/pkg/crypto"
)

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int8, int16, int32, int64:
		return fmt.Sprintf("%d", x)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", x)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case []string:
		out := ""
		for i, s := range x {
			if i > 0 {
				out += ","
			}
			out += s
		}
		return out
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}

// fingerprint 把 DeviceContext 转成签名层需要的最小指纹。
func (d DeviceContext) fingerprint() crypto.DeviceFingerprint {
	return crypto.DeviceFingerprint{
		DeviceID:  d.DeviceID,
		InstallID: d.InstallID,
		CDID:      d.CDID,
		OpenUDID:  d.OpenUDID,
		AID:       d.AID,
	}
}
