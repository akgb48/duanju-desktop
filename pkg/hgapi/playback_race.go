package hgapi

import (
	"context"
	"errors"
	"time"

	"duanju-desktop/pkg/api"
)

const (
	playbackRaceBudget      = 30
	playbackFreshRaceBudget = 6
	playbackRaceParallel    = 4
	playbackRaceHedge       = 250 * time.Millisecond
)

type playbackRaceTarget struct {
	entry     PlaybackSessionEntry
	routes    *playbackRoutes
	hosts     []string
	installID string
	next      int
	stopped   bool
}

func (c *Client) racePlaybackSessions(ctx context.Context, provider PlaybackSessionProvider, entries []PlaybackSessionEntry, body any, budget int, identities []api.DeviceContext) (*api.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var last error
	var denied error
	targets := []*playbackRaceTarget{}
	for _, entry := range entries {
		if entry.Session == nil {
			continue
		}
		routes := &c.playback
		if entry.ID != "" {
			routes = c.sparePlaybackRoutes(entry.ID)
		}
		hosts := routes.order(time.Now())
		if len(hosts) == 0 {
			hosts = []string{""}
		}
		installID := c.borrowPlaybackInstallID(entry.Session, identities)
		targets = append(targets, &playbackRaceTarget{entry: entry, routes: routes, hosts: hosts, installID: installID})
	}
	if len(targets) == 0 {
		return nil, last
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		target *playbackRaceTarget
		host   string
		result *api.Result
		err    error
	}
	results := make(chan outcome, playbackRaceParallel)
	active, sent, nextTarget, width := 0, 0, 0, 1
	launch := func() bool {
		if sent >= budget || raceCtx.Err() != nil {
			return false
		}
		for range len(targets) {
			target := targets[nextTarget]
			nextTarget = (nextTarget + 1) % len(targets)
			if target.stopped || target.next >= len(target.hosts) {
				continue
			}
			host := target.hosts[target.next]
			emptyIID := target.next%2 == 1
			target.next++
			active++
			sent++
			go func() {
				res, err := fetchPlaybackHostWithInstallID(raceCtx, target.entry.Session, body, host, 4*time.Second, target.installID, emptyIID)
				select {
				case results <- outcome{target: target, host: host, result: res, err: err}:
				case <-raceCtx.Done():
				}
			}()
			return true
		}
		return false
	}
	launch()
	ticker := time.NewTicker(playbackRaceHedge)
	defer ticker.Stop()
	for active > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if width < playbackRaceParallel {
				width++
			}
		case done := <-results:
			active--
			target := done.target
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if done.err == nil {
				target.routes.mu.Lock()
				target.routes.host, target.routes.goodUntil = done.host, time.Now().Add(10*time.Minute)
				delete(target.routes.badUntil, done.host)
				target.routes.mu.Unlock()
				provider.PlaybackSessionUsed(target.entry.ID)
				return done.result, nil
			}
			last = done.err
			var business *api.BizError
			if errors.As(done.err, &business) {
				if business.Code == 101000 {
					return nil, done.err
				}
				if business.Code == 110001 {
					target.stopped = true
				}
			}
			if !playbackSwitchable(done.err) {
				target.stopped = true
				denied = done.err
			}
		}
		for active < width && launch() {
		}
	}
	if denied != nil {
		return nil, denied
	}
	return nil, last
}

// Transport work is isolated from pool feedback: canceled losers cannot retire
// a healthy device or overwrite the winning device's routing selection.
func fetchPlaybackHost(ctx context.Context, session *api.Session, body any, host string, timeout time.Duration) (*api.Result, error) {
	return fetchPlaybackHostWithInstallID(ctx, session, body, host, timeout, "", false)
}

func fetchPlaybackHostWithInstallID(ctx context.Context, session *api.Session, body any, host string, timeout time.Duration, installID string, emptyIID bool) (*api.Result, error) {
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := session.CallPlaybackJSONWithInstallID(attempt, body, host, installID, emptyIID)
	if err == nil && !res.Envelope.OK() {
		err = &api.BizError{Action: res.Action, Code: res.Envelope.Code, HTTPCode: res.Envelope.HTTPCode, Message: res.Envelope.Message, LogID: res.Envelope.LogID}
	}
	if err == nil {
		err = validatePlaybackResult(res, body)
	}
	return res, err
}
