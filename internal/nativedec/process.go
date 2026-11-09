package nativedec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type decoderProcess struct {
	cmd           *exec.Cmd
	reader        *bufio.Reader
	width, height int
	fps           float64
	cancel        context.CancelFunc
	parentCancel  context.CancelFunc
	guard         func()
	once          sync.Once
}

func startDecoder(ctx context.Context, path, url string, at float64, hardware bool) (*decoderProcess, error) {
	lifetime, cancel := context.WithCancel(ctx)
	p := &decoderProcess{cancel: cancel}
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
