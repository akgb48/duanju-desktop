package api

import "duanju-desktop/pkg/crypto"

func requestSignMode(a *API) crypto.SignMode {
	if a.Route == RouteBase || a.Route == RouteUpdate {
		return crypto.SignNone
	}
	switch a.Action {
	case "multi_video_model":
		return crypto.SignFull
	case "send_code", "sms_login", "upsms_verify":
		return crypto.SignFull
	case "account_info", "user_info", "reading_history":
		return crypto.SignMedusa
	}
	if a.Route == RoutePassport {
		return crypto.SignPassport
	}
	return crypto.SignMedusa
}
