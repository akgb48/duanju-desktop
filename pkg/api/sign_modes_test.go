package api

import (
	"hongguo-desktop/pkg/crypto"
	"testing"
)

func TestProtocolRegistrySignatureModes(t *testing.T) {
	for action, want := range map[string]crypto.SignMode{"feed": crypto.SignMedusa, "search": crypto.SignMedusa, "video_detail": crypto.SignMedusa, "multi_video_model": crypto.SignFull, "send_code": crypto.SignFull, "sms_login": crypto.SignFull} {
		a, ok := DefaultRegistry.Get(action)
		if !ok {
			t.Fatal(action)
		}
		if got := requestSignMode(a); got != want {
			t.Errorf("%s mode=%d want %d", action, got, want)
		}
	}
}
