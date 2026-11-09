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
	"path"
	"strings"
	"time"

	"duanju-desktop/pkg/updproto"
)

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

func (m *Manager) request(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DuanjuDesktop/"+m.Snapshot().Version)
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

// A release asset avoids the unauthenticated REST API's shared-IP rate limit.
// Older releases without update.json continue to work through the REST fallback.
func (m *Manager) loadRelease(ctx context.Context) (githubRelease, error) {
	if m.manifestURL != "" {
		if release, err := m.manifestRelease(ctx); err == nil {
			return release, nil
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

func (m *Manager) manifestRelease(ctx context.Context) (githubRelease, error) {
	raw, err := m.request(ctx, m.manifestURL, 2<<20)
	if err != nil {
		return githubRelease{}, err
	}
	index, err := updproto.DecodeIndex(raw)
	if err != nil {
		return githubRelease{}, err
	}
	v, err := updproto.ParseVersion(index.Version)
	if err != nil || !versionPattern.MatchString(index.Version) {
		return githubRelease{}, errors.New("更新清单版本号无效")
	}
	release := githubRelease{
		Tag: "v" + strings.TrimPrefix(index.Version, "v"), Body: index.Notes,
		Prerelease: v.IsPre(), Published: time.Unix(index.PubDate, 0),
	}
	for _, p := range index.Packages {
		if u, _ := url.Parse(p.URL); u != nil {
			release.Assets = append(release.Assets, asset{
				Name: path.Base(u.Path), URL: p.URL, Size: p.Size, Digest: "sha256:" + p.SHA256,
			})
		}
	}
	return release, nil
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
