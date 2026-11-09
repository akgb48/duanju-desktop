package hgapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"duanju-desktop/pkg/media/cenc"
	"duanju-desktop/pkg/model"
)

// SeriesDetail uses the series endpoints identified in report 03 §2.3.
// A video_list contains episodes; playback definitions come from multi_video_model.
func (c *Client) SeriesDetail(ctx context.Context, seriesID string) (*model.Series, error) {
	if strings.TrimSpace(seriesID) == "" {
		return nil, fmt.Errorf("缺少剧集 ID")
	}
	var last error
	for _, action := range []string{"multi_video_detail", "video_detail"} {
		res, err := c.sess.CallJSON(ctx, action, map[string]any{"series_id": seriesID, "detail_page_version": 1, "disable_digg_stat": false, "need_all_video_definition": true, "need_mp4_align": true, "use_os_player": true, "use_server_dns": false})
		if err != nil {
			last = err
			continue
		}
		var raw json.RawMessage
		if err = res.Decode(&raw); err != nil {
			last = err
			continue
		}
		data, err := decodeAnyPreservingIDs(raw)
		if err != nil {
			last = err
			continue
		}
		root := findSeries(data, seriesID)
		if root == nil {
			last = fmt.Errorf("详情接口未返回剧集 %s", seriesID)
			continue
		}
		out := parseSeries(root, seriesID)
		if out.Title == "" && len(out.Episodes) == 0 {
			last = fmt.Errorf("剧集详情为空，可能需要有效设备注册或登录")
			continue
		}
		for _, episode := range out.Episodes {
			if out.ContentType > 0 {
				c.contentTypes.Store(episode.VideoID, out.ContentType)
			}
		}
		return out, nil
	}
	return nil, last
}

func findSeries(v any, id string) map[string]any {
	m, ok := v.(map[string]any)
	if ok {
		if child, ok := m[id]; ok {
			if out := findSeries(child, id); out != nil {
				return out
			}
		}
		for _, key := range []string{"video_data", "video_detail", "series", "series_info"} {
			if child, ok := m[key]; ok {
				if out := findSeries(child, id); out != nil {
					return out
				}
			}
		}
		if _, ok := m["video_list"]; ok {
			return m
		}
		if text(m, "series_id", "book_id", "id") == id && text(m, "series_title", "title", "book_name") != "" {
			return m
		}
		for _, key := range []string{"data", "series_list", "video_detail_list", "list"} {
			if child, ok := m[key]; ok {
				if out := findSeries(child, id); out != nil {
					return out
				}
			}
		}
	}
	if list, ok := v.([]any); ok {
		for _, child := range list {
			if out := findSeries(child, id); out != nil {
				return out
			}
		}
	}
	return nil
}

func parseSeries(m map[string]any, id string) *model.Series {
	info := m
	for _, key := range []string{"base_info", "series_info"} {
		if x, ok := m[key].(map[string]any); ok {
			info = mergeMaps(m, x)
			break
		}
	}
	out := &model.Series{ID: first(text(info, "series_id_str", "series_id", "book_id", "id"), id), Title: text(info, "series_title", "title", "book_name"), Cover: text(info, "series_cover", "cover", "cover_url", "thumb_url"), Description: text(info, "series_intro", "video_desc", "sub_title", "abstract", "description", "intro"), Author: text(info, "author", "author_name"), Category: text(info, "category", "category_name"), EpisodeCnt: number(info, "episode_cnt", "episode_count", "chapter_number"), ContentType: number(info, "content_type", "video_content_type"), Favorited: truth(info, "is_favorite", "is_collected", "favorited"), Liked: truth(info, "is_digg", "is_digged", "liked"), Episodes: []model.Episode{}}
	list, _ := m["video_list"].([]any)
	out.Score = text(info, "score")
	out.SeasonIndex = number(info, "season_index")
	out.MultiSeason = truth(info, "multi_season", "is_multi_season")
	out.SubInfo = text(info, "sub_title")
	if tag, ok := info["tag_info"].(map[string]any); ok {
		out.Badge = text(tag, "text")
		out.TitleTag = parseTitleTag(tag)
	} else if raw := text(info, "recommend_reason_list"); raw != "" {
		var reason map[string]any
		if json.Unmarshal([]byte(raw), &reason) == nil {
			if tag, ok := reason["tag_info"].(map[string]any); ok {
				out.TitleTag = parseTitleTag(tag)
				out.Badge = text(tag, "text")
			}
		}
	}
	out.PlayCount = int64(number(info, "series_play_cnt", "play_cnt", "play_count"))
	if rec, ok := info["rec_text_item"].(map[string]any); ok {
		out.RankText = text(rec, "RecommendText", "recommend_text")
	}
	if details, ok := info["secondary_info_list"].([]any); ok && out.RankText == "" {
		for _, v := range details {
			if entry, ok := v.(map[string]any); ok {
				out.RankText = text(entry, "content")
				if out.RankText != "" {
					break
				}
			}
		}
	}
	if subtitles, ok := info["sub_title_list"].([]any); ok {
		for _, v := range subtitles {
			if entry, ok := v.(map[string]any); ok {
				value := text(entry, "content")
				if number(entry, "data_type") == 23 {
					out.Cast = append(out.Cast, value)
				} else if value != "" && number(entry, "data_type") != 27 {
					out.Tags = append(out.Tags, value)
				}
			}
		}
	}
	if schema := text(info, "category_schema"); schema != "" {
		var categories []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(schema), &categories) == nil {
			for _, entry := range categories {
				if out.Category == "" {
					out.Category = entry.Name
				}
				if entry.Name == "" || len(out.Tags) >= 4 {
					continue
				}
				found := false
				for _, tag := range out.Tags {
					if tag == entry.Name {
						found = true
						break
					}
				}
				if !found {
					out.Tags = append(out.Tags, entry.Name)
				}
			}
		}
	}
	for idx, v := range list {
		e, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if child, ok := e["video_data"].(map[string]any); ok {
			e = mergeMaps(e, child)
		}
		vid := text(e, "video_id", "vid", "item_id")
		if vid == "" {
			continue
		}
		index := number(e, "vid_index", "video_index", "episode_index", "index")
		if index < 1 {
			index = idx + 1
		}
		out.Episodes = append(out.Episodes, model.Episode{SeriesID: out.ID, Index: index, VideoID: vid, Title: first(text(e, "title", "video_title", "name"), fmt.Sprintf("第%d集", index)), Duration: number(e, "duration"), Cover: text(e, "cover", "thumb_url"), IsFree: !truth(e, "is_locked", "locked")})
	}
	if out.EpisodeCnt == 0 {
		out.EpisodeCnt = len(out.Episodes)
	}
	sort.SliceStable(out.Episodes, func(i, j int) bool { return out.Episodes[i].Index < out.Episodes[j].Index })
	return out
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
func first(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
func text(m map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := m[key].(type) {
		case string:
			if v != "" {
				return v
			}
		case json.Number:
			return string(v)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case uint64:
			return strconv.FormatUint(v, 10)
		}
	}
	return ""
}
func number(m map[string]any, keys ...string) int {
	s := text(m, keys...)
	v, _ := strconv.ParseFloat(s, 64)
	return int(v)
}
func truth(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok {
			return v
		}
		s := text(m, k)
		if s != "" {
			return s == "1" || s == "true"
		}
	}
	return false
}

// decodePlaybackModel parses a JSON-string video_model and base64 encoded CDN URLs.
func decodePlaybackModel(raw any, videoID string) (*model.VideoDetail, error) {
	m := locatePlaybackModel(raw, videoID)
	return parsePlaybackSources(m, videoID)
}

func locatePlaybackModel(raw any, videoID string) map[string]any {
	var locate func(any) map[string]any
	locate = func(v any) map[string]any {
		if str, ok := v.(string); ok {
			var decoded any
			dec := json.NewDecoder(strings.NewReader(str))
			dec.UseNumber()
			if dec.Decode(&decoded) == nil {
				return locate(decoded)
			}
			return nil
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if x, ok := m[videoID]; ok {
			if found := locate(x); found != nil {
				return found
			}
		}
		if x, ok := m["video_model"]; ok {
			if found := locate(x); found != nil {
				return found
			}
		}
		if _, ok := m["video_list"]; ok {
			return m
		}
		if x, ok := m["data"]; ok {
			return locate(x)
		}
		return nil
	}
	return locate(raw)
}

func parsePlaybackSources(m map[string]any, videoID string) (*model.VideoDetail, error) {
	if m == nil {
		return nil, fmt.Errorf("播放接口未返回 video_model，可能需要有效设备注册或登录")
	}
	out := &model.VideoDetail{VideoID: videoID, Duration: number(m, "video_duration", "duration"), Sources: []model.PlaySource{}, ExpireAt: int64(number(m, "url_expire", "expire_at"))}
	seed, seedErr := cenc.DecodeSeed(text(m, "key_seed"))
	if seedErr != nil {
		return nil, fmt.Errorf("解析 key_seed 失败: %w", seedErr)
	}
	var sourceErr error
	decodeURL := func(s string) (string, error) {
		s = cenc.CleanEscapedURL(s)
		if s == "" {
			return "", nil
		}
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			decoded, err := cenc.DecodeSeed(s)
			if err != nil {
				return "", err
			}
			if len(decoded) > 3 && decoded[0] == 0xa8 && decoded[2] == 1 && decoded[3] == 0 {
				s, err = cenc.DecryptSpadeURL(s, seed)
				if err != nil {
					return "", err
				}
			} else {
				s = string(decoded)
			}
		}
		s = cenc.CleanEscapedURL(s)
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "", fmt.Errorf("播放地址不是有效的 HTTP URL")
		}
		return s, nil
	}
	appendSource := func(v any) {
		s, ok := v.(map[string]any)
		if !ok {
			return
		}
		s = normalizePlaybackItem(s)
		if strings.Contains(strings.ToLower(text(s, "codec_type", "codec")), "bytevc2hevc") {
			return
		}
		u, err := decodeURL(text(s, "main_url", "url"))
		backup, backupErr := decodeURL(text(s, "backup_url_1", "backup_url"))
		if u == "" {
			u = backup
			backup = ""
			if backupErr != nil {
				sourceErr = backupErr
			}
		}
		if u == "" {
			if err != nil {
				sourceErr = err
			}
			return
		}
		keyText := first(text(s, "spade_a"), text(m, "spade_a"))
		var key []byte
		if keyText != "" {
			key, err = cenc.DeriveContentKey(keyText)
			if err != nil {
				sourceErr = fmt.Errorf("派生内容密钥失败: %w", err)
				return
			}
		}
		q := first(text(s, "definition", "quality"), strconv.Itoa(number(s, "height", "vheight"))+"p")
		out.Sources = append(out.Sources, model.PlaySource{Quality: q, URL: u, BackupURL: backup, ContentKey: key, Codec: text(s, "codec_type", "codec"), Width: number(s, "width", "vwidth"), Height: number(s, "height", "vheight"), Bitrate: number(s, "bitrate", "bit_rate"), Size: int64(number(s, "size")), Format: first(text(s, "format"), "mp4")})
	}
	switch list := m["video_list"].(type) {
	case map[string]any:
		keys := make([]string, 0, len(list))
		for k := range list {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			appendSource(list[k])
		}
	case []any:
		for _, v := range list {
			appendSource(v)
		}
	}
	if len(out.Sources) == 0 {
		if sourceErr != nil {
			return nil, fmt.Errorf("解析播放源失败: %w", sourceErr)
		}
		return nil, fmt.Errorf("未找到可播放的 HTTP 源")
	}
	sort.SliceStable(out.Sources, func(i, j int) bool { return out.Sources[i].Height > out.Sources[j].Height })
	return out, nil
}

func (c *Client) playbackWithFallback(ctx context.Context, raw any, videoID string) (*model.VideoDetail, error) {
	m := locatePlaybackModel(raw, videoID)
	if m == nil {
		return parsePlaybackSources(nil, videoID)
	}
	fallback := text(m, "fallback_api")
	if nested, ok := m["fallback_api"].(map[string]any); ok {
		fallback = text(nested, "fallback_api")
	}
	if fallback == "" {
		return parsePlaybackSources(m, videoID)
	}
	b, err := c.sess.FetchMetadata(ctx, fallback)
	if err != nil {
		return nil, fmt.Errorf("获取播放元数据失败: %w", err)
	}
	v, err := decodeAnyPreservingIDs(b)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("fallback_api 返回格式错误")
	}
	if nested, ok := obj["video_info"].(map[string]any); ok {
		obj = nested
	}
	if nested, ok := obj["data"].(map[string]any); ok {
		obj = nested
	}
	// Definitions in multi_video_model carry spade_a; fallback records carry
	// the URLs. Preserve both when the fallback list replaces video_list.
	modelByName := map[string]map[string]any{}
	addProtocol := func(name string, value any) {
		if v, ok := value.(map[string]any); ok {
			v = normalizePlaybackItem(v)
			modelByName[name] = v
			if definition := text(v, "definition"); definition != "" {
				modelByName[definition] = v
			}
		}
	}
	switch list := m["video_list"].(type) {
	case map[string]any:
		for name, v := range list {
			addProtocol(name, v)
		}
	case []any:
		for i, v := range list {
			addProtocol(strconv.Itoa(i), v)
		}
	}
	if fallbackList, ok := obj["video_list"].(map[string]any); ok {
		for name, value := range fallbackList {
			if b, ok := value.(map[string]any); ok {
				a := modelByName[text(b, "definition")]
				if a == nil {
					a = modelByName[name]
				}
				if a != nil {
					fallbackList[name] = mergeMaps(a, b)
				}
			}
		}
	}
	obj = mergeMaps(m, obj)
	return parsePlaybackSources(obj, videoID)
}

func normalizePlaybackItem(s map[string]any) map[string]any {
	if meta, ok := s["video_meta"].(map[string]any); ok {
		s = mergeMaps(s, meta)
	}
	if encryption, ok := s["encrypt_info"].(map[string]any); ok {
		s = mergeMaps(s, encryption)
	}
	return s
}

func decodeAnyPreservingIDs(data []byte) (any, error) {
	var v any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err := decoder.Decode(&v)
	return v, err
}

// parseVideoFeed selects video_data records. Novel book_data must never become
// a playable short drama merely because both use a numeric book/series ID.
func (c *Client) parseVideoFeed(raw []byte) (*model.FeedPage, error) {
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	out := &model.FeedPage{Items: []model.FeedItem{}}
	seen := map[string]bool{}
	hasBooks := false
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, child := range x {
				walk(child)
			}
		case map[string]any:
			if actors, ok := x["ugc_user_data"].([]any); ok {
				for _, actor := range actors {
					entry, ok := actor.(map[string]any)
					if !ok {
						continue
					}
					user, _ := entry["user_info"].(map[string]any)
					base, _ := user["base_info"].(map[string]any)
					id := text(base, "actor_id_str", "actor_id", "user_id")
					if id == "" || seen["person:"+id] {
						continue
					}
					seen["person:"+id] = true
					person := &model.Series{ID: id, Title: text(base, "user_name"), Cover: text(base, "user_avatar"), Description: text(entry, "intro"), Person: true}
					if rec, ok := entry["rec_text_item"].(map[string]any); ok {
						person.RankText = text(rec, "RecommendText")
					}
					if subs, ok := entry["sub_title_list"].([]any); ok {
						var labels []string
						for _, v := range subs {
							if sub, ok := v.(map[string]any); ok {
								labels = append(labels, text(sub, "content"))
							}
						}
						person.SubInfo = strings.Join(labels, " · ")
					}
					out.Items = append(out.Items, model.FeedItem{SeriesID: id, Title: person.Title, Cover: person.Cover, Series: person})
				}
			}
			if books, ok := x["book_data"].([]any); ok && len(books) > 0 {
				hasBooks = true
			}
			if truth(x, "has_more") {
				out.HasMore = true
			}
			if s := text(x, "next_offset", "cursor"); s != "" {
				out.Cursor = s
			}
			if list, ok := x["video_data"].([]any); ok {
				for _, vd := range list {
					walk(map[string]any{"video_data": vd})
				}
				return
			}
			if vd, ok := x["video_data"].(map[string]any); ok {
				details := vd
				if d, ok := vd["video_detail"].(map[string]any); ok {
					details = mergeMaps(vd, d)
				}
				id := text(details, "series_id_str", "series_id", "book_id")
				if id == "" {
					return
				}
				series := parseSeries(details, id)
				vid := text(vd, "vid", "video_id")
				episode := number(vd, "vid_index", "video_index")
				if vid == "" && len(series.Episodes) > 0 {
					vid = series.Episodes[0].VideoID
					episode = series.Episodes[0].Index
				}
				key := id + ":" + vid
				if seen[key] {
					return
				}
				seen[key] = true
				contentType := number(vd, "content_type")
				if contentType == 0 {
					contentType = series.ContentType
				}
				series.ContentType = contentType
				if contentType == 1 {
					series.Kind = "real"
				} else if contentType == 1004 {
					series.Kind = "anime"
					if text(vd, "video_category_type") == "ai_video" {
						series.Kind = "ai"
					}
				}
				series.StartVideoID = vid
				series.StartEpisode = episode
				if vid != "" && contentType > 0 {
					c.contentTypes.Store(vid, contentType)
				}
				for _, ep := range series.Episodes {
					if contentType > 0 {
						c.contentTypes.Store(ep.VideoID, contentType)
					}
				}
				out.Items = append(out.Items, model.FeedItem{SeriesID: id, Title: series.Title, Cover: series.Cover, Subtitle: series.Description, Category: series.Category, VideoID: vid, Episode: episode, Series: series})
				return
			}
			keys := make([]string, 0, len(x))
			for key := range x {
				if key != "book_data" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(x[key])
			}
		}
	}
	walk(data)
	out.TotalCount = len(out.Items)
	if len(out.Items) == 0 && hasBooks {
		return nil, fmt.Errorf("上游未返回短剧内容，设备注册/签名尚未完成")
	}
	return out, nil
}

// Category retains the source {o,s} continuation. Larger batches amortize
// the observed ~700ms request latency; the server may return fewer than asked.
func (c *Client) Category(ctx context.Context, filters map[string]any, cursor string) (*model.FeedPage, error) {
	var state struct {
		Offset  int    `json:"o"`
		Session string `json:"s"`
	}
	if !strings.HasPrefix(cursor, "{") || json.Unmarshal([]byte(cursor), &state) != nil {
		state.Offset, _ = strconv.Atoi(cursor)
	}
	if state.Offset < 0 {
		state.Offset = 0
	}
	scene := "default"
	genre := filters["genre"]
	if list, ok := genre.([]string); ok && len(list) == 1 && (list[0] == "comic_series" || list[0] == "ai_series") {
		scene = list[0]
	}
	if list, ok := genre.([]any); ok && len(list) == 1 && (list[0] == "comic_series" || list[0] == "ai_series") {
		scene, _ = list[0].(string)
	}
	params := map[string]any{"select_items": filters, "offset": state.Offset, "limit": 54, "need_selector_panel": false, "req_scene": scene, "req_type": "only_content", "client_req_type": 3, "filter_ids": "", "session_id": state.Session}
	res, err := c.sess.CallJSON(ctx, "category_landpage", params)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = res.Decode(&raw); err != nil {
		return nil, err
	}
	page, err := c.parseVideoFeed(raw)
	if err != nil {
		return nil, err
	}
	var meta struct {
		NextOffset int               `json:"next_offset"`
		Session    string            `json:"session_id"`
		VideoData  []json.RawMessage `json:"video_data"`
	}
	if err = json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	if meta.NextOffset <= state.Offset {
		meta.NextOffset = state.Offset + len(meta.VideoData)
	}
	if meta.NextOffset <= state.Offset {
		page.HasMore = false
	}
	state.Offset = meta.NextOffset
	if meta.Session != "" {
		state.Session = meta.Session
	}
	b, _ := json.Marshal(state)
	page.Cursor = string(b)
	return page, nil
}

// SelectorPanel and RankPanel return actual panels, never invented options.
func (c *Client) SelectorPanel(ctx context.Context, genre string) (any, error) {
	res, err := c.sess.CallJSON(ctx, "category_landpage", map[string]any{"req_scene": first(genre, "default"), "need_selector_panel": false, "req_type": "only_panel"})
	if err != nil {
		return nil, err
	}
	var data struct {
		Rows []struct {
			Type  string `json:"type"`
			Name  string `json:"row_name"`
			Items []struct {
				ID   string `json:"selector_item_id"`
				Name string `json:"show_name"`
			} `json:"items"`
		} `json:"selector_rows"`
	}
	if err = res.Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Rows) == 0 {
		return nil, fmt.Errorf("上游未返回筛选面板")
	}
	groups := make([]map[string]any, 0, len(data.Rows))
	for _, row := range data.Rows {
		options := make([]map[string]string, 0, len(row.Items))
		for _, item := range row.Items {
			options = append(options, map[string]string{"id": item.ID, "name": item.Name})
		}
		groups = append(groups, map[string]any{"key": row.Type, "name": row.Name, "options": options})
	}
	return groups, nil
}
func (c *Client) RankPanel(ctx context.Context) (any, error) {
	return c.rankPanel(ctx)
}
func (c *Client) fetchPanel(ctx context.Context, action string, params map[string]any, keys ...string) (any, error) {
	res, err := c.sess.CallJSON(ctx, action, params)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err = res.Decode(&raw); err != nil {
		return nil, err
	}
	data, err := decodeAnyPreservingIDs(raw)
	if err != nil {
		return nil, err
	}
	var find func(any) any
	find = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range keys {
				if panel, ok := x[k]; ok && panel != nil {
					return panel
				}
			}
			for _, k := range []string{"data", "cell_view", "tab_item", "cell_data"} {
				if child, ok := x[k]; ok {
					if out := find(child); out != nil {
						return out
					}
				}
			}
		case []any:
			for _, child := range x {
				if out := find(child); out != nil {
					return out
				}
			}
		}
		return nil
	}
	panel := find(data)
	if panel == nil {
		return nil, fmt.Errorf("上游未返回筛选面板")
	}
	return panel, nil
}
