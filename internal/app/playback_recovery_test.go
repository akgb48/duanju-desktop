package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"hongguo-desktop/pkg/api"
	"hongguo-desktop/pkg/hgapi"
)

func TestPlaybackRecoveryMarksTransientErrorsAndPreservesCauses(t *testing.T) {
	for _, cause := range []error{
		&api.EmptyResponseError{Action: "multi_video_model"},
		context.DeadlineExceeded,
		&api.BizError{HTTPCode: 502},
		&api.BizError{Code: 110001},
	} {
		err := playbackRecoveryError(cause)
		if !regexp.MustCompile(`^\[HG_PLAYBACK_RECOVERY:\d+\]`).MatchString(err.Error()) || !errors.Is(err, cause) {
			t.Fatalf("retry marker or cause missing: %v", err)
		}
	}
	for _, cause := range []error{context.Canceled, &api.BizError{HTTPCode: 403}, errors.New("invalid model JSON"), errors.New("disk full")} {
		if err := playbackRecoveryError(cause); err != cause {
			t.Fatalf("non-transient error would retry forever: %v", err)
		}
	}
}

func TestPlaybackRecoveryRetainsJoinedRegistrationWait(t *testing.T) {
	now := time.Now()
	cooldown := &hgapi.PlaybackRegistrationCooldown{Until: now.Add(20 * time.Second), Reason: "warming"}
	cause := fmt.Errorf("%w: %w", cooldown, &api.EmptyResponseError{Action: "multi_video_model"})
	err := playbackRecoveryError(cause)
	match := regexp.MustCompile(`^\[HG_PLAYBACK_RECOVERY:(\d+)\]`).FindStringSubmatch(err.Error())
	if len(match) != 2 {
		t.Fatal(err)
	}
	stamp, _ := strconv.ParseInt(match[1], 10, 64)
	if stamp != cooldown.Until.UnixMilli() {
		t.Fatalf("registration interval lost in joined error: %v", err)
	}
	cooldown.Until = now.Add(time.Hour)
	match = regexp.MustCompile(`^\[HG_PLAYBACK_RECOVERY:(\d+)\]`).FindStringSubmatch(playbackRecoveryError(cause).Error())
	stamp, _ = strconv.ParseInt(match[1], 10, 64)
	if stamp < now.Add(29*time.Second).UnixMilli() || stamp > time.Now().Add(31*time.Second).UnixMilli() {
		t.Fatal("long registration backoff should still check for ready reserves")
	}
}
