// Package update implements the GitHub Releases update channel. It never sends
// application accounts, device credentials or GitHub tokens to the update host.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"duanju-desktop/pkg/updproto"
)

const maxPackageSize = 512 << 20

type Release struct {
	Version     string `json:"version"`
	Changelog   string `json:"changelog"`
	Size        int64  `json:"size"`
	PublishedAt int64  `json:"publishedAt"`
}

// State retains the reference frontend's update-panel contract.
type State struct {
	Version      string   `json:"version"`
	Current      string   `json:"current"`
	Latest       string   `json:"latest"`
	Enabled      bool     `json:"enabled"`
	Checking     bool     `json:"checking"`
	LastCheck    int64    `json:"lastCheck"`
	Error        string   `json:"error"`
	Available    *Release `json:"available"`
	Phase        string   `json:"phase"`
	Stage        string   `json:"stage"`
	Progress     float64  `json:"progress"`
	Bytes        int64    `json:"bytes"`
	Total        int64    `json:"total"`
	Speed        int64    `json:"speed"`
	CanApply     bool     `json:"canApply"`
	ManualURL    string   `json:"manualUrl"`
	DownloadPage string   `json:"downloadPage"`
	LastResult   string   `json:"lastResult"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	Tag        string    `json:"tag_name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []asset   `json:"assets"`
}

type Manager struct {
	mu                  sync.Mutex
	state               State
	repo, dir, apiBase  string
	manifestURL         string
	client              *http.Client
	asset               asset
	checksum, readyPath string
	gate                chan struct{}
	ctx                 context.Context
	cancel              context.CancelFunc
	onState             func(State)
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

func New(repo, version, dir string, onState func(State)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	page := "https://github.com/" + repo + "/releases"
	return &Manager{repo: repo, dir: dir, apiBase: "https://api.github.com", client: &http.Client{Timeout: 30 * time.Second},
		manifestURL: page + "/latest/download/update.json",
		gate:        make(chan struct{}, 1), ctx: ctx, cancel: cancel, onState: onState,
		state: State{Version: version, Current: version, Enabled: repositoryPattern.MatchString(repo), Phase: "idle", Stage: "idle", ManualURL: page, DownloadPage: page}}
}

func (m *Manager) Close()                { m.cancel() }
func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }

func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state)
}

func cloneState(s State) State {
	if s.Available != nil {
		copy := *s.Available
		s.Available = &copy
	}
	return s
}

func (m *Manager) set(fn func(*State)) {
	m.mu.Lock()
	fn(&m.state)
	m.state.Stage = m.state.Phase
	s := cloneState(m.state)
	m.mu.Unlock()
	if m.onState != nil {
		m.onState(s)
	}
}

func (m *Manager) fail(err error) error {
	m.set(func(s *State) { s.Checking = false; s.Phase = "failed"; s.Error = err.Error() })
	return err
}

func (m *Manager) request(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DuanjuDesktop/"+m.state.Version)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 更新源失败: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 更新源返回 HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("更新信息超过大小限制")
	}
	return b, nil
}

func (m *Manager) Check(ctx context.Context) (State, error) {
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	default:
		return m.Snapshot(), nil
	}
	if !m.state.Enabled {
		return m.Snapshot(), errors.New("更新仓库未配置")
	}
	if m.Snapshot().Phase == "ready" {
		return m.Snapshot(), nil
	}
	m.set(func(s *State) { s.Checking = true; s.Phase = "checking"; s.Error = "" })
	fail := func(err error) (State, error) { m.fail(err); return m.Snapshot(), err }
	release, err := m.loadRelease(ctx)
	if err != nil {
		return fail(err)
	}
	if !versionPattern.MatchString(release.Tag) || release.Draft || release.Prerelease {
		return fail(errors.New("GitHub 未返回有效的正式发布版本"))
	}
	latest := strings.TrimPrefix(release.Tag, "v")
	if updproto.CompareVersion(latest, m.state.Version) <= 0 {
		m.set(func(s *State) {
			s.Checking = false
			s.Phase = "idle"
			s.Latest = latest
			s.Available = nil
			s.LastCheck = time.Now().Unix()
			s.LastResult = "当前已是最新版本"
		})
		return m.Snapshot(), nil
	}
	name := "DuanjuDesktop-" + latest + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".exe"
	var selected, sums asset
	for _, a := range release.Assets {
		if a.Name == name {
			selected = a
		}
		if a.Name == "SHA256SUMS.txt" {
			sums = a
		}
	}
	if selected.URL == "" || selected.Size < 1 || selected.Size > maxPackageSize {
		return fail(errors.New("新版本缺少适用于当前系统的更新包"))
	}
	if !m.trustedAssetURL(selected.URL, release.Tag, name) {
		return fail(errors.New("更新包地址不属于指定 GitHub 仓库"))
	}
	checksum := strings.TrimPrefix(selected.Digest, "sha256:")
	if !validHash(checksum) {
		if !m.trustedAssetURL(sums.URL, release.Tag, "SHA256SUMS.txt") {
			return fail(errors.New("更新包缺少 SHA256 校验值"))
		}
		data, err := m.request(ctx, sums.URL, 64<<10)
		if err != nil {
			return fail(err)
		}
		for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
				checksum = strings.ToLower(fields[0])
			}
		}
		if !validHash(checksum) {
			return fail(errors.New("更新包 SHA256 校验文件无效"))
		}
	}
	m.mu.Lock()
	m.asset = selected
	m.checksum = checksum
	m.mu.Unlock()
	m.set(func(s *State) {
		s.Checking = false
		s.Phase = "available"
		s.Latest = latest
		s.Available = &Release{Version: latest, Changelog: release.Body, Size: selected.Size, PublishedAt: release.Published.Unix()}
		s.LastCheck = time.Now().Unix()
		s.LastResult = "发现新版本 " + latest
		s.Total = selected.Size
		s.CanApply = runtime.GOOS == "windows"
	})
	return m.Snapshot(), nil
}

// A release asset avoids the unauthenticated REST API's shared-IP rate limit.
// Older releases without update.json continue to work through the REST fallback.
func (m *Manager) loadRelease(ctx context.Context) (githubRelease, error) {
	if m.manifestURL != "" {
		if raw, err := m.request(ctx, m.manifestURL, 2<<20); err == nil {
			if index, err := updproto.DecodeIndex(raw); err == nil {
				v, err := updproto.ParseVersion(index.Version)
				if err == nil && versionPattern.MatchString(index.Version) {
					r := githubRelease{Tag: "v" + strings.TrimPrefix(index.Version, "v"), Body: index.Notes, Prerelease: v.IsPre(), Published: time.Unix(index.PubDate, 0)}
					for _, p := range index.Packages {
						u, _ := url.Parse(p.URL)
						if u != nil {
							r.Assets = append(r.Assets, asset{Name: path.Base(u.Path), URL: p.URL, Size: p.Size, Digest: "sha256:" + p.SHA256})
						}
					}
					return r, nil
				}
			}
		}
	}
	raw, err := m.request(ctx, m.apiBase+"/repos/"+m.repo+"/releases/latest", 2<<20)
	if err != nil {
		return githubRelease{}, err
	}
	var r githubRelease
	if json.Unmarshal(raw, &r) != nil {
		return r, errors.New("GitHub 更新信息格式无效")
	}
	return r, nil
}

func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}

func (m *Manager) trustedAssetURL(raw, tag, name string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if m.apiBase != "https://api.github.com" {
		base, _ := url.Parse(m.apiBase)
		return u.Scheme == base.Scheme && u.Host == base.Host
	}
	return u.Scheme == "https" && u.Host == "github.com" && u.Path == "/"+m.repo+"/releases/download/"+tag+"/"+name
}

func (m *Manager) StartDownload() error {
	select {
	case m.gate <- struct{}{}:
	default:
		return errors.New("更新任务正在执行")
	}
	m.mu.Lock()
	a, checksum := m.asset, m.checksum
	available := m.state.Available != nil
	m.mu.Unlock()
	if !available || a.URL == "" || !validHash(checksum) {
		<-m.gate
		return errors.New("请先检查并选择可用的新版本")
	}
	m.set(func(s *State) {
		s.Phase = "downloading"
		s.Progress = 0
		s.Error = ""
		s.CanApply = false
		s.LastResult = ""
	})
	go func() {
		defer func() { <-m.gate }()
		if err := m.download(a, checksum); err != nil {
			m.fail(err)
		}
	}()
	return nil
}

// The reference UI awaits this RPC, then invokes ApplyUpdate. Progress events
// remain independent of the pending download call.
func (m *Manager) Download(ctx context.Context) error {
	if m.Snapshot().Phase == "ready" {
		return nil
	}
	if err := m.StartDownload(); err != nil {
		return err
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-timer.C:
		}
		s := m.Snapshot()
		if s.Phase == "ready" {
			return nil
		}
		if s.Phase != "downloading" {
			return errors.New(s.Error)
		}
	}
}

func (m *Manager) download(a asset, checksum string) error {
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Minute)
	defer cancel()
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".download-*.part")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "DuanjuDesktop")
	// Large packages have a task deadline, rather than a short total client timeout.
	client := *m.client
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载更新失败: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("下载更新返回 HTTP %d", res.StatusCode)
	}
	if res.ContentLength > 0 && res.ContentLength != a.Size {
		return errors.New("更新包大小与发布信息不符")
	}
	h := sha256.New()
	writer := io.MultiWriter(f, h)
	var done int64
	buf := make([]byte, 128<<10)
	started, last := time.Now(), time.Now()
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > a.Size {
				return errors.New("更新包超过声明大小")
			}
			if _, err = writer.Write(buf[:n]); err != nil {
				return err
			}
			if time.Since(last) > 150*time.Millisecond {
				m.set(func(s *State) {
					s.Bytes = done
					s.Progress = float64(done) / float64(a.Size)
					s.Speed = int64(float64(done) / time.Since(started).Seconds())
				})
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if done != a.Size || hex.EncodeToString(h.Sum(nil)) != checksum {
		return errors.New("更新包 SHA256 或长度校验失败，已放弃更新")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	target := strings.TrimSuffix(temp, ".part") + ".exe"
	if err = os.Rename(temp, target); err != nil {
		return err
	}
	m.mu.Lock()
	m.readyPath = target
	m.mu.Unlock()
	m.set(func(s *State) {
		s.Phase = "ready"
		s.Progress = 1
		s.Bytes = done
		s.CanApply = true
		s.LastResult = "更新已下载，可以重启安装"
	})
	return nil
}

// Apply stages a verified package beside the running EXE. A copied helper waits
// for this process to exit, replaces the EXE, and rolls back if restart fails.
func (m *Manager) Apply(executable string, args []string) error {
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	default:
		return errors.New("更新任务正在执行")
	}
	m.mu.Lock()
	ready, hash := m.readyPath, m.checksum
	canApply := m.state.CanApply && m.state.Phase == "ready"
	m.mu.Unlock()
	if !canApply {
		return errors.New("没有经过校验的更新包")
	}
	if err := launchReplacement(m.dir, executable, ready, hash, args); err != nil {
		return m.fail(err)
	}
	m.set(func(s *State) { s.Phase = "applying"; s.CanApply = false })
	return nil
}
