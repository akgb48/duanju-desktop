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
	playbackHostTimeout     = 4 * time.Second
)

type playbackRaceTarget struct {
	entry      PlaybackSessionEntry
	routes     *playbackRoutes
	hosts      []string
	identities []playbackIdentity
	next       int
	stopped    bool
}

func (c *Client) racePlaybackSessions(ctx context.Context, provider PlaybackSessionProvider, entries []PlaybackSessionEntry, body any, budget int, identities []api.DeviceContext) (*api.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets := make([]*playbackRaceTarget, 0, len(entries))
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
		alternate := c.borrowPlaybackInstallID(entry.Session, identities)
		targets = append(targets, &playbackRaceTarget{entry: entry, routes: routes, hosts: hosts, identities: playbackIdentities(entry.Session, installID, alternate, routes.identityMode(0))})
	}
	if len(targets) == 0 {
		return nil, nil
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		target *playbackRaceTarget
		host   string
		result *api.Result
		err    error
		mode   int
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
			if target.stopped || target.next >= max(len(target.hosts), len(target.identities)) {
				continue
			}
			host := target.hosts[target.next%len(target.hosts)]
			identity := target.identities[target.next%len(target.identities)]
			target.next++
			active++
			sent++
			go func() {
				res, err := fetchPlaybackIdentity(raceCtx, target.entry.Session, body, host, playbackHostTimeout, identity.PlaybackIdentity)
				select {
				case results <- outcome{target: target, host: host, result: res, err: err, mode: identity.mode}:
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
	var last, denied error
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
				cancel() // Stop losing probes before pool feedback can touch disk.
				target.routes.markGoodIdentity(done.host, done.mode)
				provider.PlaybackSessionUsed(target.entry.ID)
				return done.result, nil
			}
			last = done.err
			if isEmptyPlaybackResponse(done.err) {
				target.routes.deferHost(done.host, playbackEmptyHostDelay)
			} else if playbackSwitchable(done.err) {
				target.routes.deferHost(done.host, 30*time.Second)
			}
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
	return fetchPlaybackHostWithInstallID(ctx, session, body, host, timeout, "")
}

func fetchPlaybackHostWithInstallID(ctx context.Context, session *api.Session, body any, host string, timeout time.Duration, installID string) (*api.Result, error) {
	return fetchPlaybackIdentity(ctx, session, body, host, timeout, api.PlaybackIdentity{InstallID: installID})
}

func fetchPlaybackIdentity(ctx context.Context, session *api.Session, body any, host string, timeout time.Duration, identity api.PlaybackIdentity) (*api.Result, error) {
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := session.CallPlaybackJSONWithIdentity(attempt, body, host, identity)
	if err != nil {
		return res, err
	}
	if e := res.Envelope; !e.OK() {
		return res, &api.BizError{Action: res.Action, Code: e.Code, HTTPCode: e.HTTPCode, Message: e.Message, LogID: e.LogID}
	}
	return res, validatePlaybackResult(res, body)
}
