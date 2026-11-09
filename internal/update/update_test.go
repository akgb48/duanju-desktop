package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func updateFixture(t *testing.T, modify func(*githubRelease)) (*Manager, []byte) {
	t.Helper()
	payload := []byte("MZ-verified-new-program")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials reached update source")
		}
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			release := githubRelease{Tag: "v1.1.0", Body: "播放优化", Published: time.Now(), Assets: []asset{{Name: "DuanjuDesktop-1.1.0-" + runtime.GOOS + "-" + runtime.GOARCH + ".exe", URL: server.URL + "/package", Size: int64(len(payload)), Digest: "sha256:" + digest(payload)}}}
			if modify != nil {
				modify(&release)
			}
			json.NewEncoder(w).Encode(release)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	m := New("owner/repo", "1.0.3", t.TempDir(), nil)
	m.apiBase = server.URL
	m.manifestURL = ""
	m.client = server.Client()
	t.Cleanup(m.Close)
	return m, payload
}

func waitDownload(t *testing.T, m *Manager) State {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := m.Snapshot()
		if s.Phase != "downloading" {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("download did not finish")
	return State{}
}

func TestGitHubUpdateCheckDownloadAndVerify(t *testing.T) {
	m, payload := updateFixture(t, nil)
	s, err := m.Check(context.Background())
	if err != nil || s.Available == nil || s.Available.Version != "1.1.0" || s.Phase != "available" {
		t.Fatalf("check: %+v %v", s, err)
	}
	if err = m.StartDownload(); err != nil {
		t.Fatal(err)
	}
	s = waitDownload(t, m)
	if !s.CanApply || s.Phase != "ready" || s.Progress != 1 {
		t.Fatalf("download: %+v", s)
	}
	b, err := os.ReadFile(m.readyPath)
	if err != nil || string(b) != string(payload) {
		t.Fatal("verified package was not staged")
	}
}

func TestGitHubUpdateRejectsBadChecksum(t *testing.T) {
	m, _ := updateFixture(t, func(r *githubRelease) { r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64) })
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.StartDownload(); err != nil {
		t.Fatal(err)
	}
	s := waitDownload(t, m)
	if s.CanApply || s.Phase != "failed" || m.readyPath != "" || !strings.Contains(s.Error, "校验失败") {
		t.Fatalf("bad package accepted: %+v", s)
	}
	files, _ := os.ReadDir(m.dir)
	if len(files) != 0 {
		t.Fatal("failed download retained partial package")
	}
}

func TestGitHubUpdateRejectsWrongReleaseOrUntrustedURL(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*githubRelease)
	}{
		{"draft", func(r *githubRelease) { r.Draft = true }},
		{"prerelease", func(r *githubRelease) { r.Prerelease = true }},
		{"missing-package", func(r *githubRelease) { r.Assets = nil }},
		{"untrusted-host", func(r *githubRelease) { r.Assets[0].URL = "https://untrusted.example/new.exe" }},
		{"missing-hash", func(r *githubRelease) { r.Assets[0].Digest = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := updateFixture(t, tc.modify)
			s, err := m.Check(context.Background())
			if err == nil || s.Phase != "failed" || s.CanApply {
				t.Fatalf("invalid release accepted: %+v %v", s, err)
			}
		})
	}
}

func TestGitHubUpdateNeverDowngrades(t *testing.T) {
	m, _ := updateFixture(t, func(r *githubRelease) { r.Tag = "v1.0.2" })
	s, err := m.Check(context.Background())
	if err != nil || s.Available != nil || s.Phase != "idle" {
		t.Fatalf("downgrade offered: %+v %v", s, err)
	}
}

func TestReferenceDownloadCallReturnsOnlyWhenReady(t *testing.T) {
	m, _ := updateFixture(t, nil)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply("unused.exe", nil); err == nil {
		t.Fatal("offered release was accepted before download")
	}
	if err := m.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.Phase != "ready" || s.Progress != 1 || !s.CanApply {
		t.Fatalf("download returned prematurely: %+v", s)
	}
}

func TestStaticReleaseManifestAvoidsRESTAndValidatesPackage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update.json" {
			t.Error("static manifest unexpectedly used REST")
			http.Error(w, "rate limit", 403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"version": "1.1.0", "pubDate": time.Now().Unix(), "packages": []any{map[string]any{"platform": runtime.GOOS, "arch": runtime.GOARCH, "url": server.URL + "/DuanjuDesktop-1.1.0-" + runtime.GOOS + "-" + runtime.GOARCH + ".exe", "size": 123, "sha256": strings.Repeat("a", 64)}}})
	}))
	defer server.Close()
	m := New("owner/repo", "1.0.3", t.TempDir(), nil)
	defer m.Close()
	m.apiBase = server.URL
	m.manifestURL = server.URL + "/update.json"
	s, err := m.Check(context.Background())
	if err != nil || s.Available == nil || s.Latest != "1.1.0" {
		t.Fatalf("manifest: %+v %v", s, err)
	}
}

func TestGitHubAssetMustBelongToConfiguredRelease(t *testing.T) {
	m := New("owner/repo", "1.0.0", t.TempDir(), nil)
	defer m.Close()
	for _, url := range []string{"https://github.com/another/repo/releases/download/v1.1.0/a.exe", "http://github.com/owner/repo/releases/download/v1.1.0/a.exe", "https://github.com/owner/repo/releases/download/v1.0.0/a.exe", "https://github.com/owner/repo/releases/download/v1.1.0/a.exe?redirect=other"} {
		if m.trustedAssetURL(url, "v1.1.0", "a.exe") {
			t.Fatalf("untrusted release accepted: %s", url)
		}
	}
	if !m.trustedAssetURL("https://github.com/owner/repo/releases/download/v1.1.0/a.exe", "v1.1.0", "a.exe") {
		t.Fatal("own release rejected")
	}
}

func replacementFixture(t *testing.T) replacementPlan {
	t.Helper()
	dir := t.TempDir()
	p := replacementPlan{PID: os.Getpid(), Target: filepath.Join(dir, "短剧电脑版.exe"), Staged: filepath.Join(dir, ".duanju-update-new.exe"), Args: []string{"--data-dir", filepath.Join(dir, "data with spaces")}}
	p.Backup = p.Staged + ".backup"
	p.SHA256 = digest([]byte("new-program"))
	if err := os.WriteFile(p.Target, []byte("old-program"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Staged, []byte("new-program"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReplacementPreservesArgumentsAndRollsBackOnLaunchFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			p := replacementFixture(t)
			if err := validatePlan(p); err != nil {
				t.Fatal(err)
			}
			starts := 0
			err := replaceAndRestart(p, func(path string, args []string) error {
				starts++
				if path != p.Target || !reflect.DeepEqual(args, p.Args) {
					t.Error("restart changed executable or arguments")
				}
				if fail && starts == 1 {
					return errors.New("launch failed")
				}
				return nil
			})
			b, _ := os.ReadFile(p.Target)
			if fail {
				if err == nil || string(b) != "old-program" || starts != 2 {
					t.Fatalf("rollback: %q, starts=%d, err=%v", b, starts, err)
				}
			} else if err != nil || string(b) != "new-program" || starts != 1 {
				t.Fatalf("replace: %q, starts=%d, err=%v", b, starts, err)
			}
		})
	}
}

func TestReplacementChecksAgainBeforeTouchingOldProgram(t *testing.T) {
	p := replacementFixture(t)
	os.WriteFile(p.Staged, []byte("modified-after-download"), 0700)
	if err := replaceAndRestart(p, func(string, []string) error { t.Fatal("invalid package launched"); return nil }); err == nil {
		t.Fatal("tampered package accepted")
	}
	b, _ := os.ReadFile(p.Target)
	if string(b) != "old-program" {
		t.Fatal("old program was modified before verification")
	}
	p.Staged = filepath.Join(t.TempDir(), "unrelated.exe")
	if err := validatePlan(p); err == nil {
		t.Fatal("replacement accepted another directory")
	}
}
