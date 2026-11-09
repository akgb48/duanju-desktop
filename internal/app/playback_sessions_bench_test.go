package app

import (
	"fmt"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/register"
)

func BenchmarkPlaybackPoolHotPath(b *testing.B) {
	for _, action := range []string{"snapshot-40-devices", "reuse-winner"} {
		b.Run(action, func(b *testing.B) {
			p := newPlaybackSessionPool(b.TempDir(), api.NewSession())
			defer p.stop()
			profile, err := register.NewProfile(time.Now())
			if err != nil {
				b.Fatal(err)
			}
			p.loaded = true
			p.state.Version = 1
			for i := range 40 {
				p.state.Devices = append(p.state.Devices, playbackDeviceRecord{ID: fmt.Sprint(i), Device: deviceRecord{Profile: profile, Identity: register.Identity{DeviceID: fmt.Sprint(10000 + i), InstallID: fmt.Sprint(20000 + i)}}})
			}
			p.PlaybackSessions()
			p.PlaybackSessionUsed("0")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if action == "reuse-winner" {
					p.PlaybackSessionUsed("0")
				} else {
					p.PlaybackSessions()
				}
			}
		})
	}
}
