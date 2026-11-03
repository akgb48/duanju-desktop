package hgapi

import (
	"context"
	"errors"
	"time"

	"hongguo-desktop/pkg/api"
)

const (
	playbackRaceBudget      = 30
	playbackFreshRaceBudget = 6
	playbackRaceParallel    = 4
	playbackRaceHedge       = 250 * time.Millisecond
)

type playbackRaceTarget struct {
	entry    PlaybackSessionEntry
	routes   *playbackRoutes
	hosts    []string
	next     int
	pending  int
	stopped  bool
	finished bool
	last     error
	emptyErr error
}

func (c *Client) racePlaybackSessions(ctx context.Context, provider PlaybackSessionProvider, entries []PlaybackSessionEntry, body any, budget int) (*api.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var last error
	targets := []*playbackRaceTarget{}
	for _, entry := range entries {
		if entry.Session == nil {
			continue
		}
		routes := &c.playback
		if entry.ID != "" {
			routes = c.sparePlaybackRoutes(entry.ID)
		}
		routes.mu.Lock()
		cooling := routes.retired || time.Now().Before(routes.cooldownUntil) || time.Now().Before(entry.CooldownUntil)
		previous := routes.lastError
		routes.mu.Unlock()
		if cooling {
			if previous != nil {
				last = previous
			}
			continue
		}
		hosts := routes.order(time.Now())
		if len(hosts) == 0 {
			hosts = []string{""}
		}
		targets = append(targets, &playbackRaceTarget{entry: entry, routes: routes, hosts: hosts})
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
			target.next++
			target.pending++
			active++
			sent++
			go func() {
				res, err := fetchPlaybackHost(raceCtx, target.entry.Session, body, host, 4*time.Second)
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
			target.pending--
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if done.err == nil {
				target.routes.mu.Lock()
				target.routes.host, target.routes.goodUntil = done.host, time.Now().Add(10*time.Minute)
				delete(target.routes.badUntil, done.host)
				target.routes.mu.Unlock()
				c.playbackSessionHealthy(target.routes)
				provider.PlaybackSessionUsed(target.entry.ID)
				return done.result, nil
			}
			last, target.last = done.err, done.err
			if !playbackSwitchable(done.err) {
				return nil, done.err
			}
			var empty *api.EmptyResponseError
			if errors.As(done.err, &empty) {
				now := time.Now()
				target.routes.mu.Lock()
				if target.routes.badUntil == nil {
					target.routes.badUntil = map[string]time.Time{}
				}
				if target.routes.emptyHosts == nil {
					target.routes.emptyHosts = map[string]time.Time{}
				}
				target.routes.badUntil[done.host] = now.Add(2 * time.Minute)
				target.routes.emptyHosts[done.host] = now.Add(2 * time.Minute)
				for host, until := range target.routes.emptyHosts {
					if !now.Before(until) {
						delete(target.routes.emptyHosts, host)
					}
				}
				// Preserve failure removal, confirming on distinct hosts instead of
				// blocking every other reserve on a complete serial host sweep.
				confirmed := target.entry.ID != "" && len(target.routes.emptyHosts) >= min(3, len(target.hosts))
				target.routes.mu.Unlock()
				if confirmed {
					target.stopped = true
					target.emptyErr = done.err
				}
			}
			if !target.finished && target.pending == 0 && (target.stopped || target.next == len(target.hosts)) {
				failure := target.last
				if target.emptyErr != nil {
					failure = target.emptyErr
				}
				c.playbackSessionFailed(provider, target.entry.ID, target.routes, failure, target.emptyErr != nil)
				target.finished = true
			}
		}
		for active < width && launch() {
		}
	}
	return nil, last
}

// Transport work is isolated from pool feedback: canceled losers cannot retire
// a healthy device or overwrite the winning device's routing selection.
func fetchPlaybackHost(ctx context.Context, session *api.Session, body any, host string, timeout time.Duration) (*api.Result, error) {
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := session.CallPlaybackJSON(attempt, body, host)
	if err == nil && !res.Envelope.OK() {
		err = &api.BizError{Action: res.Action, Code: res.Envelope.Code, HTTPCode: res.Envelope.HTTPCode, Message: res.Envelope.Message, LogID: res.Envelope.LogID}
	}
	if err == nil {
		err = validatePlaybackResult(res, body)
	}
	return res, err
}
