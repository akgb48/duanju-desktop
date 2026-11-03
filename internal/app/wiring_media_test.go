package app

import (
	"bytes"
	"hongguo-desktop/pkg/model"
	"testing"
)

func TestVideoCandidatesKeepBackupAndContentKeyAtRequestedQuality(t *testing.T) {
	d := &model.VideoDetail{Sources: []model.PlaySource{{Quality: "1080p", URL: "https://high", BackupURL: "https://high-backup", ContentKey: []byte("high-key")}, {Quality: "720p", URL: "https://medium", BackupURL: "https://medium-backup", ContentKey: []byte("medium-key")}}}
	c := videoCandidates(d, "720p")
	if len(c) != 2 || c[0].URL != "https://medium" || c[1].URL != "https://medium-backup" || !bytes.Equal(c[1].Key, []byte("medium-key")) {
		t.Fatal("fallback mixed definitions or keys")
	}
	d.Sources[1].BackupURL = d.Sources[1].URL
	if len(videoCandidates(d, "720p")) != 1 {
		t.Fatal("duplicate URL retried")
	}
}
