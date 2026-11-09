package service

import (
	"context"
	"testing"
	"time"

	"duanju-desktop/pkg/model"
)

func TestPlaybackWaitIsCancellableAndDifferentVideosAreIndependent(t *testing.T) {
	s := New(nil)
	unlock, err := s.lockVideo(context.Background(), "slow-neighbour")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.lockVideo(ctx, "slow-neighbour"); err != context.DeadlineExceeded {
		t.Fatalf("same-video wait ignored cancellation: %v", err)
	}
	otherCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	other, err := s.lockVideo(otherCtx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	other()
	if len(s.videoGates) != 1 {
		t.Fatal("completed/canceled video gates were retained")
	}
}

func TestPlaybackRechecksCacheAfterWaiting(t *testing.T) {
	s := New(nil) // Any accidental upstream fetch will fail the test.
	unlock, _ := s.lockVideo(context.Background(), "v1")
	result := make(chan *model.VideoDetail, 1)
	errors := make(chan error, 1)
	go func() {
		d, err := s.VideoDetail(context.Background(), "v1")
		result <- d
		errors <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		s.videoMu.Lock()
		waiting := s.videoGates["v1"].users == 2
		s.videoMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			unlock()
			t.Fatal("second caller never waited for the video gate")
		}
		time.Sleep(time.Millisecond)
	}
	want := &model.VideoDetail{VideoID: "v1"}
	s.Cache().Put("vd:v1", want)
	unlock()
	if got, err := <-result, <-errors; got != want || err != nil {
		t.Fatalf("waiter did not reuse filled cache: %p, %v", got, err)
	}
}
