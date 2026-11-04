package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"duanju-desktop/pkg/store"
)

func TestMoveDataDirSwitchesLiveWritersAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "original")
	target := filepath.Join(root, "迁移 目录 #1")
	t.Setenv("HG_DATA_DIR", source)
	a := NewInitialized()
	t.Cleanup(func() {
		if a.st != nil {
			a.OnShutdown(ctx)
		}
	})
	if a.st == nil {
		t.Fatal("database failed to initialize")
	}
	if err := a.st.SetKV(ctx, "migration-test", "committed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, dataDatabase+"-wal")); err != nil {
		t.Fatalf("expected source to have a live WAL: %v", err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	q, err := a.downloadManager()
	if err != nil {
		t.Fatal(err)
	}
	download := DownloadItem{ID: "preserved", State: "done", FilePath: filepath.Join(root, "saved.mp4")}
	if _, err := q.add([]DownloadItem{download}); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveDataDir(target); err != nil {
		t.Fatal(err)
	}
	if a.dataDir != target || a.st.Path() != filepath.Join(target, dataDatabase) ||
		q.path != filepath.Join(target, "downloads.json") ||
		a.playbackPool.path != filepath.Join(target, "playback-sessions.json") {
		t.Fatal("one or more live writers still use the original directory")
	}
	if value, err := a.st.GetKV(ctx, "migration-test"); err != nil || value != "committed" {
		t.Fatalf("WAL data missing from the snapshot: %q %v", value, err)
	}
	if _, err := a.readDeviceRecord(); err != nil {
		t.Fatalf("device profile missing after migration: %v", err)
	}
	if err := a.saveProgress(PlayState{SeriesID: "after-move", Episode: 1, PositionMs: 2500}); err != nil {
		t.Fatal(err)
	}
	a.OnShutdown(ctx)
	if err := os.Rename(source, filepath.Join(root, "original-backup")); err != nil {
		t.Fatal(err)
	}

	restarted := NewInitialized()
	t.Cleanup(func() { restarted.OnShutdown(ctx) })
	if restarted.dataDir != target || restarted.st == nil {
		t.Fatal("restart did not select the migrated data directory")
	}
	if progress, err := restarted.st.GetProgress(ctx, "after-move", 1); err != nil || progress.PositionMS != 2500 {
		t.Fatalf("write after the move was not restored: %v %v", progress, err)
	}
	q, err = restarted.downloadManager()
	if err != nil {
		t.Fatal(err)
	}
	if items := q.list(); len(items) != 1 || items[0] != download {
		t.Fatalf("download queue was not restored: %v", items)
	}
	again := filepath.Join(root, "second target")
	if err := restarted.MoveDataDir(again); err != nil {
		t.Fatal(err)
	}
	if got := selectedDataDir(source, restarted.dataDirConfig); got != again {
		t.Fatalf("second migration updated the wrong configuration: %q", got)
	}
}

func TestMoveDataDirKeepsSourceWhenActivationFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "original")
	t.Setenv("HG_DATA_DIR", source)
	a := NewInitialized()
	t.Cleanup(func() { a.OnShutdown(ctx) })
	if a.st == nil {
		t.Fatal("database failed to initialize")
	}
	if err := a.MoveDataDir(filepath.Join(source, "nested", "target")); err == nil {
		t.Fatal("accepted nested target")
	}
	occupied := filepath.Join(root, "occupied")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(occupied, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveDataDir(occupied); err == nil {
		t.Fatal("accepted an occupied target")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("existing data changed: %q %v", got, err)
	}
	// A directory at the selection path prevents the final atomic write.
	if err := os.Mkdir(a.dataDirConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	old := a.st
	target := filepath.Join(root, "new")
	if err := a.MoveDataDir(target); err == nil {
		t.Fatal("migration reported success without persisting the new selection")
	}
	if a.dataDir != source || a.st != old {
		t.Fatal("failed activation replaced the live store")
	}
	if err := a.st.UpsertProgress(ctx, &store.Progress{SeriesID: "source-still-writable", Episode: 1, PositionMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a.dataDirConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveDataDir(target); err != nil {
		t.Fatalf("could not retry failed migration at the same destination: %v", err)
	}
}
