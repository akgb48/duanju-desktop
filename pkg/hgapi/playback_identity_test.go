package hgapi

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"duanju-desktop/pkg/api"
)

func TestPlaybackTriesReferenceIIDsOnOneHostAndRemembersTheWinner(t *testing.T) {
	previous := api.HostPool
	api.HostPool = []string{"player.example"}
	t.Cleanup(func() { api.HostPool = previous })
	var seen []string
	session := api.NewSession(api.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		iid := r.URL.Query().Get("iid")
		seen = append(seen, iid)
		if r.URL.Query().Get("device_id") != "7654321098765432101" {
			t.Error("IID probe replaced the registered device")
		}
		if iid != "" {
			return raceResponse(""), nil
		}
		cookie, err := r.Cookie("install_id")
		if err != nil || cookie.Value != "7654321098765436197" {
			t.Error("empty query IID removed the installation cookie")
		}
		return raceResponse(`{"code":0,"data":{"episode":{"video_list":{"v":{"main_url":"https://media.example/episode.mp4"}}}}}`), nil
	})}))
	session.SetDevice(api.DeviceContext{DeviceID: "7654321098765432101", InstallID: "registered-install"})
	session.SetAccount("user", "account-token", "sessionid=account-cookie")
	client := New(session)
	if _, err := client.callPlayback(context.Background(), videoModelParams("episode", 1004)); err != nil {
		t.Fatal(err)
	}
	want := []string{"7654321098765436197", "7654321098765428005", ""}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("reference IID fallback was skipped: got %v want %v", seen, want)
	}
	seen = nil
	if _, err := client.callPlayback(context.Background(), videoModelParams("episode", 1004)); err != nil || !reflect.DeepEqual(seen, []string{""}) {
		t.Fatalf("successful IID mode was not reused: %v %v", seen, err)
	}
	if session.DeviceContext().InstallID != "registered-install" || session.CookieString() != "sessionid=account-cookie" || session.Token() != "account-token" {
		t.Fatal("IID recovery changed registered account state")
	}
}

func TestPlaybackIIDDerivationRejectsInvalidOrOverflowingDevices(t *testing.T) {
	for _, input := range []struct {
		device string
		delta  int64
	}{
		{"", 4096}, {"not-a-device", 4096}, {"-1", 4096},
		{"9223372036854775807", 4096}, {"4096", -4096},
	} {
		if got := derivedPlaybackInstallID(input.device, input.delta); got != "" {
			t.Fatalf("invalid device produced an IID: %q", got)
		}
	}
}
