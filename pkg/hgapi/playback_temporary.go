package hgapi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync/atomic"
	"time"

	"duanju-desktop/pkg/api"
)

const (
	playbackFastRaceBudget    = 3
	temporaryPlaybackAttempts = 2
	temporaryPlaybackDelay    = 3 * time.Second
	temporaryPlaybackMaxDelay = time.Minute
	temporaryPlaybackWindow   = 2 * time.Minute
)

// A temporary session is scoped to one playback attempt.
type temporaryPlaybackSessionProvider interface {
	RegisterTemporaryPlaybackSession(context.Context) (*api.Session, error)
}

type temporaryPlayback struct {
	preferUntil atomic.Int64
	gate        chan struct{}
	// Only the goroutine holding gate may access these fields.
	nextAttempt time.Time
	failures    int
}

func (p *temporaryPlayback) cooldown() *PlaybackRegistrationCooldown {
	return &PlaybackRegistrationCooldown{Until: p.nextAttempt, Reason: "临时播放设备恢复间隔未到"}
}

func temporaryPlaybackSwitchable(err error) bool {
	var cooldown *PlaybackRegistrationCooldown
	return playbackSwitchable(err) || errors.As(err, &cooldown)
}

func (p *temporaryPlayback) fetch(ctx context.Context, provider temporaryPlaybackSessionProvider, body any) (*api.Result, error) {
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if time.Now().Before(p.nextAttempt) {
		return nil, p.cooldown()
	}

	for attempt := 0; ; attempt++ {
		p.nextAttempt = time.Now().Add(temporaryPlaybackDelay)
		session, err := provider.RegisterTemporaryPlaybackSession(ctx)
		if err == nil && session == nil {
			err = errors.New("临时播放设备注册响应为空")
		}
		if err == nil {
			host := ""
			if len(api.HostPool) > 0 {
				host = api.HostPool[attempt%len(api.HostPool)]
			}
			res, fetchErr := fetchTemporaryPlaybackHost(ctx, session, body, host)
			if fetchErr == nil {
				p.failures = 0
				return res, nil
			}
			if !playbackSwitchable(fetchErr) {
				return nil, fetchErr
			}
			err = fetchErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var blocked *PlaybackRegistrationCooldown
		if errors.As(err, &blocked) {
			p.nextAttempt = blocked.Until
			return nil, err
		}

		p.failures = min(p.failures+1, 6)
		delay := min(temporaryPlaybackDelay<<(p.failures-1), temporaryPlaybackMaxDelay)
		p.nextAttempt = time.Now().Add(delay)
		// Retry once in this request when the short delay fits. Longer delays
		// are carried in the response so the player can release the request.
		if attempt+1 == temporaryPlaybackAttempts || delay > temporaryPlaybackDelay {
			return nil, fmt.Errorf("临时播放设备暂不可用，%w: %w", p.cooldown(), err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func fetchTemporaryPlaybackHost(ctx context.Context, session *api.Session, body any, host string) (*api.Result, error) {
	res, err := fetchPlaybackHost(ctx, session, body, host, playbackHostTimeout)
	var business *api.BizError
	if !errors.As(err, &business) || business.Code != 101000 {
		return res, err
	}
	// A content-type retry can use the same device, even during registration cooldown.
	params, _ := body.(map[string]any)
	ids, _ := params["mixed_video_id_map"].(map[string][]string)
	if len(ids) != 1 || len(ids["1004"]) == 0 {
		return res, err
	}
	params = maps.Clone(params)
	params["mixed_video_id_map"] = map[string][]string{"1001": ids["1004"]}
	return fetchPlaybackHost(ctx, session, params, host, playbackHostTimeout)
}
