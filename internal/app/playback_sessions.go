package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hongguo-desktop/pkg/api"
	"hongguo-desktop/pkg/crypto"
	"hongguo-desktop/pkg/hgapi"
	"hongguo-desktop/pkg/register"
)

const (
	playbackSuccessInterval = 30 * time.Second
	playbackAttemptInterval = 30 * time.Second
	playbackRegisterTimeout = 20 * time.Second
	playbackPoolTarget      = 20
)

type playbackDeviceRecord struct {
	ID            string       `json:"id"`
	Device        deviceRecord `json:"device"`
	Token         string       `json:"token,omitempty"`
	CooldownUntil time.Time    `json:"cooldownUntil,omitempty"`
	LastUsed      time.Time    `json:"lastUsed,omitempty"`
	NeedsRecovery bool         `json:"needsRecovery,omitempty"`
}

type playbackSessionState struct {
	Version              int                    `json:"version"`
	Preferred            string                 `json:"preferred,omitempty"`
	Devices              []playbackDeviceRecord `json:"devices"`
	LastRegistration     time.Time              `json:"lastRegistration,omitempty"`
	LastAttempt          time.Time              `json:"lastAttempt,omitempty"`
	RegisterBlockedUntil time.Time              `json:"registerBlockedUntil,omitempty"`
	RetiredHashes        []string               `json:"retiredHashes,omitempty"`
}

type playbackRegistration struct {
	done  chan struct{}
	entry hgapi.PlaybackSessionEntry
	err   error
}

type playbackSessionPool struct {
	mu             sync.Mutex
	main           *api.Session
	path           string
	loaded         bool
	loadErr        error
	state          playbackSessionState
	inFlight       *playbackRegistration
	sessions       map[string]*api.Session
	cancel         context.CancelFunc
	workerDone     chan struct{}
	lifetime       context.Context
	wake           chan struct{}
	now            func() time.Time
	newProfile     func(time.Time) (*register.Profile, error)
	registerDevice func(context.Context, *register.Profile, *http.Client) (*register.Response, error)
}

func newPlaybackSessionPool(dir string, main *api.Session) *playbackSessionPool {
	return &playbackSessionPool{
		main: main, path: filepath.Join(dir, "playback-sessions.json"), now: time.Now, newProfile: register.NewProfile,
		wake: make(chan struct{}, 1),
		registerDevice: func(ctx context.Context, p *register.Profile, client *http.Client) (*register.Response, error) {
			return (&register.Client{Profile: p, HTTPClient: client}).Register(ctx)
		},
	}
}

func (p *playbackSessionPool) loadLocked() {
	if p.loaded {
		return
	}
	p.loaded = true
	p.state.Version = 1
	f, err := os.Open(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		p.loadErr = err
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		p.loadErr = err
		return
	}
	if len(b) > 2<<20 {
		p.loadErr = errors.New("播放设备文件过大")
		return
	}
	var state playbackSessionState
	if err = json.Unmarshal(b, &state); err != nil {
		p.loadErr = err
		return
	}
	if state.Version != 1 {
		p.loadErr = errors.New("播放设备文件版本无效")
		return
	}
	seen := make(map[string]bool)
	seenDevice := make(map[string]bool)
	dirty := false
	devices := make([]playbackDeviceRecord, 0, len(state.Devices))
	for _, item := range state.Devices {
		if item.ID == "" || seen[item.ID] || !validPlaybackDevice(&item.Device) || seenDevice[item.Device.Identity.DeviceID] {
			continue
		}
		seen[item.ID] = true
		seenDevice[item.Device.Identity.DeviceID] = true
		if item.NeedsRecovery || !item.CooldownUntil.IsZero() {
			rememberRetired(&state, item.Device.Identity.DeviceID)
			dirty = true
			continue
		}
		item.Device.Cookies = anonymousPlaybackCookies(&item.Device, nil)
		devices = append(devices, item)
	}
	state.Devices = devices
	preferredPresent := state.Preferred == ""
	for _, r := range devices {
		if r.ID == state.Preferred {
			preferredPresent = true
		}
	}
	if !preferredPresent {
		state.Preferred = ""
	}
	p.state = state
	if dirty {
		_ = f.Close()
		_ = p.saveLocked()
	}
}

func playbackDeviceHash(id string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(id))) }

func rememberRetired(state *playbackSessionState, deviceID string) {
	hash := playbackDeviceHash(deviceID)
	for _, item := range state.RetiredHashes {
		if item == hash {
			return
		}
	}
	state.RetiredHashes = append(state.RetiredHashes, hash)
	if len(state.RetiredHashes) > 80 {
		state.RetiredHashes = state.RetiredHashes[len(state.RetiredHashes)-80:]
	}
}

func validPlaybackDevice(r *deviceRecord) bool {
	return r != nil && r.Profile != nil && r.Profile.AID == 8662 && r.Profile.CDID != "" && r.Profile.OpenUDID != "" && validDeviceIdentity(r.Identity)
}

func (p *playbackSessionPool) saveLocked() error {
	b, err := json.MarshalIndent(p.state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".playback-sessions-*.json")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, p.path)
}

func (p *playbackSessionPool) sessionFor(r *deviceRecord) *api.Session {
	s := p.main.NewAnonymousSession()
	register.ApplySession(s, r.Profile, r.Identity, anonymousPlaybackCookies(r, nil))
	s.SetSigner(crypto.NewDefaultSigner())
	return s
}

func (p *playbackSessionPool) PlaybackSessions() (string, []hgapi.PlaybackSessionEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	if p.loadErr != nil {
		return "", nil
	}
	entries := make([]hgapi.PlaybackSessionEntry, 0, len(p.state.Devices))
	// Unused/new reserves precede older devices when the preferred one fails.
	for i := len(p.state.Devices) - 1; i >= 0; i-- {
		r := &p.state.Devices[i]
		entries = append(entries, hgapi.PlaybackSessionEntry{ID: r.ID, Session: p.sessionLocked(r), CooldownUntil: r.CooldownUntil})
	}
	return p.state.Preferred, entries
}

func (p *playbackSessionPool) sessionLocked(r *playbackDeviceRecord) *api.Session {
	if p.sessions == nil {
		p.sessions = make(map[string]*api.Session)
	}
	if s := p.sessions[r.ID]; s != nil {
		s.SyncAnonymousConnectionFrom(p.main)
		return s
	}
	s := p.sessionFor(&r.Device)
	s.SetToken(r.Token)
	p.sessions[r.ID] = s
	return s
}

// Persist successful anonymous device challenges independently of the account.
// Confirmed empty-response devices are removed instead of checkpointed.
func (p *playbackSessionPool) checkpointLocked(r *playbackDeviceRecord) {
	if s := p.sessions[r.ID]; s != nil {
		r.Device.Cookies = s.CookieString()
		r.Device.Cookies = anonymousPlaybackCookies(&r.Device, nil)
		r.Token = s.Token()
	}
}

func (p *playbackSessionPool) PlaybackSessionFailed(id string, _ time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	for i := range p.state.Devices {
		r := &p.state.Devices[i]
		if r.ID == id {
			rememberRetired(&p.state, r.Device.Identity.DeviceID)
			delete(p.sessions, id)
			p.state.Devices = append(p.state.Devices[:i], p.state.Devices[i+1:]...)
			if p.state.Preferred == id {
				p.state.Preferred = ""
			}
			_ = p.saveLocked()
			p.signal()
			return
		}
	}
}

func (p *playbackSessionPool) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *playbackSessionPool) PlaybackSessionUsed(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	if p.loadErr != nil {
		return
	}
	if id != "" {
		found := false
		for i := range p.state.Devices {
			r := &p.state.Devices[i]
			if r.ID == id {
				p.checkpointLocked(r)
				r.CooldownUntil = time.Time{}
				r.NeedsRecovery = false
				r.LastUsed = p.now()
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	p.state.Preferred = id
	// A persistence failure never replaces or clears the main account session.
	_ = p.saveLocked()
}

func (p *playbackSessionPool) RegisterPlaybackSession(ctx context.Context) (hgapi.PlaybackSessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return hgapi.PlaybackSessionEntry{}, err
	}
	p.mu.Lock()
	p.loadLocked()
	if p.loadErr != nil {
		err := p.loadErr
		p.mu.Unlock()
		return hgapi.PlaybackSessionEntry{}, fmt.Errorf("读取播放设备失败: %w", err)
	}
	if flight := p.inFlight; flight != nil {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return hgapi.PlaybackSessionEntry{}, ctx.Err()
		case <-flight.done:
			return flight.entry, flight.err
		}
	}
	now := p.now()
	until := p.state.LastAttempt.Add(playbackAttemptInterval)
	reason := "播放设备正在按间隔补充"
	if stamp := p.state.LastRegistration.Add(playbackSuccessInterval); stamp.After(until) {
		until = stamp
	}
	if p.state.RegisterBlockedUntil.After(until) {
		until = p.state.RegisterBlockedUntil
		reason = "注册服务暂时拒绝请求"
	}
	if now.Before(until) {
		p.mu.Unlock()
		return hgapi.PlaybackSessionEntry{}, &hgapi.PlaybackRegistrationCooldown{Until: until, Reason: reason}
	}
	p.state.LastAttempt = now
	if err := p.saveLocked(); err != nil {
		p.mu.Unlock()
		return hgapi.PlaybackSessionEntry{}, fmt.Errorf("保存播放设备尝试记录失败: %w", err)
	}
	flight := &playbackRegistration{done: make(chan struct{})}
	p.inFlight = flight
	lifetime := p.lifetime
	p.mu.Unlock()

	// Registration is shared by concurrent callers. A canceled waiter does not
	// cancel another viewer's registration; the operation itself is bounded.
	regCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), playbackRegisterTimeout)
	if lifetime != nil {
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
	}
	profile, err := p.newProfile(now)
	var response *register.Response
	if err == nil {
		response, err = p.registerDevice(regCtx, profile, p.main.NewAnonymousSession().HTTPClient())
	}
	cancel()
	var record deviceRecord
	if err == nil {
		if response == nil {
			err = errors.New("播放设备注册响应为空")
		} else {
			record = deviceRecord{Profile: profile, Identity: response.Identity}
		}
		if err == nil && !validPlaybackDevice(&record) {
			err = errors.New("播放设备注册结果无效")
		}
		if err == nil {
			record.Cookies = anonymousPlaybackCookies(&record, response.Cookies)
		}
	}
	p.mu.Lock()
	var status *register.HTTPStatusError
	if errors.As(err, &status) && (status.StatusCode == 429 || status.StatusCode == 401 || status.StatusCode == 403) {
		delay := 15 * time.Minute
		if status.StatusCode != 429 {
			delay = time.Hour
		}
		if status.RetryAfter > delay {
			delay = status.RetryAfter
		}
		p.state.RegisterBlockedUntil = p.now().Add(delay)
		_ = p.saveLocked()
	}
	if err == nil {
		stamp := p.now()
		p.state.LastRegistration = stamp
		for _, hash := range p.state.RetiredHashes {
			if hash == playbackDeviceHash(record.Identity.DeviceID) {
				err = errors.New("注册服务返回已失效的设备，等待下次补充")
				break
			}
		}
	}
	if err == nil {
		id := crypto.RandomUUID()
		index := -1
		for i := range p.state.Devices {
			if p.state.Devices[i].Device.Identity.DeviceID == record.Identity.DeviceID {
				index, id = i, p.state.Devices[i].ID
				break
			}
		}
		if index >= 0 {
			// A repeated server identity is one device, not a new reserve.
			p.state.Devices[index].Device = record
			p.state.Devices[index].Token = ""
			delete(p.sessions, id)
		} else {
			p.state.Devices = append(p.state.Devices, playbackDeviceRecord{ID: id, Device: record})
			index = len(p.state.Devices) - 1
		}
		flight.entry = hgapi.PlaybackSessionEntry{ID: id, Session: p.sessionLocked(&p.state.Devices[index])}
		if saveErr := p.saveLocked(); saveErr != nil {
			err = fmt.Errorf("保存播放备用会话失败: %w", saveErr)
		}
	}
	flight.err = err
	_ = p.saveLocked()
	p.inFlight = nil
	close(flight.done)
	p.mu.Unlock()
	return flight.entry, flight.err
}

// Maintain a ready reserve on first playback, replenishing it for as long as
// playback consumes devices. There is no daily or lifetime replacement quota.
// The target is a ready-stock watermark, not a registration allowance; the
// foreground and worker still share pacing and upstream backoff.
func (p *playbackSessionPool) start(ctx context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	ctx, p.cancel = context.WithCancel(ctx)
	p.lifetime = ctx
	p.workerDone = make(chan struct{})
	done := p.workerDone
	p.mu.Unlock()
	go func() {
		defer close(done)
		for {
			if ctx.Err() != nil {
				return
			}
			p.maintain(ctx)
			delay := p.nextMaintenanceDelay()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-p.wake:
				timer.Stop()
			case <-timer.C:
			}
		}
	}()
}

func (p *playbackSessionPool) nextMaintenanceDelay() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	until := p.state.LastAttempt.Add(playbackAttemptInterval)
	if t := p.state.LastRegistration.Add(playbackSuccessInterval); t.After(until) {
		until = t
	}
	if p.state.RegisterBlockedUntil.After(until) {
		until = p.state.RegisterBlockedUntil
	}
	delay := until.Sub(now)
	if delay < time.Second {
		delay = time.Second
	}
	if len(p.state.Devices) >= playbackPoolTarget || p.loadErr != nil || delay > time.Minute {
		delay = time.Minute
	}
	return delay
}

func (p *playbackSessionPool) maintain(ctx context.Context) {
	p.mu.Lock()
	p.loadLocked()
	ready := 0
	for _, r := range p.state.Devices {
		if !r.NeedsRecovery && !p.now().Before(r.CooldownUntil) {
			ready++
		}
	}
	needsReserve := p.loadErr == nil && ready < playbackPoolTarget
	p.mu.Unlock()
	if needsReserve {
		_, _ = p.RegisterPlaybackSession(ctx)
	}
}

func (p *playbackSessionPool) stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.workerDone
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (a *App) startPlaybackPool() {
	if a.playbackPool == nil {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.playbackPool.start(ctx)
}

func anonymousPlaybackCookies(r *deviceRecord, response []*http.Cookie) string {
	region := r.Identity.StoreRegion
	if region == "" {
		region = "cn-tj"
	}
	order := []string{"passport_csrf_token", "passport_csrf_token_default", "store-region", "store-region-src", "install_id"}
	values := map[string]string{"passport_csrf_token": "", "passport_csrf_token_default": "", "store-region": region, "store-region-src": "did", "install_id": r.Identity.InstallID}
	set := func(name, value string) {
		if accountPlaybackCookie(name) {
			return
		}
		if _, ok := values[name]; !ok {
			order = append(order, name)
		}
		values[name] = value
	}
	if r.Identity.OdinTT != "" {
		set("odin_tt", r.Identity.OdinTT)
	}
	if r.Identity.TTReq != "" {
		set("ttreq", r.Identity.TTReq)
	}
	req := http.Request{Header: http.Header{"Cookie": []string{r.Cookies}}}
	for _, cookie := range req.Cookies() {
		set(cookie.Name, cookie.Value)
	}
	for _, cookie := range response {
		if cookie == nil || accountPlaybackCookie(cookie.Name) {
			continue
		}
		if cookie.MaxAge < 0 {
			delete(values, cookie.Name)
			continue
		}
		set(cookie.Name, cookie.Value)
	}
	values["install_id"] = r.Identity.InstallID
	parts := make([]string, 0, len(order))
	seen := make(map[string]bool)
	for _, name := range order {
		value, ok := values[name]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		if cookie := (&http.Cookie{Name: name, Value: value}).String(); cookie != "" {
			parts = append(parts, cookie)
		}
	}
	return strings.Join(parts, "; ")
}

func accountPlaybackCookie(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "session") || strings.HasPrefix(name, "sid_") || strings.HasPrefix(name, "uid_") || name == "tt_token" || name == "x-tt-token" || strings.HasPrefix(name, "passport_auth") || strings.HasPrefix(name, "passport_assist")
}
