package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // 注册纯 Go 的 sqlite3 驱动
)

// driverName 是 modernc 驱动注册名。
const driverName = "sqlite"

// 通用错误，调用方可直接比对。
var (
	// ErrNotFound 表示查询目标不存在。
	ErrNotFound = errors.New("store: 记录不存在")
	// ErrClosed 表示 Store 已关闭。
	ErrClosed = errors.New("store: 数据库已关闭")
)

// Store 是数据库句柄。
//
// 零值不可用，必须经 Open 构造；可被多 goroutine 并发使用。
type Store struct {
	db   *sql.DB
	path string
}

// Account 是登录账号快照。
//
// 与 account 表一一对应；字段全为字符串是因为上游返回的
// user_id 可能超出 int64，且 token/cookie 都是不透明串。
type Account struct {
	UserID    string `json:"userId"`
	Token     string `json:"token"`
	Cookie    string `json:"cookie"`
	DeviceID  string `json:"deviceId"`
	InstallID string `json:"installId"`
	CDID      string `json:"cdid"`
	OpenUDID  string `json:"openudid"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Progress 是单集播放进度。
type Progress struct {
	SeriesID   string `json:"seriesId"`
	Episode    int    `json:"episode"`
	PositionMS int64  `json:"positionMs"`
	DurationMS int64  `json:"durationMs"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// Favorite 是本地收藏项。
type Favorite struct {
	SeriesID  string `json:"seriesId"`
	Title     string `json:"title"`
	Cover     string `json:"cover"`
	CreatedAt int64  `json:"createdAt"`
}

// Like 是本地点赞项。
type Like struct {
	TargetID   string `json:"targetId"`
	TargetType string `json:"targetType"`
	CreatedAt  int64  `json:"createdAt"`
}

// DownloadTask 是一条下载任务。
//
// 字段与前端 TypeScript 类型逐一对齐，json tag 不带 omitempty：
// 前端按固定形状读取，缺字段会导致进度条归零。
type DownloadTask struct {
	ID           string `json:"id"`
	SeriesID     string `json:"seriesId"`
	SeriesTitle  string `json:"seriesTitle"`
	EpisodeIndex int    `json:"episodeIndex"`
	VideoID      string `json:"videoId"`
	Quality      string `json:"quality"`
	State        string `json:"state"`
	Bytes        int64  `json:"bytes"`
	Total        int64  `json:"total"`
	Error        string `json:"error"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Duration     int    `json:"duration"`
	CreatedAt    int64  `json:"createdAt"`
}

// 下载任务状态常量。
const (
	DownloadPending = "pending"
	DownloadRunning = "running"
	DownloadDone    = "done"
	DownloadFailed  = "failed"
	DownloadPaused  = "paused"
)

// dsn 组装带 WAL 与忙等超时的 DSN。
//
// modernc 驱动的 DSN 形如 file:path?_pragma=name(value)&_pragma=...
// 多个 _pragma 用 & 连接，重复键按出现顺序全部执行。
func dsn(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
}

// Open 打开（必要时创建）数据库文件并完成迁移。
//
// path 为 ":memory:" 时使用内存库，仅用于测试。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: 数据库路径为空")
	}
	if path != ":memory:" && path != "file::memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: 创建数据目录失败: %w", err)
			}
		}
	}
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: 打开数据库失败: %w", err)
	}
	// 连接池固定为 1：WAL 下写锁只有一个持有者，多连接只会互相等待。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	if err != nil {
		return fmt.Errorf("store: 关闭数据库失败: %w", err)
	}
	return nil
}

// Path 返回数据库文件路径。
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// DB 返回底层句柄，供同包测试与迁移脚本使用。
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// Migrate 建表建索引，可重复调用。
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	for _, stmt := range schemaStatements() {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: 执行迁移失败 (%s): %w", firstLine(stmt), err)
		}
	}
	return nil
}

// check 报告句柄是否可用。
func (s *Store) check() error {
	if s == nil || s.db == nil {
		return ErrClosed
	}
	return nil
}

// now 返回当前 Unix 秒。集中在此便于测试替换。
var now = func() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------------------
// kv
// ---------------------------------------------------------------------------

// GetKV 读取一个键；不存在时返回 ErrNotFound。
func (s *Store) GetKV(ctx context.Context, key string) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: 读取 kv %q 失败: %w", key, err)
	}
	return v, nil
}

// SetKV 写入或覆盖一个键。
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	if err := s.check(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: 写入 kv %q 失败: %w", key, err)
	}
	return nil
}

// DeleteKV 删除一个键；键不存在不报错。
func (s *Store) DeleteKV(ctx context.Context, key string) error {
	if err := s.check(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: 删除 kv %q 失败: %w", key, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// account
// ---------------------------------------------------------------------------

// SaveAccount 写入登录态；表中最多一行，旧行被覆盖。
func (s *Store) SaveAccount(ctx context.Context, a *Account) error {
	if err := s.check(); err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("store: SaveAccount 收到 nil")
	}
	ts := a.UpdatedAt
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO account
		   (id, user_id, token, cookie, device_id, install_id, cdid, openudid, nickname, avatar, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   user_id    = excluded.user_id,
		   token      = excluded.token,
		   cookie     = excluded.cookie,
		   device_id  = excluded.device_id,
		   install_id = excluded.install_id,
		   cdid       = excluded.cdid,
		   openudid   = excluded.openudid,
		   nickname   = excluded.nickname,
		   avatar     = excluded.avatar,
		   updated_at = excluded.updated_at`,
		a.UserID, a.Token, a.Cookie, a.DeviceID, a.InstallID,
		a.CDID, a.OpenUDID, a.Nickname, a.Avatar, ts)
	if err != nil {
		return fmt.Errorf("store: 保存账号失败: %w", err)
	}
	a.UpdatedAt = ts
	return nil
}

// LoadAccount 读取登录态；未登录时返回 ErrNotFound。
func (s *Store) LoadAccount(ctx context.Context) (*Account, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT user_id, token, cookie, device_id, install_id, cdid, openudid, nickname, avatar, updated_at
		   FROM account WHERE id = 1`)
	var a Account
	err := row.Scan(&a.UserID, &a.Token, &a.Cookie, &a.DeviceID, &a.InstallID,
		&a.CDID, &a.OpenUDID, &a.Nickname, &a.Avatar, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: 读取账号失败: %w", err)
	}
	return &a, nil
}

// ClearAccount 删除登录态。
func (s *Store) ClearAccount(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM account WHERE id = 1`); err != nil {
		return fmt.Errorf("store: 清理账号失败: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// progress
// ---------------------------------------------------------------------------

// UpsertProgress 写入或覆盖一集进度。
//
// PositionMS 为 0 时不覆盖已有值，避免「刚打开就被清零」。
func (s *Store) UpsertProgress(ctx context.Context, p *Progress) error {
	if err := s.check(); err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("store: UpsertProgress 收到 nil")
	}
	ts := p.UpdatedAt
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO progress (series_id, episode, position_ms, duration_ms, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(series_id, episode) DO UPDATE SET
		   position_ms = CASE WHEN excluded.position_ms > 0
		                      THEN excluded.position_ms ELSE progress.position_ms END,
		   duration_ms = CASE WHEN excluded.duration_ms > 0
		                      THEN excluded.duration_ms ELSE progress.duration_ms END,
		   updated_at  = excluded.updated_at`,
		p.SeriesID, p.Episode, p.PositionMS, p.DurationMS, ts)
	if err != nil {
		return fmt.Errorf("store: 写入进度失败: %w", err)
	}
	p.UpdatedAt = ts
	return nil
}

// ListProgress 按最近观看时间倒序返回最多 limit 条进度。
//
// limit <= 0 时返回全部。
func (s *Store) ListProgress(ctx context.Context, limit int) ([]Progress, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	q := `SELECT series_id, episode, position_ms, duration_ms, updated_at
	        FROM progress ORDER BY updated_at DESC, series_id ASC, episode ASC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: 列出进度失败: %w", err)
	}
	defer rows.Close()
	out := make([]Progress, 0, 16)
	for rows.Next() {
		var p Progress
		if err := rows.Scan(&p.SeriesID, &p.Episode, &p.PositionMS, &p.DurationMS, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: 扫描进度失败: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历进度失败: %w", err)
	}
	return out, nil
}

// GetProgress 读取指定剧集的一集进度；不存在时返回 ErrNotFound。
func (s *Store) GetProgress(ctx context.Context, seriesID string, episode int) (*Progress, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	var p Progress
	err := s.db.QueryRowContext(ctx,
		`SELECT series_id, episode, position_ms, duration_ms, updated_at
		   FROM progress WHERE series_id = ? AND episode = ?`, seriesID, episode).
		Scan(&p.SeriesID, &p.Episode, &p.PositionMS, &p.DurationMS, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: 读取进度失败: %w", err)
	}
	return &p, nil
}

// ---------------------------------------------------------------------------
// download
// ---------------------------------------------------------------------------

// UpsertDownload 写入或覆盖一条下载任务。
//
// 覆盖时保留原 CreatedAt，除非调用方显式给了非零新值。
func (s *Store) UpsertDownload(ctx context.Context, t *DownloadTask) error {
	if err := s.check(); err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("store: UpsertDownload 收到 nil")
	}
	if t.ID == "" {
		return fmt.Errorf("store: 下载任务缺少 id")
	}
	ts := t.CreatedAt
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO download
		   (id, series_id, series_title, episode_index, video_id, quality, state,
		    bytes, total, error, width, height, duration, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   series_id     = excluded.series_id,
		   series_title  = excluded.series_title,
		   episode_index = excluded.episode_index,
		   video_id      = excluded.video_id,
		   quality       = excluded.quality,
		   state         = excluded.state,
		   bytes         = excluded.bytes,
		   total         = excluded.total,
		   error         = excluded.error,
		   width         = excluded.width,
		   height        = excluded.height,
		   duration      = excluded.duration,
		   created_at    = download.created_at`,
		t.ID, t.SeriesID, t.SeriesTitle, t.EpisodeIndex, t.VideoID, t.Quality, t.State,
		t.Bytes, t.Total, t.Error, t.Width, t.Height, t.Duration, ts)
	if err != nil {
		return fmt.Errorf("store: 写入下载任务 %q 失败: %w", t.ID, err)
	}
	// 冲突时 created_at 保留旧值，回读一次以保证调用方看到库内真值。
	if err := s.db.QueryRowContext(ctx,
		`SELECT created_at FROM download WHERE id = ?`, t.ID).Scan(&t.CreatedAt); err != nil {
		return fmt.Errorf("store: 回读下载任务 %q 失败: %w", t.ID, err)
	}
	return nil
}

// ListDownloads 按创建时间倒序返回全部下载任务。
func (s *Store) ListDownloads(ctx context.Context) ([]DownloadTask, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, series_id, series_title, episode_index, video_id, quality, state,
		        bytes, total, error, width, height, duration, created_at
		   FROM download ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: 列出下载任务失败: %w", err)
	}
	defer rows.Close()
	out := make([]DownloadTask, 0, 16)
	for rows.Next() {
		var t DownloadTask
		if err := rows.Scan(&t.ID, &t.SeriesID, &t.SeriesTitle, &t.EpisodeIndex, &t.VideoID,
			&t.Quality, &t.State, &t.Bytes, &t.Total, &t.Error,
			&t.Width, &t.Height, &t.Duration, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 扫描下载任务失败: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历下载任务失败: %w", err)
	}
	return out, nil
}

// GetDownload 读取单条下载任务；不存在时返回 ErrNotFound。
func (s *Store) GetDownload(ctx context.Context, id string) (*DownloadTask, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	var t DownloadTask
	err := s.db.QueryRowContext(ctx,
		`SELECT id, series_id, series_title, episode_index, video_id, quality, state,
		        bytes, total, error, width, height, duration, created_at
		   FROM download WHERE id = ?`, id).
		Scan(&t.ID, &t.SeriesID, &t.SeriesTitle, &t.EpisodeIndex, &t.VideoID,
			&t.Quality, &t.State, &t.Bytes, &t.Total, &t.Error,
			&t.Width, &t.Height, &t.Duration, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: 读取下载任务 %q 失败: %w", id, err)
	}
	return &t, nil
}

// DeleteDownload 删除一条下载任务。
func (s *Store) DeleteDownload(ctx context.Context, id string) error {
	if err := s.check(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM download WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: 删除下载任务 %q 失败: %w", id, err)
	}
	return nil
}

// UpdateDownloadState 只改状态与错误信息，不动进度字段。
func (s *Store) UpdateDownloadState(ctx context.Context, id, state, errMsg string) error {
	if err := s.check(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE download SET state = ?, error = ? WHERE id = ?`, state, errMsg, id)
	if err != nil {
		return fmt.Errorf("store: 更新下载状态 %q 失败: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: 更新下载状态 %q 失败: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// local_favorite / local_like
// ---------------------------------------------------------------------------

// AddFavorite 添加收藏；重复添加只刷新时间。
func (s *Store) AddFavorite(ctx context.Context, f *Favorite) error {
	if err := s.check(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("store: AddFavorite 收到 nil")
	}
	if f.SeriesID == "" {
		return fmt.Errorf("store: 收藏项缺少 seriesId")
	}
	ts := f.CreatedAt
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO local_favorite (series_id, title, cover, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(series_id) DO UPDATE SET
		   title      = excluded.title,
		   cover      = excluded.cover,
		   created_at = CASE WHEN local_favorite.created_at > 0
		                     THEN local_favorite.created_at ELSE excluded.created_at END`,
		f.SeriesID, f.Title, f.Cover, ts)
	if err != nil {
		return fmt.Errorf("store: 添加收藏 %q 失败: %w", f.SeriesID, err)
	}
	f.CreatedAt = ts
	return nil
}

// RemoveFavorite 取消收藏；不存在不报错。
func (s *Store) RemoveFavorite(ctx context.Context, seriesID string) error {
	if err := s.check(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM local_favorite WHERE series_id = ?`, seriesID); err != nil {
		return fmt.Errorf("store: 取消收藏 %q 失败: %w", seriesID, err)
	}
	return nil
}

// ListFavorites 按收藏时间倒序返回全部收藏。
func (s *Store) ListFavorites(ctx context.Context) ([]Favorite, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT series_id, title, cover, created_at
		   FROM local_favorite ORDER BY created_at DESC, series_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: 列出收藏失败: %w", err)
	}
	defer rows.Close()
	out := make([]Favorite, 0, 16)
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.SeriesID, &f.Title, &f.Cover, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 扫描收藏失败: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历收藏失败: %w", err)
	}
	return out, nil
}

// AddLike 添加点赞；重复添加只刷新时间。
func (s *Store) AddLike(ctx context.Context, l *Like) error {
	if err := s.check(); err != nil {
		return err
	}
	if l == nil {
		return fmt.Errorf("store: AddLike 收到 nil")
	}
	if l.TargetID == "" {
		return fmt.Errorf("store: 点赞项缺少 targetId")
	}
	ts := l.CreatedAt
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO local_like (target_id, target_type, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(target_id, target_type) DO UPDATE SET
		   created_at = CASE WHEN local_like.created_at > 0
		                     THEN local_like.created_at ELSE excluded.created_at END`,
		l.TargetID, l.TargetType, ts)
	if err != nil {
		return fmt.Errorf("store: 添加点赞 %q 失败: %w", l.TargetID, err)
	}
	l.CreatedAt = ts
	return nil
}

// RemoveLike 取消点赞；不存在不报错。
func (s *Store) RemoveLike(ctx context.Context, targetID, targetType string) error {
	if err := s.check(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM local_like WHERE target_id = ? AND target_type = ?`,
		targetID, targetType); err != nil {
		return fmt.Errorf("store: 取消点赞 %q 失败: %w", targetID, err)
	}
	return nil
}

// ListLikes 按点赞时间倒序返回全部点赞。
func (s *Store) ListLikes(ctx context.Context) ([]Like, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_id, target_type, created_at
		   FROM local_like ORDER BY created_at DESC, target_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: 列出点赞失败: %w", err)
	}
	defer rows.Close()
	out := make([]Like, 0, 16)
	for rows.Next() {
		var l Like
		if err := rows.Scan(&l.TargetID, &l.TargetType, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 扫描点赞失败: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历点赞失败: %w", err)
	}
	return out, nil
}

// firstLine 取字符串第一行，用于压缩报错信息。
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
