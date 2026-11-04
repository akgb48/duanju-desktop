package hgapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"duanju-desktop/pkg/api"
)

type playbackRoutes struct {
	mu        sync.Mutex
	host      string
	goodUntil time.Time
	badUntil  map[string]time.Time
	provider  PlaybackSessionProvider
	spares    map[string]*playbackRoutes
	iidCursor int
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
	ID      string
	Session *api.Session
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
		if host == r.host && now.Before(r.goodUntil) && !now.Before(r.badUntil[host]) {
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

// Caller holds r.mu. A route that has just failed should not remain sticky.
func (r *playbackRoutes) deferHostLocked(host string, now time.Time, empty bool) {
	if r.host == host {
		r.goodUntil = time.Time{}
	}
	if r.badUntil == nil {
		r.badUntil = make(map[string]time.Time)
	}
	delay := 30 * time.Second
	if empty {
		delay = 2 * time.Minute
	}
	r.badUntil[host] = now.Add(delay)
}

func playbackSwitchable(err error) bool {
	var empty *api.EmptyResponseError
	var network net.Error
	var business *api.BizError
	return errors.As(err, &empty) || errors.As(err, &network) ||
		(errors.As(err, &business) && (business.HTTPCode >= 500 || business.Code == 110001))
}

func playbackDeviceIdentities(primary *api.Session, entries []PlaybackSessionEntry) []api.DeviceContext {
	identities := make([]api.DeviceContext, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Session != nil {
			identities = append(identities, entry.Session.DeviceContext())
		}
	}
	return append(identities, primary.DeviceContext())
}

func (c *Client) borrowPlaybackInstallID(session *api.Session, identities []api.DeviceContext) string {
	own := session.DeviceContext()
	c.playback.mu.Lock()
	defer c.playback.mu.Unlock()
	for range identities {
		index := c.playback.iidCursor % len(identities)
		c.playback.iidCursor = (index + 1) % len(identities)
		other := identities[index]
		if other.DeviceID != "" && other.DeviceID != "0" && other.DeviceID != own.DeviceID && other.InstallID != "" && other.InstallID != "0" {
			return other.InstallID
		}
	}
	return ""
}

// Speculative feed requests use at most one existing route. Their failure is
// not a reason to remove a device or mark the visible player's routes unhealthy.
func (c *Client) callSpeculativePlayback(ctx context.Context, body any) (*api.Result, error) {
	c.playback.mu.Lock()
	provider := c.playback.provider
	c.playback.mu.Unlock()
	session, routes := c.sess, &c.playback
	var identities []api.DeviceContext
	if provider != nil {
		preferred, entries := provider.PlaybackSessions()
		identities = playbackDeviceIdentities(c.sess, entries)
		for _, entry := range entries {
			if entry.ID != "" && entry.ID == preferred && entry.Session != nil {
				session, routes = entry.Session, c.sparePlaybackRoutes(entry.ID)
				break
			}
		}
	}
	hosts := routes.order(time.Now())
	host := ""
	if len(hosts) > 0 {
		host = hosts[0]
	}
	return fetchPlaybackHostWithInstallID(ctx, session, body, host, playbackPrefetchTimeout, c.borrowPlaybackInstallID(session, identities), false)
}

func (c *Client) callPlayback(ctx context.Context, body any) (*api.Result, error) {
	c.playback.mu.Lock()
	provider := c.playback.provider
	c.playback.mu.Unlock()
	if provider == nil {
		return callPlaybackSession(ctx, c.sess, &c.playback, body)
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
	ordered := make([]PlaybackSessionEntry, 0, len(entries)+1)
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
	identities := playbackDeviceIdentities(c.sess, entries)
	res, err := c.racePlaybackSessions(ctx, provider, ordered, body, playbackRaceBudget, identities)
	if err == nil && res != nil {
		return res, nil
	}
	last := err
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !playbackSwitchable(err) {
		return nil, err
	}
	entry, regErr := provider.RegisterPlaybackSession(ctx)
	if regErr == nil && entry.ID != "" && entry.Session != nil {
		identities = append(identities, entry.Session.DeviceContext())
		res, err := c.racePlaybackSessions(ctx, provider, []PlaybackSessionEntry{entry}, body, playbackFreshRaceBudget, identities)
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
		return nil, fmt.Errorf("暂无可用播放设备")
	}
	if regErr != nil {
		return nil, fmt.Errorf("播放设备暂不可用，%w: %w", regErr, last)
	}
	return nil, last
}

func callPlaybackSession(ctx context.Context, session *api.Session, routes *playbackRoutes, body any) (*api.Result, error) {
	hosts := routes.order(time.Now())
	var last error
	if len(hosts) == 0 {
		hosts = []string{""}
	}
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A dead route must not consume the entire app request deadline.
		res, err := fetchPlaybackHost(ctx, session, body, host, 4*time.Second)
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
		var empty *api.EmptyResponseError
		routes.mu.Lock()
		routes.deferHostLocked(host, time.Now(), errors.As(err, &empty))
		routes.mu.Unlock()
	}
	return nil, last
}
