package nativedec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
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
type decoderProcess struct {
	cmd           *exec.Cmd
	reader        *bufio.Reader
	width, height int
	fps           float64
	started       time.Time
	cancel        context.CancelFunc
	parentCancel  context.CancelFunc
	guard         func()
	once          sync.Once
}

func startDecoder(ctx context.Context, path, url string, at float64, hardware bool) (*decoderProcess, error) {
	lifetime, cancel := context.WithCancel(ctx)
	p := &decoderProcess{started: time.Now(), cancel: cancel}
	cmd := exec.CommandContext(lifetime, path, ffmpegArgs(url, at, hardware)...)
	p.cmd = cmd
	hideProcess(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("无法启动原生解码器: %w", err)
	}
	p.guard = guardProcess(cmd)
	p.reader = bufio.NewReaderSize(out, 128<<10)
	line, err := p.reader.ReadSlice('\n')
	if err == nil {
		p.width, p.height, p.fps, err = parseY4MHeader(string(line))
	}
	if err != nil {
		p.close()
		return nil, errors.New("原生解码器未能读取视频")
	}
	return p, nil
}
func (p *decoderProcess) close() {
	p.once.Do(func() {
		p.cancel()
		if p.parentCancel != nil {
			p.parentCancel()
		}
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		if p.guard != nil {
			p.guard()
		}
	})
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
		process := task.process
		m.warmMu.Unlock()
		cancel()
		if process != nil {
			process.close()
		}
	})
	m.warmMu.Unlock()
	disposePrepared(old)
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
		task.timer.Stop()
		m.warmMu.Unlock()
		cancel()
		if process != nil {
			process.close()
		}
	}()
}
func disposePrepared(task *preparedDecode) {
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
	disposePrepared(task)
	return nil
}
func (m *Manager) clearPreload() {
	m.warmMu.Lock()
	task := m.warm
	m.warm = nil
	m.warmMu.Unlock()
	disposePrepared(task)
}
