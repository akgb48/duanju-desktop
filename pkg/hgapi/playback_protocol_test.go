package hgapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"duanju-desktop/pkg/api"
)

func TestPlaybackUsesModelTypesAndFallsBackOnlyForTypeError(t *testing.T) {
	for _, detailType := range []int{0, 1, 1004} {
		t.Run(fmt.Sprint(detailType), func(t *testing.T) {
			var types []string
			s := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body struct {
					IDs map[string][]string `json:"mixed_video_id_map"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				for typ, ids := range body.IDs {
					types = append(types, typ)
					if len(ids) != 1 || ids[0] != "episode" {
						t.Fatalf("video identity changed: %v", body.IDs)
					}
				}
				response := `{"code":101000,"message":"type mismatch"}`
				if len(types) == 2 {
					response = `{"code":0,"data":{"episode":{"video_list":{"v":{"definition":"720p","main_url":"https://media.example/episode.mp4"}}}}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}))
			c := New(s)
			if detailType != 0 {
				c.contentTypes.Store("episode", detailType)
			}
			v, err := c.VideoDetail(context.Background(), "episode")
			if err != nil || v == nil || len(v.Sources) != 1 || strings.Join(types, ",") != "1004,1001" {
				t.Fatalf("model type routing failed: types=%v sources=%v err=%v", types, v, err)
			}
		})
	}
}
