package nativedec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const Slots = 6
const HeaderBytes = 64
const MaxFrameBytes = 1920 * 1088 * 3 / 2

type frameRing interface {
	Memory(int) []byte
	Close()
}
type sharedView interface {
	NewRing(int, int) (frameRing, error)
	Close()
}
type Status struct {
	Available bool   `json:"available"`
	FFmpeg    string `json:"ffmpeg"`
	GPU       string `json:"gpu"`
	Reason    string `json:"reason"`
}
type Event struct {
	ID         int     `json:"id"`
	Type       string  `json:"t"`
	Width      int     `json:"w,omitempty"`
	Height     int     `json:"h,omitempty"`
	FPS        float64 `json:"fps,omitempty"`
	Hardware   string  `json:"hw,omitempty"`
	Generation int     `json:"gen,omitempty"`
	Message    string  `json:"msg,omitempty"`
	SpawnMS    int64   `json:"spawnMs,omitempty"`
	ReadyMS    int64   `json:"readyMs,omitempty"`
}

type Manager struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	uiContext      context.Context
	dir, path      string
	preparing      bool
	runtimeChecked bool
	err            string
	view           sharedView
	next           int
	sessions       map[int]*session
	emit           func(Event)
	cpu            atomic.Bool
	gpuFailed      atomic.Bool
	warmMu         sync.Mutex
	warm           *preparedDecode
}

type session struct {
	mu       sync.Mutex
	id       int
	url      string
	ring     frameRing
	gen      int
	sequence uint32
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
	manager  *Manager
}

func New(ctx context.Context, dir string, emit func(Event)) *Manager {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Manager{ctx: lifetime, cancel: cancel, uiContext: ctx, dir: dir, sessions: map[int]*session{}, emit: emit}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.runtimeChecked {
		m.path = installedRuntime(m.dir)
		m.runtimeChecked = true
	}
	if m.view == nil {
		view, err := attachWebview(m.uiContext)
		if err == nil {
			m.view = view
		} else {
			m.err = err.Error()
		}
	}
	state := "ready"
	if m.path == "" {
		state = "missing"
	}
	if m.preparing {
		state = "downloading"
	}
	return Status{Available: m.path != "" && m.view != nil, FFmpeg: state, GPU: "D3D11VA", Reason: m.err}
}

func (m *Manager) Prepare() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path != "" || m.preparing {
		return nil
	}
	m.preparing = true
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
		defer cancel()
		path, err := installRuntime(ctx, m.dir)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.preparing = false
		if err != nil {
			m.err = err.Error()
		} else {
			m.path = path
			m.err = ""
		}
	}()
	return nil
}

func (m *Manager) Open(url string, at float64) (int, error) {
	if err := m.ctx.Err(); err != nil {
		return 0, err
	}
	status := m.Status()
	if !status.Available {
		return 0, fmt.Errorf("原生解码尚未就绪: %s", status.Reason)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= 2 {
		return 0, errors.New("原生解码会话过多")
	}
	m.next++
	id := m.next
	ring, err := m.view.NewRing(id, HeaderBytes+MaxFrameBytes)
	if err != nil {
		return 0, err
	}
	m.sessions[id] = &session{id: id, url: url, ring: ring, gen: 1, manager: m}
	// Decoding begins with the frontend's initial seek acknowledgement, after
	// it knows the ID and has installed its metadata/frame event handlers.
	return id, nil
}

func (m *Manager) Seek(id int, at float64) (int, error) {
	if math.IsNaN(at) || math.IsInf(at, 0) || at < 0 {
		return 0, errors.New("定位时间无效")
	}
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return 0, errors.New("原生解码会话已关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("原生解码会话已关闭")
	}
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	s.gen++
	gen := s.gen
	ctx, cancel := context.WithCancel(m.ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() { defer close(s.done); s.run(ctx, gen, at) }()
	return gen, nil
}

func (m *Manager) Close(id int) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	s.ring.Close()
}

func (m *Manager) Shutdown() {
	m.cancel()
	m.clearPreload()
	m.mu.Lock()
	ids := make([]int, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
	m.mu.Lock()
	if m.view != nil {
		m.view.Close()
		m.view = nil
	}
	m.mu.Unlock()
}

func (m *Manager) SetCPUOnly(enabled bool) { m.cpu.Store(enabled) }

func (s *session) run(ctx context.Context, gen int, at float64) {
	m := s.manager
	hardware := !m.cpu.Load() && !m.gpuFailed.Load()
	err := s.decode(ctx, gen, at, hardware)
	if err != nil && ctx.Err() == nil && hardware {
		m.gpuFailed.Store(true)
		err = s.decode(ctx, gen, at, false)
	}
	if err != nil && ctx.Err() == nil && m.emit != nil {
		m.emit(Event{ID: s.id, Type: "error", Generation: gen, Message: err.Error()})
	}
}

func ffmpegArgs(url string, at float64, hardware bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "0", "-rw_timeout", "10000000"}
	if hardware {
		args = append(args, "-hwaccel", "d3d11va")
	}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	return append(args, "-protocol_whitelist", "http,tcp", "-i", url, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", "scale='trunc(iw*min(1,sqrt(2073600/(iw*ih)))/2)*2':'trunc(ih*min(1,sqrt(2073600/(iw*ih)))/2)*2'", "-pix_fmt", "yuv420p", "-fps_mode", "cfr", "-f", "yuv4mpegpipe", "pipe:1")
}

func parseY4MHeader(line string) (w, h int, fps float64, err error) {
	if !strings.HasPrefix(line, "YUV4MPEG2 ") {
		return 0, 0, 0, errors.New("解码器未输出 Y4M 数据")
	}
	fps = 30
	for _, field := range strings.Fields(line) {
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'W':
			w, _ = strconv.Atoi(field[1:])
		case 'H':
			h, _ = strconv.Atoi(field[1:])
		case 'F':
			parts := strings.Split(field[1:], ":")
			if len(parts) == 2 {
				a, _ := strconv.ParseFloat(parts[0], 64)
				b, _ := strconv.ParseFloat(parts[1], 64)
				if a > 0 && b > 0 {
					fps = a / b
				}
			}
		case 'C':
			if !strings.HasPrefix(field, "C420") {
				return 0, 0, 0, errors.New("不支持的原生像素格式")
			}
		}
	}
	if w < 2 || h < 2 || w%2 != 0 || h%2 != 0 || w > 8192 || h > 8192 || w*h*3/2 > MaxFrameBytes || fps < 1 || fps > 240 {
		return 0, 0, 0, errors.New("原生视频尺寸或帧率超限")
	}
	return
}

func (s *session) decode(ctx context.Context, gen int, at float64, hardware bool) error {
	m := s.manager
	m.mu.Lock()
	path := m.path
	m.mu.Unlock()
	started := time.Now()
	process := m.takePreloaded(s.url, at, hardware)
	var err error
	if process == nil {
		process, err = startDecoder(ctx, path, s.url, at, hardware)
	}
	if err != nil {
		return err
	}
	defer process.close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			process.cancel()
		case <-done:
		}
	}()
	reader := process.reader
	w, h, fps := process.width, process.height, process.fps
	hw := ""
	if hardware {
		hw = "D3D11VA"
	}
	if m.emit != nil {
		m.emit(Event{ID: s.id, Type: "meta", Width: w, Height: h, FPS: fps, Hardware: hw, Generation: gen, SpawnMS: time.Since(started).Milliseconds(), ReadyMS: time.Since(started).Milliseconds()})
	}
	frame := make([]byte, w*h*3/2)
	for index := 0; ; index++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		lineBytes, readErr := reader.ReadSlice('\n')
		err = readErr
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(lineBytes), "FRAME") {
			return errors.New("原生视频帧格式无效")
		}
		if _, err = io.ReadFull(reader, frame); err != nil {
			return err
		}
		seq := s.sequence + 1
		slot := int((seq - 1) % Slots)
		memory := s.ring.Memory(slot)
		for atomic.LoadUint32((*uint32)(unsafe.Pointer(&memory[0]))) != atomic.LoadUint32((*uint32)(unsafe.Pointer(&memory[4]))) {
			timer := time.NewTimer(2 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		copy(memory[HeaderBytes:], frame)
		binary.LittleEndian.PutUint32(memory[8:], uint32(w))
		binary.LittleEndian.PutUint32(memory[12:], uint32(h))
		binary.LittleEndian.PutUint32(memory[16:], uint32(gen))
		binary.LittleEndian.PutUint32(memory[20:], 1)
		binary.LittleEndian.PutUint32(memory[24:], 0)
		binary.LittleEndian.PutUint64(memory[32:], math.Float64bits(at+float64(index)/fps))
		s.sequence = seq
		atomic.StoreUint32((*uint32)(unsafe.Pointer(&memory[0])), seq)
	}
}
