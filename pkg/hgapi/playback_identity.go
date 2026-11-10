package hgapi

import (
	"math"
	"strconv"

	"duanju-desktop/pkg/api"
)

const playbackIdentityModes = 4

type playbackIdentity struct {
	api.PlaybackIdentity
	mode int
}

func playbackIdentities(session *api.Session, borrowed, alternate string, preferred int) []playbackIdentity {
	base := borrowed
	if base == "" {
		base = derivedPlaybackInstallID(session.DeviceContext().DeviceID, 4096)
	}
	if base == "" {
		base = session.DeviceContext().InstallID
	}
	seen := map[string]bool{base: true}
	identities := []playbackIdentity{{PlaybackIdentity: api.PlaybackIdentity{InstallID: base}}}
	for mode := 1; mode < playbackIdentityModes; mode++ {
		iid, empty := playbackInstallID(session, alternate, mode)
		if (iid == "" && !empty) || seen[iid] {
			continue
		}
		seen[iid] = true
		identities = append(identities, playbackIdentity{api.PlaybackIdentity{InstallID: base, QueryInstallID: &iid}, mode})
	}
	for i := range identities {
		if identities[i].mode == preferred {
			identities[0], identities[i] = identities[i], identities[0]
			break
		}
	}
	return identities
}

// v1.2.16 first borrows an IID for the playback session (DID+4096 when no
// reserve is available), then varies only the query IID across hosts. The
// installation cookie keeps the session's base IID throughout these probes.
func playbackInstallID(session *api.Session, borrowed string, mode int) (string, bool) {
	switch mode {
	case 1:
		if borrowed != "" {
			return borrowed, false
		}
		return derivedPlaybackInstallID(session.DeviceContext().DeviceID, 4096), false
	case 2:
		return derivedPlaybackInstallID(session.DeviceContext().DeviceID, -4096), false
	case 3:
		return "", true
	default:
		return "", false
	}
}

func derivedPlaybackInstallID(deviceID string, delta int64) string {
	id, err := strconv.ParseInt(deviceID, 10, 64)
	if err != nil || id <= 0 || (delta > 0 && id > math.MaxInt64-delta) || id+delta <= 0 {
		return ""
	}
	return strconv.FormatInt(id+delta, 10)
}
