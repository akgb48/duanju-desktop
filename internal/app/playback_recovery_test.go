package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/model"
)

func TestSwitchingVideoCancelsOldModelFetchAndReleasesGate(t *testing.T) {
	started := make(chan struct{})
	a := accountTestApp(t, func(r *http.Request) (*http.Response, error) {
		var request struct {
			IDs map[string][]string `json:"mixed_video_id_map"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		if request.IDs["1004"][0] == "old" {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return accountResponse(`{"code":0,"data":{"new":{"video_list":{"v":{"main_url":"https://cdn.example/new"}}}}}`), nil
	})
	svc, _ := a.service()
	svc.Cache().Put(playbackEpisodesPrefix+"series", []model.Episode{{VideoID: "old", Index: 1}, {VideoID: "new", Index: 2}})
	old := make(chan error, 1)
	go func() {
		_, err := a.PlayInfo("series", 1)
		old <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old playback never started")
	}
	next, err := a.PlayInfo("series", 2)
	if err != nil || next == nil || next.VideoID != "new" {
		t.Fatalf("new video blocked behind old fetch: %+v, %v", next, err)
	}
	select {
	case err := <-old:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("old playback was not canceled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old playback still occupied the fetch gate")
	}
}

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
			var request struct {
				IDs map[string][]string `json:"mixed_video_id_map"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.IDs["1004"]) != 1 {
				t.Fatalf("foreground playback must request one model: %v, %v", request.IDs, err)
			}
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
	// A one-episode neighbour uses the model cache and returns the same view
	// shape without another foreground request or another series-detail fetch.
	warmed, err := a.PrefetchPlayItems(seriesID, 3)
	if err != nil || warmed == nil || warmed.Episode != 3 || len(warmed.Sources) != 1 || details != 2 {
		t.Fatalf("cached neighbour was not prepared: %+v, %v", warmed, err)
	}
	missing, err := a.PrefetchPlayItems(seriesID, 4)
	if err != nil || missing != nil || details != 2 {
		t.Fatalf("speculative request fetched missing series metadata: %+v, %v", missing, err)
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
