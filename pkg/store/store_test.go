package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// newTestStore 在临时目录建库，测试结束自动关闭。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ctx 返回带超时的上下文。
func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestOpenMigrateIdempotent(t *testing.T) {
	s := newTestStore(t)
	// 二次迁移不应报错，也不应丢数据。
	if err := s.Migrate(ctx(t)); err != nil {
		t.Fatalf("Migrate 第二次: %v", err)
	}
	if err := s.SetKV(ctx(t), "k", "v"); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if err := s.Migrate(ctx(t)); err != nil {
		t.Fatalf("Migrate 第三次: %v", err)
	}
	got, err := s.GetKV(ctx(t), "k")
	if err != nil || got != "v" {
		t.Fatalf("迁移后 kv 丢失: got=%q err=%v", got, err)
	}
}

func TestSchemaTablesExist(t *testing.T) {
	s := newTestStore(t)
	want := []string{"kv", "account", "progress", "download", "local_favorite", "local_like"}
	rows, err := s.DB().QueryContext(ctx(t),
		`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("查询表: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("扫描表名: %v", err)
		}
		got[n] = true
	}
	for _, n := range want {
		if !got[n] {
			t.Errorf("缺少表 %q，实际有 %v", n, got)
		}
	}
	// 索引也要存在。
	var idx string
	err = s.DB().QueryRowContext(ctx(t),
		`SELECT name FROM sqlite_master WHERE type='index' AND name='progress_updated'`).Scan(&idx)
	if err != nil {
		t.Fatalf("缺少索引 progress_updated: %v", err)
	}
}

func TestPragmasApplied(t *testing.T) {
	s := newTestStore(t)
	var mode string
	if err := s.DB().QueryRowContext(ctx(t), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("读 journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q，期望 wal", mode)
	}
	var timeout int
	if err := s.DB().QueryRowContext(ctx(t), `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("读 busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d，期望 5000", timeout)
	}
}

func TestKV(t *testing.T) {
	s := newTestStore(t)
	c := ctx(t)

	if _, err := s.GetKV(c, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空表 GetKV 应返回 ErrNotFound，实际 %v", err)
	}
	if err := s.SetKV(c, "theme", "dark"); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if got, err := s.GetKV(c, "theme"); err != nil || got != "dark" {
		t.Fatalf("GetKV = %q, %v", got, err)
	}
	// 覆盖写
	if err := s.SetKV(c, "theme", "light"); err != nil {
		t.Fatalf("SetKV 覆盖: %v", err)
	}
	if got, _ := s.GetKV(c, "theme"); got != "light" {
		t.Fatalf("覆盖后 = %q，期望 light", got)
	}
	if err := s.DeleteKV(c, "theme"); err != nil {
		t.Fatalf("DeleteKV: %v", err)
	}
	if _, err := s.GetKV(c, "theme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound，实际 %v", err)
	}
	// 删不存在的键不报错
	if err := s.DeleteKV(c, "theme"); err != nil {
		t.Fatalf("重复删除应无错，实际 %v", err)
	}
}

func TestAccountSingleRow(t *testing.T) {
	s := newTestStore(t)
	c := ctx(t)

	if _, err := s.LoadAccount(c); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未登录应 ErrNotFound，实际 %v", err)
	}
	a := &Account{UserID: "1001", Token: "tk1", Nickname: "阿果"}
	if err := s.SaveAccount(c, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if a.UpdatedAt == 0 {
		t.Error("SaveAccount 应回填 UpdatedAt")
	}
	got, err := s.LoadAccount(c)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}
	if got.UserID != "1001" || got.Token != "tk1" || got.Nickname != "阿果" {
		t.Fatalf("读回不一致: %+v", got)
	}
	// 再存一次必须覆盖而不是插入第二行
	if err := s.SaveAccount(c, &Account{UserID: "2002", Token: "tk2"}); err != nil {
		t.Fatalf("SaveAccount 第二次: %v", err)
	}
	var n int
	if err := s.DB().QueryRowContext(c, `SELECT COUNT(*) FROM account`).Scan(&n); err != nil {
		t.Fatalf("计数: %v", err)
	}
	if n != 1 {
		t.Fatalf("account 行数 = %d，期望 1", n)
	}
	got, _ = s.LoadAccount(c)
	if got.UserID != "2002" {
		t.Fatalf("覆盖失败，user_id = %q", got.UserID)
	}
	// CHECK(id=1) 必须挡掉非法 id
	if _, err := s.DB().ExecContext(c,
		`INSERT INTO account (id, user_id, updated_at) VALUES (2, 'x', 0)`); err == nil {
		t.Fatal("CHECK(id=1) 未生效，插入 id=2 竟然成功")
	}
	if err := s.ClearAccount(c); err != nil {
		t.Fatalf("ClearAccount: %v", err)
	}
	if _, err := s.LoadAccount(c); !errors.Is(err, ErrNotFound) {
		t.Fatalf("登出后应 ErrNotFound，实际 %v", err)
	}
}

func TestProgress(t *testing.T) {
	s := newTestStore(t)
	c := ctx(t)

	if _, err := s.GetProgress(c, "S1", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空表应 ErrNotFound，实际 %v", err)
	}
	if err := s.UpsertProgress(c, &Progress{SeriesID: "S1", Episode: 1, PositionMS: 1000, DurationMS: 60000, UpdatedAt: 100}); err != nil {
		t.Fatalf("UpsertProgress: %v", err)
	}
	if err := s.UpsertProgress(c, &Progress{SeriesID: "S1", Episode: 2, PositionMS: 2000, DurationMS: 90000, UpdatedAt: 300}); err != nil {
		t.Fatalf("UpsertProgress 2: %v", err)
	}
	if err := s.UpsertProgress(c, &Progress{SeriesID: "S2", Episode: 1, PositionMS: 500, DurationMS: 30000, UpdatedAt: 200}); err != nil {
		t.Fatalf("UpsertProgress 3: %v", err)
	}
	list, err := s.ListProgress(c, 0)
	if err != nil {
		t.Fatalf("ListProgress: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("进度条数 = %d，期望 3", len(list))
	}
	// 按 updated_at 倒序
	if list[0].UpdatedAt != 300 || list[2].UpdatedAt != 100 {
		t.Fatalf("排序错误: %+v", list)
	}
	// limit 生效
	lim, err := s.ListProgress(c, 2)
	if err != nil || len(lim) != 2 {
		t.Fatalf("ListProgress(2) = %d 条, %v", len(lim), err)
	}
	// position_ms=0 不应把已有进度清零
	if err := s.UpsertProgress(c, &Progress{SeriesID: "S1", Episode: 1, PositionMS: 0, UpdatedAt: 400}); err != nil {
		t.Fatalf("UpsertProgress 清零: %v", err)
	}
	p, err := s.GetProgress(c, "S1", 1)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if p.PositionMS != 1000 {
		t.Fatalf("position_ms 被清零: %+v", p)
	}
	if p.UpdatedAt != 400 {
		t.Fatalf("updated_at 未更新: %d", p.UpdatedAt)
	}
}

func TestDownload(t *testing.T) {
	s := newTestStore(t)
	c := ctx(t)

	task := &DownloadTask{
		ID: "d1", SeriesID: "S1", SeriesTitle: "测试剧", EpisodeIndex: 3,
		VideoID: "v1", Quality: "1080p", State: DownloadRunning,
		Bytes: 1024, Total: 4096, Width: 1920, Height: 1080, Duration: 120000,
	}
	if err := s.UpsertDownload(c, task); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	if task.CreatedAt == 0 {
		t.Error("CreatedAt 应被回填")
	}
	created := task.CreatedAt

	// 覆盖时 CreatedAt 不变
	if err := s.UpsertDownload(c, &DownloadTask{ID: "d1", State: DownloadDone, Bytes: 4096}); err != nil {
		t.Fatalf("UpsertDownload 覆盖: %v", err)
	}
	got, err := s.GetDownload(c, "d1")
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.State != DownloadDone || got.Bytes != 4096 {
		t.Fatalf("覆盖失败: %+v", got)
	}
	if got.CreatedAt != created {
		t.Fatalf("CreatedAt 被改动: %d -> %d", created, got.CreatedAt)
	}

	// 状态更新
	if err := s.UpdateDownloadState(c, "d1", DownloadFailed, "网络中断"); err != nil {
		t.Fatalf("UpdateDownloadState: %v", err)
	}
	got, _ = s.GetDownload(c, "d1")
	if got.State != DownloadFailed || got.Error != "网络中断" {
		t.Fatalf("状态更新失败: %+v", got)
	}
	if err := s.UpdateDownloadState(c, "missing", DownloadFailed, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在任务应 ErrNotFound，实际 %v", err)
	}

	// 列表
	if err := s.UpsertDownload(c, &DownloadTask{ID: "d2", State: DownloadPending, CreatedAt: created + 10}); err != nil {
		t.Fatalf("UpsertDownload d2: %v", err)
	}
	list, err := s.ListDownloads(c)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListDownloads = %d 条, %v", len(list), err)
	}
	if list[0].ID != "d2" {
		t.Fatalf("应按 created_at 倒序，实际首位 %q", list[0].ID)
	}

	// 删除
	if err := s.DeleteDownload(c, "d1"); err != nil {
		t.Fatalf("DeleteDownload: %v", err)
	}
	if _, err := s.GetDownload(c, "d1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound，实际 %v", err)
	}

	// 缺 id 要报错
	if err := s.UpsertDownload(c, &DownloadTask{}); err == nil {
		t.Fatal("缺 id 应报错")
	}
}

func TestDownloadJSONShape(t *testing.T) {
	// 前端按固定形状读取，json tag 不得带 omitempty。
	b, err := json.Marshal(DownloadTask{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{
		"id", "seriesId", "seriesTitle", "episodeIndex", "videoId", "quality",
		"state", "bytes", "total", "error", "width", "height", "duration", "createdAt",
	}
	if len(m) != len(want) {
		t.Fatalf("字段数 = %d，期望 %d: %s", len(m), len(want), b)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少字段 %q，实际 %s", k, b)
		}
	}
}

func TestFavoriteAndLike(t *testing.T) {
	s := newTestStore(t)
	c := ctx(t)

	if err := s.AddFavorite(c, &Favorite{SeriesID: "S1", Title: "剧一", Cover: "c1", CreatedAt: 100}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := s.AddFavorite(c, &Favorite{SeriesID: "S2", Title: "剧二", CreatedAt: 200}); err != nil {
		t.Fatalf("AddFavorite 2: %v", err)
	}
	// 重复添加只刷新字段，不新增行
	if err := s.AddFavorite(c, &Favorite{SeriesID: "S1", Title: "剧一改", CreatedAt: 999}); err != nil {
		t.Fatalf("AddFavorite 重复: %v", err)
	}
	favs, err := s.ListFavorites(c)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if len(favs) != 2 {
		t.Fatalf("收藏条数 = %d，期望 2", len(favs))
	}
	if favs[0].SeriesID != "S2" {
		t.Fatalf("应按时间倒序，首位 %q", favs[0].SeriesID)
	}
	for _, f := range favs {
		if f.SeriesID == "S1" {
			if f.Title != "剧一改" {
				t.Errorf("标题未刷新: %q", f.Title)
			}
			if f.CreatedAt != 100 {
				t.Errorf("created_at 应保留原值 100，实际 %d", f.CreatedAt)
			}
		}
	}
	if err := s.RemoveFavorite(c, "S1"); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	if err := s.RemoveFavorite(c, "S1"); err != nil {
		t.Fatalf("重复删除应无错: %v", err)
	}
	if favs, _ := s.ListFavorites(c); len(favs) != 1 {
		t.Fatalf("删除后条数 = %d", len(favs))
	}

	// 点赞：复合主键
	if err := s.AddLike(c, &Like{TargetID: "T1", TargetType: "series", CreatedAt: 10}); err != nil {
		t.Fatalf("AddLike: %v", err)
	}
	if err := s.AddLike(c, &Like{TargetID: "T1", TargetType: "episode", CreatedAt: 20}); err != nil {
		t.Fatalf("AddLike 2: %v", err)
	}
	likes, err := s.ListLikes(c)
	if err != nil {
		t.Fatalf("ListLikes: %v", err)
	}
	if len(likes) != 2 {
		t.Fatalf("点赞条数 = %d，期望 2（同 id 不同类型应共存）", len(likes))
	}
	if err := s.RemoveLike(c, "T1", "series"); err != nil {
		t.Fatalf("RemoveLike: %v", err)
	}
	if likes, _ := s.ListLikes(c); len(likes) != 1 || likes[0].TargetType != "episode" {
		t.Fatalf("删除后剩余错误: %+v", likes)
	}
	if err := s.AddLike(c, &Like{TargetType: "x"}); err == nil {
		t.Fatal("缺 targetId 应报错")
	}
}

func TestClosedStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.GetKV(ctx(t), "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后应 ErrClosed，实际 %v", err)
	}
	if err := s.SetKV(ctx(t), "k", "v"); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后 SetKV 应 ErrClosed，实际 %v", err)
	}
	// 重复 Close 不报错
	if err := s.Close(); err != nil {
		t.Fatalf("重复 Close: %v", err)
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c.db")
	s, err := Open(nested)
	if err != nil {
		t.Fatalf("Open 嵌套路径: %v", err)
	}
	defer s.Close()
	if s.Path() != nested {
		t.Errorf("Path() = %q，期望 %q", s.Path(), nested)
	}
	if _, err := Open(""); err == nil {
		t.Fatal("空路径应报错")
	}
}

func TestConcurrentAccess(t *testing.T) {
	// 连接池限制为 1，并发写不应死锁。
	s := newTestStore(t)
	c := ctx(t)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			done <- s.SetKV(c, "k", "v")
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("并发写: %v", err)
		}
	}
}
