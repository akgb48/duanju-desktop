// Package app 是 Wails 的绑定层，也是全部前端可调方法的唯一入口。
//
// 界面通过兼容桥调用 window.go.app.App.<Method>。
// 约定：
//   - 入参出参只用可 JSON 化的类型
//   - 返回 error 时前端收到 reject，错误串已是中文可读文案
//   - 不在本层做重业务，一律转交 pkg/service
package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"duanju-desktop/internal/nativedec"
	appupdate "duanju-desktop/internal/update"

	"duanju-desktop/pkg/api"
	"duanju-desktop/pkg/auth"
	"duanju-desktop/pkg/crypto"
	"duanju-desktop/pkg/hgapi"
	"duanju-desktop/pkg/media"
	"duanju-desktop/pkg/model"
	"duanju-desktop/pkg/service"
	"duanju-desktop/pkg/store"
)

// App 是 Wails 应用上下文持有者。
//
// 字段分四组：会话层、窗口外观、隐身后台、杂项标记。
// 全部可变字段由 mu 保护；跨 goroutine 读取时先取快照。
type App struct {
	ctx     context.Context
	webMode bool

	mu             sync.RWMutex
	bootstrapOnce  sync.Once
	sess           *api.Session
	svc            *service.Service
	settings       model.Settings
	dataDir        string
	defaultDataDir string
	dataDirConfig  string
	dataMoveMu     sync.Mutex

	// 窗口与外观
	windowMode     string // normal|mini|fullscreen
	fullscreen     bool
	isMaximised    bool
	buttonsVisible bool

	// 隐身与后台
	stealth   bool
	bossKey   string
	keepAwake bool

	// 一次性标记
	dataDirNoticeDone bool
	noticesRead       bool

	// 本地媒体代理基址，如 http://127.0.0.1:52341
	proxyBase  string
	proxy      *media.Proxy
	mediaError error
	st         *store.Store

	// 二维码会话
	authMu         sync.Mutex
	authGeneration uint64
	qr             qrLoginSession
	smsPhone       string
	smsSession     *api.Session

	updater          *appupdate.Manager
	native           *nativedec.Manager
	nativeWarmMu     sync.Mutex
	nativeWarmCancel context.CancelFunc

	startedAt          time.Time
	version            string
	registrationMu     sync.Mutex
	deviceState        DeviceRegistrationState
	playbackPool       *playbackSessionPool
	playbackMu         sync.Mutex
	playbackCancel     context.CancelFunc
	playbackGeneration uint64
}

type qrLoginSession interface {
	Poll(context.Context) (*auth.Poll, error)
}

// New 构造 App。
func New() *App {
	return &App{
		startedAt:      time.Now(),
		version:        Version,
		settings:       model.DefaultSettings(),
		windowMode:     "normal",
		buttonsVisible: true,
	}
}

// NewInitialized finishes local setup before the desktop frontend can call
// bindings. Wails runs OnStartup in a goroutine alongside loading the page.
// Network registration remains in OnStartup so it never delays window creation.
func NewInitialized() *App {
	a := New()
	a.bootstrap()
	return a
}

// Version 是应用版本号，构建时可用 -ldflags 覆盖。
var Version = "1.1.0"

// OnStartup 由 Wails 在窗口就绪时调用。
func (a *App) OnStartup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	a.bootstrap()
	a.mu.Lock()
	if !a.webMode && a.native == nil {
		cacheDir, _ := os.UserCacheDir()
		a.native = nativedec.New(ctx, filepath.Join(cacheDir, "duanju-desktop-codecs", nativedec.RuntimeVersion), a.nativeEvent)
	}
	a.mu.Unlock()
	go func() { _ = a.ensureDeviceRegistration(false) }()
	a.startDownloads()
	a.watchUpdates()
	// HG_SELFTEST=1 时打一次真实接口，把结果写进临时日志。
	// 用于在无 DevTools 的环境里确认链路是否真的通。
	if os.Getenv("HG_SELFTEST") == "1" {
		go a.selfTest()
	}
}

// OnShutdown 由 Wails 在退出前调用。
func (a *App) OnShutdown(ctx context.Context) {
	a.dataMoveMu.Lock()
	defer a.dataMoveMu.Unlock()
	a.stopDownloads()
	_ = a.persistLocal()
	a.mu.Lock()
	if a.sess != nil {
		a.sess.SetToken("")
	}
	a.mu.Unlock()
	a.shutdown()
}

// bootstrap 初始化数据目录、会话与业务层。
func (a *App) bootstrap() {
	a.bootstrapOnce.Do(a.bootstrapLocal)
}

func (a *App) bootstrapLocal() {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	override := os.Getenv("HG_DATA_DIR")
	a.dataDir = override
	if override == "" {
		a.dataDir = filepath.Join(dir, "duanju-desktop")
		// Reuse an existing installation's data without moving files that may
		// still be open in an earlier process.
		legacyDir := filepath.Join(dir, "hongguo-desktop")
		if _, err := os.Stat(a.dataDir); os.IsNotExist(err) {
			if info, err := os.Stat(legacyDir); err == nil && info.IsDir() {
				a.dataDir = legacyDir
			}
		}
	}
	if absolute, err := filepath.Abs(a.dataDir); err == nil {
		a.dataDir = absolute
	}
	a.defaultDataDir = a.dataDir
	if override != "" {
		a.dataDirConfig = a.dataDir + ".data-directory.json"
	} else {
		configRoot, err := os.UserConfigDir()
		if err != nil {
			configRoot = dir
		}
		a.dataDirConfig = filepath.Join(configRoot, "duanju-desktop", "data-directory.json")
	}
	a.dataDir = selectedDataDir(a.dataDir, a.dataDirConfig)
	_ = os.MkdirAll(a.dataDir, 0o755)

	sess := api.NewSession(api.WithLogger(dbg))

	sess.SetSigner(crypto.NewDefaultSigner())

	a.sess = sess
	client := hgapi.New(sess)
	a.playbackPool = newPlaybackSessionPool(a.dataDir, sess)
	client.SetPlaybackSessionProvider(a.playbackPool)
	a.svc = service.New(client)
	if err := a.loadDeviceProfile(); err != nil {
		a.deviceState.Error = err.Error()
	}

	api.SetUpdateHost(UpdateHost)

	// 可选组件：失败不阻断启动，UI 仍可用。
	_ = a.bootstrapStore(a.dataDir)
	_ = a.restoreLocal()
	_ = a.restoreAccount()
	a.mediaError = a.bootstrapMedia(a.dataDir)
	a.updater = appupdate.New(UpdateRepository, a.version, filepath.Join(a.dataDir, "updates"), a.updateEvent)
}

// session 返回当前会话，若未初始化则先引导。
func (a *App) session() (*api.Session, error) {
	a.mu.RLock()
	s := a.sess
	a.mu.RUnlock()
	if s == nil {
		return nil, errors.New("应用尚未初始化完成")
	}
	return s, nil
}

// service 返回业务门面。
func (a *App) service() (*service.Service, error) {
	a.mu.RLock()
	s := a.svc
	a.mu.RUnlock()
	if s == nil {
		return nil, errors.New("应用尚未初始化完成")
	}
	return s, nil
}

// callCtx 给前端调用套一个带超时的 context。
func (a *App) callCtx() (context.Context, context.CancelFunc) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, 30*time.Second)
}

// UpdateHost 是自建更新服务器地址，构建时可覆盖。
var UpdateHost = ""

// UpdateRepository is the public GitHub Releases channel; no token is embedded.
var UpdateRepository = "akgb48/duanju-desktop"

// ---------------------------------------------------------------- 系统信息

// AppInfo 是前端首屏要的元信息。
type AppInfo struct {
	Version   string   `json:"version"`
	GoVersion string   `json:"goVersion"`
	Platform  string   `json:"platform"`
	Arch      string   `json:"arch"`
	DataDir   string   `json:"dataDir"`
	Uptime    int64    `json:"uptime"`
	APIStats  APIStats `json:"apiStats"`
}

// APIStats 是接口层统计。
type APIStats struct {
	Endpoints int `json:"endpoints"`
	Hosts     int `json:"hosts"`
}

// GetAppInfo 返回应用元信息。
func (a *App) appInfo() AppInfo {
	a.mu.RLock()
	dir := a.dataDir
	a.mu.RUnlock()
	return AppInfo{
		Version:   a.version,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
		DataDir:   dir,
		Uptime:    int64(time.Since(a.startedAt).Seconds()),
		APIStats: APIStats{
			Endpoints: api.DefaultRegistry.Len(),
			Hosts:     len(api.HostPool),
		},
	}
}

// ---------------------------------------------------------------- 小工具

// nowUnix 返回当前 Unix 秒。
func nowUnix() int64 { return time.Now().Unix() }

// apiCount 返回已注册接口数。
func apiCount() int { return api.DefaultRegistry.Len() }

// apiHostCount 返回主机池大小。
func apiHostCount() int { return len(api.HostPool) }

// relaunch 以分离进程方式重启可执行文件。
func relaunch(exe string) error {
	cmd := exec.Command(exe)
	return cmd.Start()
}

// pathExists 判断路径是否存在。
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
