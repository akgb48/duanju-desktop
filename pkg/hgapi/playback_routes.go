package hgapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"hongguo-desktop/pkg/api"
)

type playbackRoutes struct {
	mu            sync.Mutex
	host          string
	goodUntil     time.Time
	cooldownUntil time.Time
	lastError     error
	retired       bool
	badUntil      map[string]time.Time
	emptyHosts    map[string]time.Time
	provider      PlaybackSessionProvider
	spares        map[string]*playbackRoutes
}

// Some upstreams return code=0 with data=null/{} instead of a blank HTTP body.
// Treat that as an empty playback response before marking a device healthy.
func validatePlaybackResult(res *api.Result, body any) error {
	params, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	groups, ok := params["mixed_video_id_map"].(map[string][]string)
	if !ok {
		return nil
	}
	if len(res.Envelope.Data) == 0 {
		return &api.EmptyResponseError{Action: res.Action}
	}
	raw, err := res.DecodeData()
	if err != nil {
		return err
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return err
	}
	for _, ids := range groups {
		for _, id := range ids {
			if locatePlaybackModel(data, id) != nil {
				return nil
			}
		}
	}
	return &api.EmptyResponseError{Action: res.Action}
}

// PlaybackSessionEntry is an independently registered anonymous device. Its
// session never replaces the signed-in account or primary device session.
type PlaybackSessionEntry struct {
	ID            string
	Session       *api.Session
	CooldownUntil time.Time
}

type playbackSessionFeedback interface {
	PlaybackSessionFailed(string, time.Time)
}

type PlaybackSessionProvider interface {
	PlaybackSessions() (preferred string, entries []PlaybackSessionEntry)
	RegisterPlaybackSession(context.Context) (PlaybackSessionEntry, error)
	PlaybackSessionUsed(string)
}

type PlaybackRegistrationCooldown struct {
	Until  time.Time
	Reason string
}

func (e *PlaybackRegistrationCooldown) Error() string {
	return fmt.Sprintf("%s，请在 %s 后重试", e.Reason, e.Until.Local().Format("15:04:05"))
}

func (c *Client) SetPlaybackSessionProvider(provider PlaybackSessionProvider) {
	c.playback.mu.Lock()
	c.playback.provider = provider
	c.playback.mu.Unlock()
}

func (c *Client) sparePlaybackRoutes(id string) *playbackRoutes {
	c.playback.mu.Lock()
	defer c.playback.mu.Unlock()
	if c.playback.spares == nil {
		c.playback.spares = make(map[string]*playbackRoutes)
	}
	if r := c.playback.spares[id]; r != nil {
		return r
	}
	r := &playbackRoutes{}
	c.playback.spares[id] = r
	return r
}

func (r *playbackRoutes) order(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	hosts := make([]string, 0, len(api.HostPool))
	add := func(host string) {
		for _, item := range hosts {
			if item == host {
				return
			}
		}
		hosts = append(hosts, host)
	}
	for _, host := range api.HostPool {
		if host == r.host && now.Before(r.goodUntil) {
			add(host)
		}
	}
	for _, host := range api.HostPool {
		if !now.Before(r.badUntil[host]) {
			add(host)
		}
	}
	for _, host := range api.HostPool {
		if now.Before(r.badUntil[host]) {
			add(host)
		}
	}
	return hosts
}

func playbackSwitchable(err error) bool {
	var empty *api.EmptyResponseError
	var network net.Error
	var business *api.BizError
	return errors.As(err, &empty) || errors.As(err, &network) ||
		(errors.As(err, &business) && (business.HTTPCode >= 500 || business.Code == 110001))
}

func (c *Client) callPlayback(ctx context.Context, body any) (*api.Result, error) {
	c.playback.mu.Lock()
	provider := c.playback.provider
	c.playback.mu.Unlock()
	if provider == nil {
		return callPlaybackSession(ctx, c.sess, &c.playback, body, 0)
	}
	preferred, entries := provider.PlaybackSessions()
	c.playback.mu.Lock()
	retained := make(map[string]bool, len(entries))
	for _, entry := range entries {
		retained[entry.ID] = true
	}
	for id := range c.playback.spares {
		if !retained[id] {
			delete(c.playback.spares, id)
		}
	}
	c.playback.mu.Unlock()
	ordered := []PlaybackSessionEntry{}
	for _, entry := range entries {
		if entry.ID == preferred && entry.Session != nil && preferred != "" {
			ordered = append(ordered, entry)
		}
	}
	ordered = append(ordered, PlaybackSessionEntry{Session: c.sess})
	for _, entry := range entries {
		if entry.ID != preferred && entry.ID != "" && entry.Session != nil {
			ordered = append(ordered, entry)
		}
	}
	var last error
	res, err := c.racePlaybackSessions(ctx, provider, ordered, body, playbackRaceBudget)
	if err == nil && res != nil {
		return res, nil
	}
	last = err
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !playbackSwitchable(err) {
		return nil, err
	}
	// Registration is bounded/shared by the provider. Prefer a ready reserve;
	// never block rotation on a new registration while old reserves are usable.
	tryAdded := func() (*api.Result, error, bool) {
		_, latest := provider.PlaybackSessions()
		added := []PlaybackSessionEntry{}
		for _, entry := range latest {
			if entry.ID == "" || entry.Session == nil || retained[entry.ID] || time.Now().Before(entry.CooldownUntil) {
				continue
			}
			retained[entry.ID] = true
			added = append(added, entry)
		}
		if len(added) > 0 {
			res, err := c.racePlaybackSessions(ctx, provider, added, body, playbackFreshRaceBudget)
			if err == nil && res != nil {
				return res, nil, true
			}
			if err != nil {
				last = err
				if ctx.Err() != nil || !playbackSwitchable(err) {
					return nil, err, true
				}
			}
		}
		return nil, nil, false
	}
	if res, err, done := tryAdded(); done {
		return res, err
	}
	entry, regErr := provider.RegisterPlaybackSession(ctx)
	var cooldown *PlaybackRegistrationCooldown
	if errors.As(regErr, &cooldown) {
		wait := time.Until(cooldown.Until)
		deadline, limited := ctx.Deadline()
		if wait > 0 && wait <= 15*time.Second && (!limited || time.Until(deadline) > wait+3*time.Second) {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			if res, err, done := tryAdded(); done {
				return res, err
			}
			entry, regErr = provider.RegisterPlaybackSession(ctx)
		}
	}
	if regErr == nil && entry.ID != "" && entry.Session != nil {
		res, err := c.racePlaybackSessions(ctx, provider, []PlaybackSessionEntry{entry}, body, playbackFreshRaceBudget)
		if err == nil && res != nil {
			return res, nil
		}
		if err != nil {
			last = err
			if ctx.Err() != nil || !playbackSwitchable(err) {
				return nil, err
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if last == nil {
		if regErr != nil {
			return nil, regErr
		}
		return nil, fmt.Errorf("播放设备池正在补充，请稍后重试")
	}
	if regErr != nil {
		return nil, fmt.Errorf("播放设备暂不可用，%w: %w", regErr, last)
	}
	return nil, last
}

func (c *Client) playbackSessionHealthy(routes *playbackRoutes) {
	routes.mu.Lock()
	routes.cooldownUntil, routes.lastError = time.Time{}, nil
	routes.emptyHosts = nil
	routes.mu.Unlock()
}

func (c *Client) playbackSessionFailed(provider PlaybackSessionProvider, id string, routes *playbackRoutes, err error, confirmed bool) {
	until := time.Now().Add(30 * time.Second)
	routes.mu.Lock()
	routes.cooldownUntil, routes.lastError = until, err
	var empty *api.EmptyResponseError
	remove := id != "" && confirmed && errors.As(err, &empty)
	if remove {
		routes.retired = true
	}
	routes.mu.Unlock()
	if feedback, ok := provider.(playbackSessionFeedback); ok && remove {
		feedback.PlaybackSessionFailed(id, until)
	}
}

func callPlaybackSession(ctx context.Context, session *api.Session, routes *playbackRoutes, body any, attemptLimit int) (*api.Result, error) {
	return callPlaybackSessionPass(ctx, session, routes, body, attemptLimit, nil)
}

func callPlaybackSessionPass(ctx context.Context, session *api.Session, routes *playbackRoutes, body any, attemptLimit int, tried map[string]bool) (*api.Result, error) {
	hosts := routes.order(time.Now())
	if tried != nil {
		filtered := make([]string, 0, len(hosts))
		for _, host := range hosts {
			if !tried[host] {
				filtered = append(filtered, host)
			}
		}
		hosts = filtered
	}
	var last error
	if attemptLimit > 0 && len(hosts) > attemptLimit {
		hosts = hosts[:attemptLimit]
	}
	if len(hosts) == 0 {
		hosts = []string{""}
	}
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tried != nil {
			tried[host] = true
		}
		// A dead route must not consume the entire app request deadline.
		timeout := 4 * time.Second
		if attemptLimit > 0 {
			timeout = 2 * time.Second
		}
		res, err := fetchPlaybackHost(ctx, session, body, host, timeout)
		if err == nil {
			routes.mu.Lock()
			routes.host, routes.goodUntil = host, time.Now().Add(10*time.Minute)
			delete(routes.badUntil, host)
			routes.mu.Unlock()
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if !playbackSwitchable(err) {
			return nil, err
		}
		routes.mu.Lock()
		if routes.host == host {
			routes.goodUntil = time.Time{}
		}
		routes.mu.Unlock()
		var empty *api.EmptyResponseError
		if errors.As(err, &empty) {
			routes.mu.Lock()
			if routes.badUntil == nil {
				routes.badUntil = make(map[string]time.Time)
			}
			routes.badUntil[host] = time.Now().Add(2 * time.Minute)
			routes.mu.Unlock()
		}
	}
	return nil, last
}
