package register

import (
	"testing"

	"hongguo-desktop/pkg/api"
)

func TestPlayerVersionUsesApplicationVersionRatherThanDisplayDensity(t *testing.T) {
	s := api.NewSession()
	ApplySession(s, &Profile{VersionCode: 73932, DPI: 460}, Identity{}, "")
	q := s.AppParams()
	if q["pv_player"] != "73932" || q["dpi"] != "460" {
		t.Fatalf("player version and display density were mixed: pv_player=%s dpi=%s", q["pv_player"], q["dpi"])
	}
}
