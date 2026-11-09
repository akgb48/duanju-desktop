package nativedec

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestY4MHeaderValidation(t *testing.T) {
	w, h, f, err := parseY4MHeader("YUV4MPEG2 W1920 H1080 F30000:1001 Ip C420jpeg\n")
	if err != nil || w != 1920 || h != 1080 || f < 29.9 || f > 30 {
		t.Fatalf("%d %d %f %v", w, h, f, err)
	}
	for _, line := range []string{"garbage", "YUV4MPEG2 W1919 H1080 C420", "YUV4MPEG2 W8192 H8192 C420", "YUV4MPEG2 W320 H240 C444", "YUV4MPEG2 W320 H240 F999:1 C420"} {
		if _, _, _, err := parseY4MHeader(line); err == nil {
			t.Fatalf("invalid header accepted: %s", line)
		}
	}
}

type testRing struct{ frames [Slots][]byte }

func newTestRing() *testRing {
	r := &testRing{}
	for i := range r.frames {
		r.frames[i] = make([]byte, HeaderBytes+MaxFrameBytes)
	}
	return r
}
func (r *testRing) Memory(slot int) []byte { return r.frames[slot] }
func (*testRing) Close()                   {}

// Opt-in real-process test also exercises the exact pinned archive's extraction.
// Normal CI stays independent of an external binary download.
func TestFFmpegRuntimeDecodeSeekAndCancellation(t *testing.T) {
	archive := os.Getenv("HG_NATIVE_TEST_ARCHIVE")
	if archive == "" {
		t.Skip("set HG_NATIVE_TEST_ARCHIVE for the pinned FFmpeg integration test")
	}
	if hash, err := fileHash(archive); err != nil || hash != RuntimeSHA256 {
		t.Fatalf("archive: %s %v", hash, err)
	}
	dir := filepath.Join(t.TempDir(), "runtime")
	executable, err := extractRuntime(archive, dir)
	if err != nil {
		t.Fatal(err)
	}
	if installedRuntime(dir) != executable {
		t.Fatal("installed runtime failed verification")
	}
	video := filepath.Join(t.TempDir(), "fixture.mp4")
	cmd := exec.Command(executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25", "-t", "4", "-c:v", "mpeg4", "-movflags", "+faststart", video)
	hideProcess(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", b, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, video) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 10)
	m := New(nil, dir, func(e Event) { events <- e })
	defer m.Shutdown()
	m.path = executable
	m.runtimeChecked = true
	m.SetCPUOnly(true)
	m.Preload(server.URL)
	deadlineWarm := time.Now().Add(5 * time.Second)
	var warmedPID int
	for time.Now().Before(deadlineWarm) {
		m.warmMu.Lock()
		if m.warm != nil && m.warm.process != nil {
			warmedPID = m.warm.process.cmd.Process.Pid
		}
		m.warmMu.Unlock()
		if warmedPID != 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if warmedPID == 0 {
		t.Fatal("next-video process was not prepared")
	}
	prepared := m.takePreloaded(server.URL, 0, false)
	if prepared == nil || prepared.cmd.Process.Pid != warmedPID {
		t.Fatal("next video did not reuse the prepared process")
	}
	prepared.close()
	ring := newTestRing()
	s := &session{id: 1, url: server.URL, ring: ring, gen: 1, manager: m}
	done := make(chan error, 1)
	go func() { done <- s.decode(ctx, 2, 1.2, false) }()
	select {
	case e := <-events:
		if e.Type != "meta" || e.Width != 320 || e.Height != 240 || e.Generation != 2 {
			t.Fatalf("meta: %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no metadata")
	}
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadUint32((*uint32)(unsafe.Pointer(&ring.frames[0][0]))) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if binary.LittleEndian.Uint32(ring.frames[0][16:]) != 2 {
		t.Fatal("no decoded frame for new generation")
	}
	// With no credits returned, decoding blocks after six frames; cancellation
	// must kill the process and release the producer without a renderer response.
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("decoder leaked on cancellation")
	}
	if install := os.Getenv("HG_NATIVE_TEST_INSTALL"); install != "" {
		if err := os.MkdirAll(filepath.Dir(install), 0700); err != nil {
			t.Fatal(err)
		}
		if installedRuntime(install) == "" {
			if _, err := extractRuntime(archive, install); err != nil {
				t.Fatal(err)
			}
		}
	}
}
