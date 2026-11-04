package hgapi

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"duanju-desktop/pkg/api"
)

func TestSpeculativePrefetchSkipsBusyGateAndDoesNotRegister(t *testing.T) {
	calls := 0
	c := New(api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return raceResponse(""), nil
	})})))
	provider := &recoveryProviderFixture{regErr: errors.New("unexpected registration")}
	c.SetPlaybackSessionProvider(provider)
	c.models.fetchGate <- struct{}{}
	if err := c.TryPrefetchVideoModels(context.Background(), []string{"v1", "v2"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("speculative request queued behind playback: %d calls", calls)
	}
	<-c.models.fetchGate

	provider.preferred = "preferred"
	provider.entries = append(provider.entries, PlaybackSessionEntry{ID: "preferred", Session: c.sess.NewAnonymousSession()})
	old := api.HostPool
	api.HostPool = []string{"one.example"}
	t.Cleanup(func() { api.HostPool = old })
	if err := c.TryPrefetchVideoModels(context.Background(), []string{"v1", "v2"}); err == nil {
		t.Fatal("expected a failed playback probe")
	}
	if calls != 1 || provider.registrations != 0 {
		t.Fatalf("speculative probe altered the device pool: calls=%d registrations=%d", calls, provider.registrations)
	}
}

func TestBatchModelsAreReusedAndRefreshedAsAWindow(t *testing.T) {
	calls := 0
	ids := []string{}
	for i := 0; i < PlaybackBatchSize; i++ {
		ids = append(ids, fmt.Sprintf("v%d", i))
	}
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		var body struct {
			IDs map[string][]string `json:"mixed_video_id_map"`
		}
		if err := json.NewDecoder(gz).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body.IDs["1004"]; strings.Join(got, ",") != strings.Join(ids, ",") {
			t.Fatalf("batch was flattened, truncated or split: %v", body.IDs)
		}
		data := map[string]any{}
		for _, id := range ids {
			data[id] = map[string]any{"video_list": map[string]any{"v": map[string]any{"definition": "720p", "main_url": "https://cdn.example/" + id}}, "url_expire": time.Now().Add(time.Hour).Unix()}
		}
		encoded, _ := json.Marshal(map[string]any{"code": 0, "data": data})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})}))
	c := New(s)
	if err := c.PrefetchVideoModels(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		v, err := c.VideoDetail(context.Background(), id)
		if err != nil || len(v.Sources) != 1 {
			t.Fatalf("cached episode unavailable: %s %v", id, err)
		}
	}
	if calls != 1 {
		t.Fatalf("episode changes refetched models: %d", calls)
	}
	if _, err := c.RefreshVideoDetail(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("forced refresh did not reload its source window: %d", calls)
	}
}

func TestFeedPrefetchedEpisodeDoesNotRequireUncachedNeighborModels(t *testing.T) {
	calls := 0
	s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"foreground":{"video_list":{"v":{"main_url":"https://cdn.example/foreground"}}}}}`))}, nil
	})}))
	c := New(s)
	if err := c.PrefetchVideoModels(context.Background(), []string{"foreground"}); err != nil {
		t.Fatal(err)
	}
	c.models.fetchGate <- struct{}{}
	defer func() { <-c.models.fetchGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.EnsurePlaybackWindow(ctx, "foreground", []string{"foreground", "unwatched-neighbor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VideoDetail(ctx, "foreground"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached feed episode triggered %d model requests", calls)
	}
}
