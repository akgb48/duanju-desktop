// native-check verifies the real WebView2 shared-buffer bridge with a local
// video fixture. It exits after consuming frames and never registers a device.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing/fstest"
	"time"

	"duanju-desktop/internal/nativedec"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type Checker struct {
	ctx     context.Context
	manager *nativedec.Manager
	url     string
	passed  atomic.Bool
}

func (c *Checker) Start() error {
	state := c.manager.Status()
	fmt.Printf("runtime=%s available=%t reason=%s\n", state.FFmpeg, state.Available, state.Reason)
	id, err := c.manager.Open(c.url, 0)
	if err != nil {
		return err
	}
	_, err = c.manager.Seek(id, 0)
	return err
}
func (c *Checker) Report(width, height, generation, sequence int) {
	if c.passed.Load() {
		return
	}
	c.passed.Store(width > 0 && height > 0 && generation == 2 && sequence > 0)
	fmt.Printf("shared-frame width=%d height=%d generation=%d sequence=%d passed=%t\n", width, height, generation, sequence, c.passed.Load())
	go func() { c.manager.Shutdown(); wruntime.Quit(c.ctx) }()
}
func (c *Checker) Fail(message string) { fmt.Println("FAIL:", message); wruntime.Quit(c.ctx) }
func main() {
	video := flag.String("video", "", "local MP4 fixture")
	dir := flag.String("runtime", "", "verified runtime directory")
	flag.Parse()
	if *video == "" || *dir == "" {
		log.Fatal("--video and --runtime are required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, *video) })}
	defer server.Close()
	go server.Serve(listener)
	c := &Checker{url: "http://" + listener.Addr().String() + "/fixture.mp4"}
	html := `<!doctype html><html><body>Checking native shared video buffers…</body></html>`
	var assets fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(html)}}
	err = wails.Run(&options.App{Title: "Native decoder verification", Width: 640, Height: 360,
		AssetServer: &assetserver.Options{Assets: assets}, Bind: []any{c},
		OnStartup: func(ctx context.Context) {
			c.ctx = ctx
			c.manager = nativedec.New(ctx, *dir, func(e nativedec.Event) {
				fmt.Printf("event=%s generation=%d hw=%s size=%dx%d error=%s\n", e.Type, e.Generation, e.Hardware, e.Width, e.Height, e.Message)
			})
		},
		OnDomReady: func(ctx context.Context) {
			wruntime.WindowExecJS(ctx, `(()=>{const buffers=[];let reported=false;chrome.webview.addEventListener('sharedbufferreceived',e=>buffers.push(e.getBuffer()));setInterval(()=>{for(const b of buffers){const h=new Uint32Array(b,0,8);if(h[0]&&!reported){reported=true;window.go.main.Checker.Report(h[2],h[3],h[4],h[0]);}h[1]=h[0];}},10);window.go.main.Checker.Start().catch(e=>window.go.main.Checker.Fail(String(e)));})()`)
			go func() {
				time.Sleep(15 * time.Second)
				if !c.passed.Load() {
					c.Fail("shared frame timed out")
				}
			}()
		}, OnShutdown: func(ctx context.Context) { c.manager.Shutdown() },
	})
	if err != nil {
		log.Fatal(err)
	}
	if !c.passed.Load() {
		os.Exit(1)
	}
}
