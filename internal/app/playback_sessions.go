package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/crypto"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/register"
)

const (
	playbackAttemptInterval = 30 * time.Second
	playbackSuccessInterval = 5 * time.Minute
	playbackRegisterTimeout = 20 * time.Second
	playbackQuotaWindow     = 24 * time.Hour
	playbackPoolCapacity    = 40
	playbackRegistrationCap = 40
)

type playbackDeviceRecord struct {
	ID     string       `json:"id"`
	Device deviceRecord `json:"device"`
	Token  string       `json:"token,omitempty"`
}

type playbackSessionState struct {
	Version              int                    `json:"version"`
	Preferred            string                 `json:"preferred,omitempty"`
	Devices              []playbackDeviceRecord `json:"devices"`
	LastRegistration     time.Time              `json:"lastRegistration,omitempty"`
	LastAttempt          time.Time              `json:"lastAttempt,omitempty"`
	RegisterBlockedUntil time.Time              `json:"registerBlockedUntil,omitempty"`
	Registrations        []time.Time            `json:"registrations,omitempty"`
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
	lifetime       context.Context
	now            func() time.Time
	newProfile     func(time.Time) (*register.Profile, error)
	registerDevice func(context.Context, *register.Profile, *http.Client) (*register.Response, error)
}

func newPlaybackSessionPool(dir string, main *api.Session) *playbackSessionPool {
	lifetime, cancel := context.WithCancel(context.Background())
	return &playbackSessionPool{
		main: main, path: filepath.Join(dir, "playback-sessions.json"), now: time.Now, newProfile: register.NewProfile,
		lifetime: lifetime, cancel: cancel,
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
	var stored struct {
		playbackSessionState
		Devices []struct {
			playbackDeviceRecord
			CooldownUntil *time.Time `json:"cooldownUntil"`
			NeedsRecovery bool       `json:"needsRecovery"`
		} `json:"devices"`
	}
	if err = json.Unmarshal(b, &stored); err != nil {
		p.loadErr = err
		return
	}
	state := stored.playbackSessionState
	if state.Version != 1 {
		p.loadErr = errors.New("播放设备文件版本无效")
		return
	}
	seen := make(map[string]bool)
	seenDevice := make(map[string]bool)
	dirty := false
	devices := make([]playbackDeviceRecord, 0, len(stored.Devices))
	for _, item := range stored.Devices {
		if item.ID == "" || seen[item.ID] || !validPlaybackDevice(&item.Device) || seenDevice[item.Device.Identity.DeviceID] {
			continue
		}
		seen[item.ID] = true
		seenDevice[item.Device.Identity.DeviceID] = true
		if item.NeedsRecovery || (item.CooldownUntil != nil && !item.CooldownUntil.IsZero()) {
			dirty = true
			continue
		}
		dirty = dirty || item.CooldownUntil != nil
		item.Device.Cookies = anonymousPlaybackCookies(&item.Device, nil)
		devices = append(devices, item.playbackDeviceRecord)
	}
	if len(devices) > playbackPoolCapacity {
		devices = devices[len(devices)-playbackPoolCapacity:]
		dirty = true
	}
	state.Devices = devices
	if len(state.Registrations) == 0 && !state.LastRegistration.IsZero() {
		state.Registrations = []time.Time{state.LastRegistration}
	}
	slices.SortFunc(state.Registrations, time.Time.Compare)
	if n := len(state.Registrations); n > 0 && state.Registrations[n-1].After(state.LastRegistration) {
		state.LastRegistration = state.Registrations[n-1]
	}
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
	for i := range p.state.Devices {
		r := &p.state.Devices[i]
		entries = append(entries, hgapi.PlaybackSessionEntry{ID: r.ID, Session: p.sessionLocked(r)})
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
func (p *playbackSessionPool) checkpointLocked(r *playbackDeviceRecord) {
	if s := p.sessions[r.ID]; s != nil {
		r.Device.Cookies = s.CookieString()
		r.Device.Cookies = anonymousPlaybackCookies(&r.Device, nil)
		r.Token = s.Token()
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

func (p *playbackSessionPool) registrationReadyAtLocked(now time.Time) time.Time {
	p.state.Registrations = slices.DeleteFunc(p.state.Registrations, func(stamp time.Time) bool {
		return !stamp.After(now.Add(-playbackQuotaWindow))
	})
	until := p.state.LastAttempt.Add(playbackAttemptInterval)
	if stamp := p.state.LastRegistration.Add(playbackSuccessInterval); stamp.After(until) {
		until = stamp
	}
	if n := len(p.state.Registrations); n >= playbackRegistrationCap {
		stamp := p.state.Registrations[n-playbackRegistrationCap].Add(playbackQuotaWindow)
		if stamp.After(until) {
			until = stamp
		}
	}
	if p.state.RegisterBlockedUntil.After(until) {
		until = p.state.RegisterBlockedUntil
	}
	return until
}

func (p *playbackSessionPool) RegisterPlaybackSession(ctx context.Context) (hgapi.PlaybackSessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return hgapi.PlaybackSessionEntry{}, err
	}
	p.mu.Lock()
	if err := p.lifetime.Err(); err != nil {
		p.mu.Unlock()
		return hgapi.PlaybackSessionEntry{}, err
	}
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
	until := p.registrationReadyAtLocked(now)
	if now.Before(until) {
		reason := "播放设备注册间隔未到"
		if until.Equal(p.state.RegisterBlockedUntil) {
			reason = "注册服务暂时拒绝请求"
		}
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
	p.mu.Unlock()

	// Registration is shared by concurrent callers. A canceled waiter does not
	// cancel another viewer's registration; the operation itself is bounded.
	regCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), playbackRegisterTimeout)
	stop := context.AfterFunc(p.lifetime, cancel)
	defer stop()
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
	}
	if err == nil {
		p.state.LastRegistration = p.now()
		p.state.Registrations = append(p.state.Registrations, p.state.LastRegistration)
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
			if len(p.state.Devices) >= playbackPoolCapacity {
				oldest := p.state.Devices[0].ID
				delete(p.sessions, oldest)
				p.state.Devices = slices.Delete(p.state.Devices, 0, 1)
				if p.state.Preferred == oldest {
					p.state.Preferred = ""
				}
			}
			p.state.Devices = append(p.state.Devices, playbackDeviceRecord{ID: id, Device: record})
			index = len(p.state.Devices) - 1
		}
		flight.entry = hgapi.PlaybackSessionEntry{ID: id, Session: p.sessionLocked(&p.state.Devices[index])}
	}
	if saveErr := p.saveLocked(); err == nil && saveErr != nil {
		err = fmt.Errorf("保存播放备用会话失败: %w", saveErr)
	}
	flight.err = err
	p.inFlight = nil
	close(flight.done)
	p.mu.Unlock()
	return flight.entry, flight.err
}

func (p *playbackSessionPool) stop() {
	p.mu.Lock()
	p.cancel()
	flight := p.inFlight
	p.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
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
