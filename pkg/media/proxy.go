package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hongguo-desktop/pkg/media/cenc"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 路由前缀。
const (
	// RouteImage 是图片代理前缀。
	RouteImage = "/dlimg/"
	// RouteMedia 是视频代理前缀。
	RouteMedia = "/media/"
	// RouteAudio 是本地音频元数据路径。
	RouteAudio = "/audio"
)

// 磁盘缓存策略。
const (
	// cacheTTL 是图片缓存有效期，7 天。
	cacheTTL = 7 * 24 * time.Hour
	// cacheMaxBytes 是单张图片的缓存上限，超过则不落盘。
	cacheMaxBytes = 32 << 20
	// imageFetchTimeout 是单张图片的拉取超时。
	imageFetchTimeout = 30 * time.Second
)

const (
	// HeaderAllowOrigin 是允许来源。
	HeaderAllowOrigin = "Access-Control-Allow-Origin"
	// HeaderAllowHeaders 是允许的请求头。
	HeaderAllowHeaders = "Access-Control-Allow-Headers"
	// HeaderExposeHeaders 是暴露给脚本的响应头。
	HeaderExposeHeaders = "Access-Control-Expose-Headers"
	// ExposeHeadersValue 让前端能读到分片信息。
	ExposeHeadersValue = "Content-Range, Content-Length, Accept-Ranges"
)

// 错误。
var (
	// ErrNoResolver 表示未注入 Resolver。
	ErrNoResolver = errors.New("media: 未配置 Resolver")
	// ErrBadKey 表示图片 key 为空或非法。
	ErrBadKey = errors.New("media: 图片 key 非法")
	// ErrNotServing 表示代理尚未启动。
	ErrNotServing = errors.New("media: 代理未启动")
)

// Resolver 把逻辑标识换成可访问的远程地址。
//
// 由上层（service 层）实现：它持有登录态与设备指纹，能算出带签名的
// CDN 地址，并给出该地址要求的 Referer。
type Resolver interface {
	// ResolveImage 返回图片直链。key 是业务侧的不透明标识
	// （通常是远程 URL 本身或 web_uri）。
	ResolveImage(ctx context.Context, key string) (string, error)
	// ResolveVideo 返回视频直链与访问它所需的 Referer。
	// referer 为空表示不需要。
	ResolveVideo(ctx context.Context, videoID, quality string) (url string, referer string, err error)
}

// ProtectedResolver supplies the decoded content key to the local proxy only.
type ProtectedResolver interface {
	ResolveProtectedVideo(context.Context, string, string) (string, string, []byte, error)
}

// Proxy 是本地反向代理，实现 http.Handler。
//
// 零值不可用，必须经 NewProxy 构造。可被多 goroutine 并发使用。
type Proxy struct {
	cacheDir string
	resolver Resolver
	client   *http.Client

	mu       sync.RWMutex
	audio    *AudioBook
	server   *http.Server
	listener net.Listener
	addr     string

	// now 便于测试替换时间源。
	now        func() time.Time
	layoutMu   sync.Mutex
	layouts    map[string]*cenc.MP4Layout
	routeMu    sync.Mutex
	failedURLs map[string]time.Time
}

// AudioBook 是 /audio 返回的章节元数据结构。
//
// 字段与前端播放器读取的形状一致；未知字段原样透传，
// 因此用 json.RawMessage 承接 chapters 内的条目。
type AudioBook struct {
	// ID 是音频书标识。
	ID string `json:"id,omitempty"`
	// Title 是书名。
	Title string `json:"title,omitempty"`
	// Chapters 是章节列表，元素形状由前端决定。
	Chapters []json.RawMessage `json:"chapters"`
}

// NewProxy 构造代理。cacheDir 为空时禁用磁盘缓存。
func NewProxy(cacheDir string, upstream Resolver) *Proxy {
	p := &Proxy{
		cacheDir: cacheDir,
		resolver: upstream,
		client: &http.Client{
			Timeout: 0, // 视频流不设总超时，靠 ctx 与连接空闲超时控制
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 8 * time.Second,
				MaxIdleConns:          64,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       90 * time.Second,
				ForceAttemptHTTP2:     true,
				// 让 Range 请求原样发出，不自动解压（否则 Content-Length 对不上）。
				DisableCompression: true,
			},
		},
		now: time.Now,
	}
	return p
}

// SetResolver 替换 Resolver，便于启动后再注入依赖。
func (p *Proxy) SetResolver(r Resolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolver = r
}

// SetAudioBook 设置 /audio 返回的内容。
func (p *Proxy) SetAudioBook(b *AudioBook) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.audio = b
}

// LoadAudioBook 从 JSON 文件读取章节元数据。
func (p *Proxy) LoadAudioBook(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("media: 读取 audio.json 失败: %w", err)
	}
	var b AudioBook
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("media: 解析 audio.json 失败: %w", err)
	}
	p.SetAudioBook(&b)
	return nil
}

// Start 在 addr 上启动服务；":0" 表示由系统分配端口。
//
// 启动后 Addr 返回实际监听地址。Start 自身不阻塞。
func (p *Proxy) Start(addr string) error {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("media: 监听 %s 失败: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 15 * time.Second,
	}

	p.mu.Lock()
	p.server = srv
	p.listener = ln
	p.addr = ln.Addr().String()
	p.mu.Unlock()

	go func() {
		// Serve 在 Close 之后返回 ErrServerClosed，无需上报。
		_ = srv.Serve(ln)
	}()
	return nil
}

// Addr 返回实际监听地址；未启动时返回空串。
func (p *Proxy) Addr() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.addr
}

// Close 停止服务并清理监听。
func (p *Proxy) Close() error {
	p.mu.Lock()
	srv, ln := p.server, p.listener
	p.server, p.listener, p.addr = nil, nil, ""
	p.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
	if ln != nil {
		return ln.Close()
	}
	return nil
}

// base 返回 http://127.0.0.1:<port> 形式的基址。
func (p *Proxy) base() string {
	addr := p.Addr()
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// URL 生成视频的本地代理地址。
func (p *Proxy) URL(videoID, quality string) string {
	b := p.base()
	if b == "" {
		return ""
	}
	return b + RouteMedia + url.PathEscape(videoID) + "/" + url.PathEscape(quality)
}

// ImageURL 生成图片的本地代理地址。
//
// key 会做 URL 编码，保证可安全放进路径段。
func (p *Proxy) ImageURL(key string) string {
	b := p.base()
	if b == "" {
		return ""
	}
	return b + RouteImage + url.PathEscape(key)
}

// LocalImageURL 是 ImageURL 的别名，语义上强调「走本地代理」。
func (p *Proxy) LocalImageURL(key string) string { return p.ImageURL(key) }

// AudioURL 生成 /audio 的本地地址。
func (p *Proxy) AudioURL() string {
	b := p.base()
	if b == "" {
		return ""
	}
	return b + RouteAudio
}

// ServeHTTP 实现 http.Handler。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch {
	case strings.HasPrefix(r.URL.Path, RouteImage):
		p.serveImage(w, r, strings.TrimPrefix(r.URL.EscapedPath(), RouteImage))
	case strings.HasPrefix(r.URL.Path, RouteMedia):
		p.serveMedia(w, r, strings.TrimPrefix(r.URL.Path, RouteMedia))
	case r.URL.Path == RouteAudio:
		p.serveAudio(w, r)
	default:
		http.NotFound(w, r)
	}
}

// setCORS 写入三条 CORS 头。
func (p *Proxy) setCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set(HeaderAllowOrigin, "*")
	h.Set(HeaderAllowHeaders, "*")
	h.Set(HeaderExposeHeaders, ExposeHeadersValue)
}

// ---------------------------------------------------------------------------
// /dlimg/
// ---------------------------------------------------------------------------

// serveImage 代理远程图片，优先命中磁盘缓存。
func (p *Proxy) serveImage(w http.ResponseWriter, r *http.Request, encoded string) {
	key, err := url.PathUnescape(encoded)
	if err != nil || strings.TrimSpace(key) == "" {
		http.Error(w, "bad image key", http.StatusBadRequest)
		return
	}

	// 先看缓存。
	if path, ok := p.cachePath(key); ok {
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			if st, err := f.Stat(); err == nil {
				w.Header().Set("Content-Type", sniffImageType(path))
				w.Header().Set("Cache-Control", "public, max-age=604800")
				w.Header().Set("X-Cache", "HIT")
				w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
				if r.Method == http.MethodHead {
					return
				}
				_, _ = io.Copy(w, f)
				return
			}
		}
	}

	p.mu.RLock()
	res := p.resolver
	p.mu.RUnlock()
	if res == nil {
		http.Error(w, ErrNoResolver.Error(), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), imageFetchTimeout)
	defer cancel()
	target, err := res.ResolveImage(ctx, key)
	if err != nil {
		http.Error(w, "resolve image: "+err.Error(), http.StatusBadGateway)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, "build request: "+err.Error(), http.StatusBadGateway)
		return
	}
	applyImageHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "fetch image: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "upstream status "+strconv.Itoa(resp.StatusCode), http.StatusBadGateway)
		return
	}

	// 最多读 cacheMaxBytes，防上游返回超大文件撑爆内存。
	body, err := io.ReadAll(io.LimitReader(resp.Body, cacheMaxBytes+1))
	if err != nil {
		http.Error(w, "read image: "+err.Error(), http.StatusBadGateway)
		return
	}
	if len(body) > cacheMaxBytes {
		http.Error(w, "图片文件过大", http.StatusBadGateway)
		return
	}
	body, contentType, err := browserImage(ctx, body, resp.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, "转换图片失败: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 落盘（失败不影响本次响应）。
	p.writeCache(key, body, contentType)

	w.Header().Set("Content-Type", firstNonEmpty(contentType, imageContentType(body)))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// applyImageHeaders 给上游图片请求补齐伪装头。
//
// 远程图床普遍校验 UA 与 Referer，缺任一项都可能 403。
func applyImageHeaders(req *http.Request) {
	req.Header.Set("User-Agent", imageUserAgent)
	req.Header.Set("Referer", imageReferer)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
}

// 图片代理伪装用的固定值。
const (
	imageUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	imageReferer = "https://hongguoduanju.com/"
)

// cachePath 返回 key 对应的缓存文件路径，并报告是否可用。
//
// 文件名取 key 的 sha256 前 32 个 hex 字符，分两级目录避免单目录文件过多。
func (p *Proxy) cachePath(key string) (string, bool) {
	p.mu.RLock()
	dir := p.cacheDir
	p.mu.RUnlock()
	if dir == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte("browser-image-v2:" + imageCacheKey(key)))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(dir, h[:2], h[2:]), true
}

// writeCache 把图片写入磁盘，带过期判定。
func (p *Proxy) writeCache(key string, body []byte, contentType string) {
	path, ok := p.cachePath(key)
	if !ok || len(body) == 0 {
		return
	}
	// 只有图片才缓存，避免把 HTML 错误页也存下来。
	if contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// 先写临时文件再改名，避免半截文件被读到。
	f, err := os.CreateTemp(filepath.Dir(path), ".image-*")
	if err != nil {
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(body); err != nil {
		f.Close()
		return
	}
	if err = f.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// CleanExpired 删除缓存目录中超过 TTL 的文件，返回删除数量。
func (p *Proxy) CleanExpired() (int, error) {
	p.mu.RLock()
	dir := p.cacheDir
	nowFn := p.now
	p.mu.RUnlock()
	if dir == "" {
		return 0, nil
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	deadline := nowFn().Add(-cacheTTL)

	removed := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(deadline) {
			if os.Remove(path) == nil {
				removed++
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return removed, fmt.Errorf("media: 清理缓存失败: %w", err)
	}
	return removed, nil
}

// sniffImageType 按扩展名猜类型；缓存文件名无扩展名，交由 http.DetectContentType。
func sniffImageType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	var header [512]byte
	n, _ := f.Read(header[:])
	return imageContentType(header[:n])
}

// ---------------------------------------------------------------------------
// /media/
// ---------------------------------------------------------------------------

// serveMedia 代理视频流，原样透传 Range。
func (p *Proxy) serveMedia(w http.ResponseWriter, r *http.Request, rest string) {
	videoID, quality, ok := splitMediaPath(rest)
	if !ok {
		http.Error(w, "bad media path", http.StatusBadRequest)
		return
	}

	p.mu.RLock()
	res := p.resolver
	p.mu.RUnlock()
	if res == nil {
		http.Error(w, ErrNoResolver.Error(), http.StatusInternalServerError)
		return
	}

	opened, err := p.openMedia(r, res, videoID, quality)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	target, key, layout, resp := opened.target, opened.key, opened.layout, opened.response
	defer resp.Body.Close()
	start := int64(0)
	if layout != nil && resp.StatusCode == http.StatusPartialContent {
		var end, total int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start < 0 || end < start || total != layout.Size {
			http.Error(w, "invalid protected media range", http.StatusBadGateway)
			return
		}
	}

	// 透传与分片相关的响应头。
	copyHeader(w.Header(), resp.Header, "Content-Type")
	copyHeader(w.Header(), resp.Header, "Content-Length")
	copyHeader(w.Header(), resp.Header, "Content-Range")
	copyHeader(w.Header(), resp.Header, "Accept-Ranges")
	copyHeader(w.Header(), resp.Header, "Last-Modified")
	copyHeader(w.Header(), resp.Header, "ETag")
	if len(key) > 0 {
		sum := sha256.Sum256(append([]byte(target), key...))
		w.Header().Set("ETag", fmt.Sprintf(`W/"clear-%x"`, sum[:12]))
	}
	// 上游若没给 Accept-Ranges，主动补上，前端据此决定是否可拖动。
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.Header().Set("Cache-Control", "no-store")

	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if layout != nil && (resp.StatusCode == 200 || resp.StatusCode == 206) {
		_ = copyProtected(w, resp.Body, layout, start)
	} else {
		_, _ = io.Copy(w, resp.Body)
	}
}

// applyMediaHeaders 给上游视频请求补齐头。
func applyMediaHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", mediaUserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Referer", imageReferer)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "identity") // 分片必须明文，避免 gzip 改变长度
}

// mediaUserAgent 是视频 CDN 常用的浏览器 UA。
const mediaUserAgent = imageUserAgent

// splitMediaPath 把 "<videoID>/<quality>" 拆开。
//
// quality 允许为空（调用方可能只给 videoID），此时返回空串。
func splitMediaPath(rest string) (videoID, quality string, ok bool) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "" {
		return "", "", false
	}
	if len(parts) == 2 {
		q, err := url.PathUnescape(parts[1])
		if err != nil {
			return "", "", false
		}
		quality = q
	}
	return id, quality, true
}

// copyHeader 在目标头不存在时从源复制。
func copyHeader(dst, src http.Header, key string) {
	if v := src.Get(key); v != "" {
		dst.Set(key, v)
	}
}

// ---------------------------------------------------------------------------
// /audio
// ---------------------------------------------------------------------------

// serveAudio 返回本地章节元数据。
func (p *Proxy) serveAudio(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	b := p.audio
	p.mu.RUnlock()
	if b == nil {
		http.Error(w, "audio.json 未加载", http.StatusNotFound)
		return
	}
	raw, err := json.Marshal(b)
	if err != nil {
		http.Error(w, "序列化 audio.json 失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(raw)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
