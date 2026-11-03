package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
)

// The compatibility layer retries only this explicit marker. Business denials,
// malformed data and local persistence errors remain actionable errors instead
// of turning into an endless loading screen. The cause stays available to Go.
func playbackRecoveryError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var empty *api.EmptyResponseError
	var network net.Error
	var cooldown *hgapi.PlaybackRegistrationCooldown
	var business *api.BizError
	hasCooldown := errors.As(err, &cooldown)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &empty) &&
		!errors.As(err, &network) && !hasCooldown &&
		!(errors.As(err, &business) && (business.HTTPCode >= 500 || business.Code == 110001)) {
		return err
	}
	retryAt := time.Now().Add(3 * time.Second)
	if cooldown != nil && cooldown.Until.After(retryAt) {
		// Check for usable reserves at least every 30 seconds while registration
		// itself keeps respecting Retry-After, including after app restarts.
		retryAt = cooldown.Until
		if latest := time.Now().Add(30 * time.Second); retryAt.After(latest) {
			retryAt = latest
		}
	}
	return fmt.Errorf("[HG_PLAYBACK_RECOVERY:%d] %w", retryAt.UnixMilli(), err)
}
