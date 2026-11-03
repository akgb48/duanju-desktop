package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"hongguo-desktop/pkg/model"
)

const discoveryPageSize = 12

type discoverySourceCursor struct {
	Cursor     string `json:"c,omitempty"`
	Position   int    `json:"p,omitempty"`
	Done       bool   `json:"d,omitempty"`
	RetryRound int    `json:"r,omitempty"`
}
type discoveryCursor struct {
	Kind    string                   `json:"kind"`
	Round   int                      `json:"round"`
	Sources [3]discoverySourceCursor `json:"sources"`
	Seen    []string                 `json:"seen,omitempty"`
}

func decodeDiscoveryCursor(raw string) discoveryCursor {
	state := discoveryCursor{Kind: "discovery"}
	if strings.HasPrefix(raw, "{") && len(raw) <= 49152 {
		var parsed discoveryCursor
		if json.Unmarshal([]byte(raw), &parsed) == nil && parsed.Kind == "discovery" && len(parsed.Seen) <= 384 && parsed.Round >= 0 {
			valid := true
			for _, s := range parsed.Sources {
				if s.Position < 0 || s.Position > 1000000 {
					valid = false
				}
			}
			if valid {
				return parsed
			}
		}
	}
	return state
}

// Recommend mixes official new releases/hot-play rankings into the default
// feed. Other tabs keep their upstream type and follow-list semantics.
func (s *Service) Recommend(ctx context.Context, tab int, cursor string) (*model.FeedPage, error) {
	if tab != 0 && tab != 16 {
		return s.Feed(ctx, tab, cursor)
	}
	key := "discovery:" + cursor
	cached := func() *model.FeedPage {
		if v, ok := s.cache.Get(key); ok {
			if p, ok := v.(*model.FeedPage); ok {
				return p
			}
		}
		return nil
	}
	if p := cached(); p != nil {
		return p, nil
	}
	select {
	case s.discoveryGate <- struct{}{}:
		defer func() { <-s.discoveryGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p := cached(); p != nil {
		return p, nil
	}
	state := decodeDiscoveryCursor(cursor)
	state.Round++
	type result struct {
		index int
		page  *model.FeedPage
		err   error
	}
	ready := make(chan result, 3)
	for i := range state.Sources {
		source := &state.Sources[i]
		if source.Done && state.Round >= source.RetryRound {
			*source = discoverySourceCursor{}
		}
		if source.Done {
			ready <- result{index: i}
			continue
		}
		go func(index int, cursor string) {
			call, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			page, err := s.discoverySource(call, index, cursor)
			ready <- result{index: index, page: page, err: err}
		}(i, source.Cursor)
	}
	var pages [3]*model.FeedPage
	var items [3][]model.FeedItem
	var failures []error
	for range 3 {
		res := <-ready
		if res.err != nil {
			failures = append(failures, res.err)
			continue
		}
		pages[res.index] = res.page
		if res.page != nil {
			items[res.index] = append([]model.FeedItem(nil), res.page.Items...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The same drama can have no tag in the rank response but be explicitly
	// marked as a burst drama in the feed. Deduplication must retain that fact.
	bestTags := map[string]*model.TitleTag{}
	for _, list := range items {
		for _, item := range list {
			if item.Series == nil || item.Series.TitleTag == nil {
				continue
			}
			tag := item.Series.TitleTag
			if titleTagPriority(tag) > titleTagPriority(bestTags[item.SeriesID]) {
				bestTags[item.SeriesID] = tag
			}
		}
	}
	// The server's own burst/new/hot tags get priority within its general feed.
	sort.SliceStable(items[2], func(i, j int) bool { return discoveryPriority(items[2][i]) > discoveryPriority(items[2][j]) })
	seen := make(map[string]bool, len(state.Seen)+discoveryPageSize)
	for _, id := range state.Seen {
		seen[id] = true
	}
	next := func(index int) (model.FeedItem, bool) {
		position := &state.Sources[index].Position
		for *position < len(items[index]) {
			item := items[index][*position]
			(*position)++
			if item.Series == nil || item.Series.Person || item.SeriesID == "" || seen[item.SeriesID] {
				continue
			}
			if item.VideoID == "" && len(item.Series.Episodes) > 0 {
				item.VideoID = item.Series.Episodes[0].VideoID
				item.Episode = item.Series.Episodes[0].Index
			}
			if item.VideoID == "" {
				continue
			}
			seen[item.SeriesID] = true
			state.Seen = append(state.Seen, item.SeriesID)
			view := discoveryItem(item, index)
			if tag := bestTags[item.SeriesID]; tag != nil {
				view.Series.TitleTag = tag
				view.Series.Badge = tag.Text
			}
			return view, true
		}
		return model.FeedItem{}, false
	}
	out := &model.FeedPage{Items: []model.FeedItem{}, HasMore: true}
	for slot := 0; slot < discoveryPageSize; slot++ {
		added := false
		for attempt := 0; attempt < 3; attempt++ {
			if item, ok := next((slot + attempt) % 3); ok {
				out.Items = append(out.Items, item)
				added = true
				break
			}
		}
		if !added {
			break
		}
	}
	for i, page := range pages {
		if page == nil {
			continue
		}
		source := &state.Sources[i]
		if source.Position >= len(items[i]) {
			if page.HasMore && page.Cursor != "" && page.Cursor != source.Cursor {
				source.Cursor = page.Cursor
				source.Position = 0
			} else {
				source.Done = true
				source.RetryRound = state.Round + 10
			}
		}
	}
	if len(state.Seen) > 384 {
		state.Seen = state.Seen[len(state.Seen)-384:]
	}
	if len(out.Items) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	b, _ := json.Marshal(state)
	out.Cursor = string(b)
	out.TotalCount = len(out.Items)
	s.cache.Put(key, out)
	return out, nil
}

func (s *Service) discoverySource(ctx context.Context, index int, cursor string) (*model.FeedPage, error) {
	switch index {
	case 0:
		return s.NewSeries(ctx, cursor)
	case 1:
		key := "discovery-hot:" + cursor
		if v, ok := s.cache.Get(key); ok {
			if p, ok := v.(*model.FeedPage); ok {
				return p, nil
			}
		}
		page, err := s.hg.RankSelected(ctx, map[string]string{"rank": "ranklist_hot_play_sc"}, cursor)
		if err == nil {
			s.cache.Put(key, page)
		}
		return page, err
	default:
		return s.Feed(ctx, 16, cursor)
	}
}

func discoveryPriority(item model.FeedItem) int {
	if item.Series == nil || item.Series.TitleTag == nil {
		return 0
	}
	return titleTagPriority(item.Series.TitleTag)
}

func titleTagPriority(tag *model.TitleTag) int {
	if tag == nil {
		return 0
	}
	switch tag.Text {
	case "爆剧":
		return 4
	case "热剧", "热播":
		return 3
	case "新剧":
		return 2
	}
	return 1
}

func discoveryItem(item model.FeedItem, index int) model.FeedItem {
	copySeries := *item.Series
	item.Series = &copySeries
	names := []string{"new", "hot", "recommend"}
	item.RecommendSource = names[index]
	if index < 2 {
		text, color := "新剧", "#00AE83"
		if index == 1 {
			text, color = "热剧", "#F47A25"
		}
		if item.Series.TitleTag == nil {
			item.Series.TitleTag = &model.TitleTag{Text: text, Colors: []string{color}, Source: names[index]}
			item.Series.Badge = text
		}
	}
	if len(item.Reasons) == 0 {
		for _, tag := range item.Series.Tags {
			if len(item.Reasons) >= 4 {
				break
			}
			item.Reasons = append(item.Reasons, model.FeedReason{Text: tag, Highlight: strings.HasPrefix(tag, "第") && strings.HasSuffix(tag, "季")})
		}
	}
	return item
}
