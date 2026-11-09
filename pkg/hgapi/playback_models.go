package hgapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/model"
)

const PlaybackBatchSize = 10

const playbackPrefetchTimeout = 2 * time.Second

type playbackModelEntry struct {
	raw     json.RawMessage
	group   []string
	expires time.Time
	added   time.Time
}

type playbackModelCache struct {
	mu        sync.Mutex
	fetchGate chan struct{}
	entries   map[string]playbackModelEntry
}

func (m *playbackModelCache) get(id string) (json.RawMessage, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	return e.raw, ok && time.Now().Before(e.expires)
}

func (m *playbackModelCache) put(id string, raw json.RawMessage, group []string, expires time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]playbackModelEntry)
	}
	now := time.Now()
	for key, e := range m.entries {
		if !now.Before(e.expires) {
			delete(m.entries, key)
		}
	}
	if _, exists := m.entries[id]; !exists && len(m.entries) >= 256 {
		oldestID, oldest := "", now
		for key, e := range m.entries {
			if e.added.Before(oldest) {
				oldestID, oldest = key, e.added
			}
		}
		delete(m.entries, oldestID)
	}
	m.entries[id] = playbackModelEntry{raw: raw, group: append([]string(nil), group...), expires: expires, added: now}
}

func (c *Client) ClearPlaybackModels() {
	c.models.mu.Lock()
	c.models.entries = nil
	c.models.mu.Unlock()
}

// CachedVideoDetail never fetches or waits for the playback gate. Speculative
// player preparation may use it after a bounded model prefetch, then give up.
func (c *Client) CachedVideoDetail(videoID string) (*model.VideoDetail, bool) {
	raw, ok := c.models.get(videoID)
	if !ok {
		return nil, false
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, false
	}
	d, err := decodePlaybackModel(data, videoID)
	return d, err == nil && d != nil && len(d.Sources) > 0
}

func (c *Client) PrefetchVideoModels(ctx context.Context, videoIDs []string) error {
	return c.prefetchVideoModels(ctx, videoIDs, "", false)
}

// TryPrefetchVideoModels uses spare time and existing sessions for speculative
// feed items. It neither queues behind playback nor waits for new devices.
func (c *Client) TryPrefetchVideoModels(ctx context.Context, videoIDs []string) error {
	ctx, cancel := context.WithTimeout(ctx, playbackPrefetchTimeout)
	defer cancel()
	return c.prefetchVideoModels(ctx, videoIDs, "", true)
}

// Feed prefetch can already have the foreground episode among a mixed-series
// batch. Do not block that playable cache entry on fetching nine unused peers.
func (c *Client) EnsurePlaybackWindow(ctx context.Context, videoID string, window []string) error {
	return c.prefetchVideoModels(ctx, window, videoID, false)
}

func (c *Client) prefetchVideoModels(ctx context.Context, videoIDs []string, required string, speculative bool) error {
	ids, seen := []string{}, map[string]bool{}
	for _, id := range videoIDs {
		if strings.TrimSpace(id) != "" && !seen[id] {
			ids, seen[id] = append(ids, id), true
			if len(ids) == PlaybackBatchSize {
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	complete := func() bool {
		if required != "" {
			_, ok := c.models.get(required)
			return ok
		}
		for _, id := range ids {
			if _, ok := c.models.get(id); !ok {
				return false
			}
		}
		return true
	}
	if complete() {
		return nil
	}
	if speculative {
		select {
		case c.models.fetchGate <- struct{}{}:
		default:
			return nil
		}
	} else {
		select {
		case c.models.fetchGate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { <-c.models.fetchGate }()
	if speculative {
		missing := ids[:0]
		for _, id := range ids {
			if _, ok := c.models.get(id); !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		ids = missing
	} else if complete() {
		return nil
	}
	params := func(typ int) map[string]any {
		p := videoModelParams(ids[0], typ)
		key := "1004"
		if typ == 1001 {
			key = "1001"
		}
		p["mixed_video_id_map"] = map[string][]string{key: ids}
		return p
	}
	fetch := func(typ int) (*api.Result, error) {
		if speculative {
			return c.callSpeculativePlayback(ctx, params(typ))
		}
		return c.callPlayback(ctx, params(typ))
	}
	res, err := fetch(1004)
	var business *api.BizError
	if errors.As(err, &business) && business.Code == 101000 {
		res, err = fetch(1001)
	}
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := res.Decode(&raw); err != nil {
		return err
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return err
	}
	stored := 0
	for _, id := range ids {
		m := locatePlaybackModel(data, id)
		if m == nil {
			continue
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			continue
		}
		expires := time.Now().Add(3 * time.Minute)
		if expiry := number(m, "url_expire", "expire_at"); expiry > 0 {
			expires = time.Unix(int64(expiry), 0).Add(-2 * time.Minute)
		}
		if !time.Now().Before(expires) {
			continue
		}
		c.models.put(id, encoded, ids, expires)
		stored++
	}
	if stored == 0 {
		return fmt.Errorf("播放接口未返回有效视频模型")
	}
	return nil
}

func (c *Client) RefreshVideoDetail(ctx context.Context, videoID string) (*model.VideoDetail, error) {
	c.models.mu.Lock()
	group := c.models.entries[videoID].group
	delete(c.models.entries, videoID)
	c.models.mu.Unlock()
	if len(group) == 0 {
		group = []string{videoID}
	}
	if err := c.PrefetchVideoModels(ctx, group); err != nil {
		return nil, err
	}
	return c.VideoDetail(ctx, videoID)
}
