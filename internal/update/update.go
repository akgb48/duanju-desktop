// Package update implements the GitHub Releases update channel. It never sends
// application accounts, device credentials or GitHub tokens to the update host.
package update

import (
	"context"
	"errors"
	"net/http"
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
	return &Manager{
		repo: repo, dir: dir, apiBase: "https://api.github.com",
		client:      &http.Client{Timeout: 30 * time.Second},
		manifestURL: page + "/latest/download/update.json",
		gate:        make(chan struct{}, 1), ctx: ctx, cancel: cancel, onState: onState,
		state: State{
			Version: version, Current: version, Enabled: repositoryPattern.MatchString(repo),
			Phase: "idle", Stage: "idle", ManualURL: page, DownloadPage: page,
		},
	}
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

func (m *Manager) Check(ctx context.Context) (State, error) {
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	default:
		return m.Snapshot(), nil
	}
	current := m.Snapshot()
	if !current.Enabled {
		return m.Snapshot(), errors.New("更新仓库未配置")
	}
	if current.Phase == "ready" {
		return current, nil
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
	if updproto.CompareVersion(latest, current.Version) <= 0 {
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
