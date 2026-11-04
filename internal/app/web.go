package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// WebHandler serves the embedded desktop UI with a loopback-only RPC transport.
// It exposes only the declared frontend contract, never lifecycle methods.
func WebHandler(a *App, assets fs.FS) http.Handler {
	a.mu.Lock()
	a.webMode = true
	a.mu.Unlock()
	a.OnStartup(context.Background())
	allowed := make(map[string]bool, len(bindingNames))
	for _, name := range bindingNames {
		allowed[name] = true
	}
	files := http.FileServer(http.FS(assets))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": a.version, "bindings": len(allowed)})
	})
	mux.HandleFunc("/api/invoke/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fail := func(status int, err error) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		}
		if r.Method != http.MethodPost {
			fail(405, fmt.Errorf("仅支持 POST"))
			return
		}
		if r.Header.Get("X-HG-Local") != "1" {
			fail(403, fmt.Errorf("缺少本地请求标记"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				fail(403, fmt.Errorf("不允许跨站调用本地接口"))
				return
			}
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/invoke/")
		if name == "RestartApp" {
			fail(422, fmt.Errorf("浏览器模式请刷新页面；桌面模式支持重启"))
			return
		}
		if !allowed[name] {
			fail(404, fmt.Errorf("未知方法 %s", name))
			return
		}
		defer func() {
			if v := recover(); v != nil {
				fail(500, fmt.Errorf("%s 调用失败: %v", name, v))
			}
		}()
		method := reflect.ValueOf(a).MethodByName(name)
		var args []json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&args); err != nil {
			fail(400, fmt.Errorf("参数格式错误: %w", err))
			return
		}
		if len(args) != method.Type().NumIn() {
			fail(400, fmt.Errorf("%s 需要 %d 个参数，收到 %d 个", name, method.Type().NumIn(), len(args)))
			return
		}
		values := make([]reflect.Value, len(args))
		for i, b := range args {
			p := reflect.New(method.Type().In(i))
			if err := json.Unmarshal(b, p.Interface()); err != nil {
				fail(400, fmt.Errorf("%s 参数 %d: %w", name, i+1, err))
				return
			}
			values[i] = p.Elem()
		}
		out := method.Call(values)
		if len(out) > 0 && out[len(out)-1].Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			last := out[len(out)-1]
			out = out[:len(out)-1]
			if !last.IsNil() {
				fail(422, last.Interface().(error))
				return
			}
		}
		var data any
		if len(out) > 0 {
			data = out[0].Interface()
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if _, err := fs.Stat(assets, strings.TrimPrefix(r.URL.Path, "/")); err != nil && !strings.Contains(r.URL.Path, ".") {
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" {
			http.Error(w, "仅允许本机访问", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
