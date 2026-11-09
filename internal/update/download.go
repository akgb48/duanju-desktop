package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func (m *Manager) StartDownload() error {
	select {
	case m.gate <- struct{}{}:
	default:
		return errors.New("更新任务正在执行")
	}
	m.mu.Lock()
	a, checksum := m.asset, m.checksum
	available := m.state.Available != nil
	m.mu.Unlock()
	if !available || a.URL == "" || !validHash(checksum) {
		<-m.gate
		return errors.New("请先检查并选择可用的新版本")
	}
	m.set(func(s *State) {
		s.Phase = "downloading"
		s.Progress = 0
		s.Error = ""
		s.CanApply = false
		s.LastResult = ""
	})
	go func() {
		defer func() { <-m.gate }()
		if err := m.download(a, checksum); err != nil {
			m.fail(err)
		}
	}()
	return nil
}

// The reference UI awaits this RPC, then invokes ApplyUpdate. Progress events
// remain independent of the pending download call.
func (m *Manager) Download(ctx context.Context) error {
	if m.Snapshot().Phase == "ready" {
		return nil
	}
	if err := m.StartDownload(); err != nil {
		return err
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-timer.C:
		}
		s := m.Snapshot()
		if s.Phase == "ready" {
			return nil
		}
		if s.Phase != "downloading" {
			return errors.New(s.Error)
		}
	}
}

func (m *Manager) download(a asset, checksum string) error {
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Minute)
	defer cancel()
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".download-*.part")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "DuanjuDesktop")
	// Large packages have a task deadline, rather than a short total client timeout.
	client := *m.client
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载更新失败: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("下载更新返回 HTTP %d", res.StatusCode)
	}
	if res.ContentLength > 0 && res.ContentLength != a.Size {
		return errors.New("更新包大小与发布信息不符")
	}
	h := sha256.New()
	writer := io.MultiWriter(f, h)
	var done int64
	buf := make([]byte, 128<<10)
	started, last := time.Now(), time.Now()
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > a.Size {
				return errors.New("更新包超过声明大小")
			}
			if _, err = writer.Write(buf[:n]); err != nil {
				return err
			}
			if time.Since(last) > 150*time.Millisecond {
				m.set(func(s *State) {
					s.Bytes = done
					s.Progress = float64(done) / float64(a.Size)
					s.Speed = int64(float64(done) / time.Since(started).Seconds())
				})
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if done != a.Size || hex.EncodeToString(h.Sum(nil)) != checksum {
		return errors.New("更新包 SHA256 或长度校验失败，已放弃更新")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	target := strings.TrimSuffix(temp, ".part") + ".exe"
	if err = os.Rename(temp, target); err != nil {
		return err
	}
	m.mu.Lock()
	m.readyPath = target
	m.mu.Unlock()
	m.set(func(s *State) {
		s.Phase = "ready"
		s.Progress = 1
		s.Bytes = done
		s.CanApply = true
		s.LastResult = "更新已下载，可以重启安装"
	})
	return nil
}
