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
	mu         sync.Mutex
	host       string
	goodUntil  time.Time
	badUntil   map[string]time.Time
	provider   PlaybackSessionProvider
	spares     map[string]*playbackRoutes
	iidCursor  int
	nextDevice int
	identity   int
}

const playbackEmptyHostDelay = 2 * time.Minute

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

func (r *playbackRoutes) markGood(host string) {
	r.markGoodIdentity(host, 0)
}

func (r *playbackRoutes) markGoodIdentity(host string, identity int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host = host
	r.identity = identity
	r.goodUntil = time.Now().Add(10 * time.Minute)
	delete(r.badUntil, host)
}

func (r *playbackRoutes) identityMode(attempt int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	preferred := 0
	if time.Now().Before(r.goodUntil) {
		preferred = r.identity
	}
	return (preferred + attempt) % playbackIdentityModes
}

func (r *playbackRoutes) deferHost(host string, delay time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.host == host {
		r.goodUntil = time.Time{}
	}
	if r.badUntil == nil {
		r.badUntil = make(map[string]time.Time)
	}
	r.badUntil[host] = time.Now().Add(delay)
}

func isEmptyPlaybackResponse(err error) bool {
	var empty *api.EmptyResponseError
	return errors.As(err, &empty)
}

func playbackSwitchable(err error) bool {
	var network net.Error
	var business *api.BizError
	return isEmptyPlaybackResponse(err) || errors.As(err, &network) ||
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
	if provider != nil {
		preferred, entries := provider.PlaybackSessions()
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
	identities := playbackIdentities(session, "", "", routes.identityMode(0))
	return fetchPlaybackIdentity(ctx, session, body, host, playbackPrefetchTimeout, identities[0].PlaybackIdentity)
}

func (c *Client) callPlayback(ctx context.Context, body any) (*api.Result, error) {
	c.playback.mu.Lock()
	provider := c.playback.provider
	c.playback.mu.Unlock()
	if provider == nil {
		return callPlaybackSession(ctx, c.sess, &c.playback, body)
	}
	temporaryProvider, hasTemporary := provider.(temporaryPlaybackSessionProvider)
	var temporaryErr error
	if hasTemporary && time.Now().UnixNano() < c.temporary.preferUntil.Load() {
		res, err := c.temporary.fetch(ctx, temporaryProvider, body)
		if err == nil {
			c.resetPlaybackCursor()
		}
		if err == nil || ctx.Err() != nil || !temporaryPlaybackSwitchable(err) {
			return res, err
		}
		temporaryErr = err
	}
	ordered, identities := c.playbackCandidates(provider)
	budget := playbackRaceBudget
	if hasTemporary {
		budget = playbackFastRaceBudget
		c.playback.mu.Lock()
		offset := c.playback.nextDevice % len(ordered)
		c.playback.mu.Unlock()
		ordered = append(ordered[offset:], ordered[:offset]...)
	}
	res, err := c.racePlaybackSessions(ctx, provider, ordered, body, budget, identities)
	if err == nil && res != nil {
		if hasTemporary {
			c.resetPlaybackCursor()
			c.temporary.preferUntil.Store(0)
		}
		return res, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !playbackSwitchable(err) {
		return nil, err
	}
	if hasTemporary {
		// A short race should still reach later saved devices on subsequent calls.
		c.playback.mu.Lock()
		c.playback.nextDevice = (c.playback.nextDevice + budget) % len(ordered)
		c.playback.mu.Unlock()
		if temporaryErr == nil {
			c.temporary.preferUntil.Store(time.Now().Add(temporaryPlaybackWindow).UnixNano())
			temporaryResult, recoveryErr := c.temporary.fetch(ctx, temporaryProvider, body)
			if recoveryErr == nil {
				c.resetPlaybackCursor()
			}
			if recoveryErr == nil || ctx.Err() != nil || !temporaryPlaybackSwitchable(recoveryErr) {
				return temporaryResult, recoveryErr
			}
			temporaryErr = recoveryErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.Join(err, temporaryErr)
	}
	return c.retryWithSavedPool(ctx, provider, body, identities, err)
}

func (c *Client) resetPlaybackCursor() {
	c.playback.mu.Lock()
	c.playback.nextDevice = 0
	c.playback.mu.Unlock()
}

func (c *Client) playbackCandidates(provider PlaybackSessionProvider) ([]PlaybackSessionEntry, []api.DeviceContext) {
	preferred, entries := provider.PlaybackSessions()
	retained := make(map[string]bool, len(entries))
	for _, entry := range entries {
		retained[entry.ID] = true
	}
	c.playback.mu.Lock()
	for id := range c.playback.spares {
		if !retained[id] {
			delete(c.playback.spares, id)
		}
	}
	c.playback.mu.Unlock()

	ordered := make([]PlaybackSessionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.ID != "" && entry.ID == preferred && entry.Session != nil {
			ordered = append(ordered, entry)
		}
	}
	ordered = append(ordered, PlaybackSessionEntry{Session: c.sess})
	for _, entry := range entries {
		if entry.ID != "" && entry.ID != preferred && entry.Session != nil {
			ordered = append(ordered, entry)
		}
	}
	return ordered, playbackDeviceIdentities(c.sess, entries)
}

// Providers without temporary registration still recover through their saved pool.
func (c *Client) retryWithSavedPool(ctx context.Context, provider PlaybackSessionProvider, body any, identities []api.DeviceContext, last error) (*api.Result, error) {
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
	identities := playbackIdentities(session, "", "", routes.identityMode(0))
	for attempt := range max(len(hosts), len(identities)) {
		host := hosts[attempt%len(hosts)]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A dead route must not consume the entire app request deadline.
		identity := identities[attempt%len(identities)]
		res, err := fetchPlaybackIdentity(ctx, session, body, host, playbackHostTimeout, identity.PlaybackIdentity)
		if err == nil {
			routes.markGoodIdentity(host, identity.mode)
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if !playbackSwitchable(err) {
			return nil, err
		}
		delay := 30 * time.Second
		if isEmptyPlaybackResponse(err) {
			delay = playbackEmptyHostDelay
		}
		routes.deferHost(host, delay)
	}
	return nil, last
}
