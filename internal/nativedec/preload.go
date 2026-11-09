package nativedec

import (
	"context"
	"time"
)

// One paused next-video process complements the active decoder. Its bounded
// stdout pipe applies backpressure, so warming never decodes an entire episode.
type preparedDecode struct {
	url      string
	hardware bool
	cancel   context.CancelFunc
	process  *decoderProcess
	timer    *time.Timer
}

func (m *Manager) Preload(url string) {
	m.mu.Lock()
	path := m.path
	m.mu.Unlock()
	if path == "" || m.ctx.Err() != nil {
		return
	}
	hardware := !m.cpu.Load() && !m.gpuFailed.Load()
	m.warmMu.Lock()
	if m.warm != nil && m.warm.url == url && m.warm.hardware == hardware {
		m.warmMu.Unlock()
		return
	}
	old := m.warm
	ctx, cancel := context.WithCancel(m.ctx)
	task := &preparedDecode{url: url, hardware: hardware, cancel: cancel}
	m.warm = task
	task.timer = time.AfterFunc(90*time.Second, func() {
		m.warmMu.Lock()
		if m.warm != task {
			m.warmMu.Unlock()
			return
		}
		m.warm = nil
		m.warmMu.Unlock()
		task.close()
	})
	m.warmMu.Unlock()
	old.close()
	go func() {
		process, err := startDecoder(ctx, path, url, 0, hardware)
		if process != nil {
			process.parentCancel = cancel
		}
		m.warmMu.Lock()
		current := m.warm == task && ctx.Err() == nil
		if current && err == nil {
			task.process = process
			m.warmMu.Unlock()
			return
		}
		if m.warm == task {
			m.warm = nil
		}
		m.warmMu.Unlock()
		task.close()
		if process != nil {
			process.close()
		}
	}()
}
func (task *preparedDecode) close() {
	if task == nil {
		return
	}
	task.timer.Stop()
	task.cancel()
	if task.process != nil {
		task.process.close()
	}
}
func (m *Manager) takePreloaded(url string, at float64, hardware bool) *decoderProcess {
	m.warmMu.Lock()
	task := m.warm
	m.warm = nil
	if task == nil {
		m.warmMu.Unlock()
		return nil
	}
	task.timer.Stop()
	if task.url == url && at == 0 && task.hardware == hardware && task.process != nil {
		process := task.process
		m.warmMu.Unlock()
		return process
	}
	m.warmMu.Unlock()
	task.close()
	return nil
}
func (m *Manager) clearPreload() {
	m.warmMu.Lock()
	task := m.warm
	m.warm = nil
	m.warmMu.Unlock()
	task.close()
}
