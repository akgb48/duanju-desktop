package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
)

func TestPlayInfoReusesEpisodeListAndRefreshesWhenEpisodeIsMissing(t *testing.T) {
	const seriesID = "7654321098765432101"
	details := 0
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/novel/player/multi_video_detail/preload/v1":
			details++
			episodes := `{"vid":"7654321098765432102","vid_index":1},{"vid":"7654321098765432103","vid_index":2}`
			if details > 1 {
				episodes += `,{"vid":"7654321098765432104","vid_index":3}`
			}
			return accountResponse(`{"code":0,"data":{"` + seriesID + `":{"series_id":"` + seriesID + `","series_title":"测试剧","video_list":[` + episodes + `]}}}`), nil
		case "/novel/player/multi_video_model/v1/":
			return accountResponse(`{"code":0,"data":{
				"7654321098765432102":{"video_list":{"v":{"main_url":"https://cdn.example/1"}}},
				"7654321098765432103":{"video_list":{"v":{"main_url":"https://cdn.example/2"}}},
				"7654321098765432104":{"video_list":{"v":{"main_url":"https://cdn.example/3"}}}
			}}`), nil
		default:
			return nil, fmt.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	})
	if _, err := a.SeriesDetail(seriesID); err != nil {
		t.Fatal(err)
	}
	for episode := 1; episode <= 3; episode++ {
		detail, err := a.PlayInfo(seriesID, episode)
		if err != nil || detail.Episode != episode || len(detail.Sources) != 1 {
			t.Fatalf("episode %d: detail=%+v err=%v", episode, detail, err)
		}
		expected := 1
		if episode == 3 {
			expected = 2
		}
		if details != expected {
			t.Fatalf("episode %d: requested metadata %d times, want %d", episode, details, expected)
		}
	}
}

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
