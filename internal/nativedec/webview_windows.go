//go:build windows

package nativedec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

type comObject struct{ table *[160]uintptr }

func comCall(object *comObject, index int, args ...uintptr) error {
	if object == nil {
		return errors.New("WebView2 接口不可用")
	}
	parameters := append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)
	hr, _, _ := syscall.SyscallN(object.table[index], parameters...)
	if int32(hr) < 0 {
		return fmt.Errorf("WebView2 HRESULT 0x%x", hr)
	}
	return nil
}
func query(object *comObject, id string) (*comObject, error) {
	guid, err := windows.GUIDFromString(id)
	if err != nil {
		return nil, err
	}
	var out *comObject
	err = comCall(object, 0, uintptr(unsafe.Pointer(&guid)), uintptr(unsafe.Pointer(&out)))
	return out, err
}

type webview struct {
	invoke            func(func())
	environment, view *comObject
}
type ring struct {
	owner   *webview
	objects []*comObject
	memory  [][]byte
}

// Wails 2.11 does not expose its WebView2 pointer publicly. The reference uses
// this same narrow adapter. Guard field names/types against the pinned Wails
// and go-webview2 versions; unsupported layouts fall back to WASM safely.
func attachWebview(ctx context.Context) (sharedView, error) {
	if ctx == nil || ctx.Value("frontend") == nil {
		return nil, errors.New("当前环境没有桌面 WebView2")
	}
	frontend := reflect.ValueOf(ctx.Value("frontend"))
	if frontend.Kind() != reflect.Ptr || frontend.IsNil() {
		return nil, errors.New("桌面窗口不可用")
	}
	frontend = frontend.Elem()
	browserField := frontend.FieldByName("chromium")
	windowField := frontend.FieldByName("mainWindow")
	if !browserField.IsValid() || !windowField.IsValid() || !browserField.CanAddr() || !windowField.CanAddr() || browserField.Type() != reflect.TypeOf((*edge.Chromium)(nil)) {
		return nil, errors.New("桌面运行时不支持共享视频缓冲区")
	}
	browser := reflect.NewAt(browserField.Type(), unsafe.Pointer(browserField.UnsafeAddr())).Elem().Interface().(*edge.Chromium)
	window := reflect.NewAt(windowField.Type(), unsafe.Pointer(windowField.UnsafeAddr())).Elem().Interface()
	invoker, ok := window.(interface{ Invoke(func()) })
	if !ok || browser == nil || (windowField.Kind() == reflect.Ptr && windowField.IsNil()) {
		return nil, errors.New("桌面窗口尚未就绪")
	}
	b := &webview{invoke: invoker.Invoke}
	err := b.sync(func() error {
		env := browser.Environment()
		controller := browser.GetController()
		if env == nil || controller == nil {
			return errors.New("WebView2 尚未就绪")
		}
		view, err := controller.GetCoreWebView2()
		if err != nil {
			return err
		}
		defer view.Release()
		b.environment, err = query((*comObject)(unsafe.Pointer(env)), "{f503db9b-739f-48dd-b151-fdfcf253f54e}")
		if err != nil {
			return err
		}
		b.view, err = query((*comObject)(unsafe.Pointer(view)), "{702e75d4-fd44-434d-9d70-1a68a6b1192a}")
		return err
	})
	if err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *webview) sync(fn func() error) error {
	done := make(chan error, 1)
	var guard sync.Mutex
	abandoned := false
	b.invoke(func() {
		guard.Lock()
		defer guard.Unlock()
		if abandoned {
			return
		}
		done <- fn()
	})
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		guard.Lock()
		abandoned = true
		guard.Unlock()
		select {
		case err := <-done:
			return err
		default:
		}
		return errors.New("桌面共享缓冲区响应超时")
	}
}

func (b *webview) NewRing(id, size int) (frameRing, error) {
	r := &ring{owner: b}
	err := b.sync(func() error {
		for slot := 0; slot < Slots; slot++ {
			var object *comObject
			// ICoreWebView2Environment12::CreateSharedBuffer, vtable slot 24.
			if err := comCall(b.environment, 24, uintptr(size), uintptr(unsafe.Pointer(&object))); err != nil {
				return err
			}
			r.objects = append(r.objects, object)
			var memory *byte
			if err := comCall(object, 4, uintptr(unsafe.Pointer(&memory))); err != nil {
				return err
			}
			r.memory = append(r.memory, unsafe.Slice(memory, size))
			metadata, _ := json.Marshal(map[string]int{"nd": id, "slot": slot})
			text, _ := windows.UTF16PtrFromString(string(metadata))
			// ICoreWebView2_17::PostSharedBufferToScript, slot 116, read/write access.
			if err := comCall(b.view, 116, uintptr(unsafe.Pointer(object)), 1, uintptr(unsafe.Pointer(text))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}
func (r *ring) Memory(slot int) []byte { return r.memory[slot] }
func (r *ring) Close() {
	_ = r.owner.sync(func() error {
		for _, o := range r.objects {
			_ = comCall(o, 7)
			_ = comCall(o, 2)
		}
		r.objects = nil
		r.memory = nil
		return nil
	})
}
func (b *webview) Close() {
	if b.invoke == nil {
		return
	}
	_ = b.sync(func() error {
		if b.view != nil {
			_ = comCall(b.view, 2)
			b.view = nil
		}
		if b.environment != nil {
			_ = comCall(b.environment, 2)
			b.environment = nil
		}
		return nil
	})
}

func hideProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
func guardProcess(cmd *exec.Cmd) func() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return func() {}
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		windows.CloseHandle(job)
		return func() {}
	}
	return func() { windows.CloseHandle(job) }
}
