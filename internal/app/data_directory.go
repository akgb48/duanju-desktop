package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"duanju-desktop/pkg/store"
)

const dataDatabase = "hgplayer.db"

var dataMetadataFiles = [...]string{"device.json", "downloads.json", "playback-sessions.json"}

func selectedDataDir(fallback, config string) string {
	data, err := os.ReadFile(config)
	if errors.Is(err, os.ErrNotExist) {
		return fallback
	}
	var dir string
	if err == nil && json.Unmarshal(data, &dir) == nil && filepath.IsAbs(dir) {
		if info, err := os.Stat(filepath.Join(dir, dataDatabase)); err == nil && info.Mode().IsRegular() {
			return dir
		}
	}
	dbg("无法读取已迁移的数据目录，使用原目录")
	return fallback
}

// MoveDataDir snapshots durable app state, then switches the live writers and
// the startup selection. Downloaded videos keep their configured file paths.
func (a *App) MoveDataDir(path string) error {
	a.dataMoveMu.Lock()
	defer a.dataMoveMu.Unlock()
	a.mu.RLock()
	source, defaultDir, config, oldStore, proxy := a.dataDir, a.defaultDataDir, a.dataDirConfig, a.st, a.proxy
	a.mu.RUnlock()
	if oldStore == nil || config == "" {
		return errors.New("本地存储未就绪")
	}
	if path == "" {
		path = defaultDir
	}
	target, err := prepareDataTarget(source, path)
	if err != nil || target == "" {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".duanju-migration-*")
	if err != nil {
		return fmt.Errorf("创建迁移暂存目录失败: %w", err)
	}
	defer os.RemoveAll(stage)

	// Quiesce file writers while the snapshot is taken and their destinations
	// are changed. Readers can keep using the old copy during this interval.
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	a.authMu.Lock()
	defer a.authMu.Unlock()
	localSaveMu.Lock()
	defer localSaveMu.Unlock()
	downloadQueues.Lock()
	defer downloadQueues.Unlock()
	queue := downloadQueues.items[a]
	if queue != nil {
		queue.mu.Lock()
		defer queue.mu.Unlock()
		if err := queue.persistLocked(); err != nil {
			return err
		}
	}
	pool := a.playbackPool
	if pool != nil {
		pool.mu.Lock()
		defer pool.mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dbPath := filepath.Join(stage, dataDatabase)
	if err := oldStore.Snapshot(ctx, dbPath); err != nil {
		return err
	}
	if err := verifyDataSnapshot(ctx, dbPath); err != nil {
		return err
	}
	for _, name := range dataMetadataFiles {
		data, err := os.ReadFile(filepath.Join(source, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("读取迁移文件 %s 失败: %w", name, err)
		}
		if err := writeFileAtomically(filepath.Join(stage, name), data); err != nil {
			return fmt.Errorf("写入迁移文件 %s 失败: %w", name, err)
		}
	}
	if err := syscall.Rmdir(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("目标目录必须为空: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		return fmt.Errorf("提交迁移目录失败: %w", err)
	}
	activated := false
	defer func() {
		if !activated {
			for _, name := range dataMetadataFiles {
				_ = os.Remove(filepath.Join(target, name))
			}
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				_ = os.Remove(filepath.Join(target, dataDatabase+suffix))
			}
		}
	}()
	next, err := store.Open(filepath.Join(target, dataDatabase))
	if err != nil {
		return err
	}
	selection, _ := json.Marshal(target)
	err = os.MkdirAll(filepath.Dir(config), 0o700)
	if err == nil {
		err = writeFileAtomically(config, selection)
	}
	if err != nil {
		_ = next.Close()
		return fmt.Errorf("保存数据目录设置失败，仍使用原目录: %w", err)
	}
	activated = true

	if queue != nil {
		queue.path = filepath.Join(target, "downloads.json")
	}
	if pool != nil {
		pool.path = filepath.Join(target, "playback-sessions.json")
	}
	if proxy != nil {
		proxy.SetCacheDir(filepath.Join(target, "media"))
	}
	a.mu.Lock()
	a.st, a.dataDir = next, target
	a.mu.Unlock()
	if err := oldStore.Close(); err != nil {
		dbg("关闭迁移前的数据库: %v", err)
	}
	return nil
}

func prepareDataTarget(source, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("请选择目标目录")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	// Check lexical containment before creating any parent directories.
	if dataPathContains(source, target) || dataPathContains(target, source) {
		if rel, err := filepath.Rel(source, target); err == nil && rel == "." {
			return "", nil
		}
		return "", errors.New("目标目录不能是当前数据目录的父目录或子目录")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	target = filepath.Join(parent, filepath.Base(target))
	if dataPathContains(source, target) || dataPathContains(target, source) {
		return "", errors.New("目标目录与当前数据目录重叠")
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return target, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("目标必须是普通目录")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", err
	}
	if len(entries) != 0 {
		return "", errors.New("目标目录必须为空")
	}
	return target, nil
}

func dataPathContains(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func verifyDataSnapshot(ctx context.Context, path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	var result string
	err = db.DB().QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result)
	closeErr = db.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return fmt.Errorf("验证数据库快照失败: %w", err)
	}
	if result != "ok" {
		return errors.New("数据库快照未通过完整性检查")
	}
	return nil
}
