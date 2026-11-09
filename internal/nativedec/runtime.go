package nativedec

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Pinned LGPL shared build. The archive is verified before extraction; the
// installed executable and DLLs are verified against its recorded file hashes.
const RuntimeURL = "https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases/download/autobuild-2026-11-09-12-42/ffmpeg-n8.1.3-34-ga35157abb9-win64-lgpl-shared-8.1.zip"
const RuntimeSHA256 = "5e1b1ee3d9f85c2c2dedbe9db0036b9232f1f9bdd1a5e644fc7128e4bfcb6877"
const RuntimeVersion = "8.1.3-34-a35157abb9"

type runtimeFiles struct {
	Archive string            `json:"archive"`
	Files   map[string]string `json:"files"`
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func installedRuntime(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil || len(b) > 64<<10 {
		return ""
	}
	var manifest runtimeFiles
	if json.Unmarshal(b, &manifest) != nil || manifest.Archive != RuntimeSHA256 || len(manifest.Files) < 2 {
		return ""
	}
	for name, hash := range manifest.Files {
		if filepath.Base(name) != name {
			return ""
		}
		if got, err := fileHash(filepath.Join(dir, name)); err != nil || got != hash {
			return ""
		}
	}
	if _, ok := manifest.Files["ffmpeg.exe"]; !ok {
		return ""
	}
	return filepath.Join(dir, "ffmpeg.exe")
}

func installRuntime(ctx context.Context, dir string) (string, error) {
	if path := installedRuntime(dir); path != "" {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return "", err
	}
	archive, err := os.CreateTemp(filepath.Dir(dir), ".ffmpeg-*.zip")
	if err != nil {
		return "", err
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	defer archive.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RuntimeURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "DuanjuDesktop")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("下载解码组件返回 HTTP %d", res.StatusCode)
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(archive, h), io.LimitReader(res.Body, 128<<20))
	if err != nil {
		return "", err
	}
	if size >= 128<<20 || hex.EncodeToString(h.Sum(nil)) != RuntimeSHA256 {
		return "", errors.New("解码组件校验失败")
	}
	if err = archive.Close(); err != nil {
		return "", err
	}
	return extractRuntime(archivePath, dir)
}

func extractRuntime(archivePath, dir string) (string, error) {
	z, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer z.Close()
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".codecs-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	manifest := runtimeFiles{Archive: RuntimeSHA256, Files: map[string]string{}}
	var total uint64
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		ext := strings.ToLower(filepath.Ext(base))
		binary := strings.Contains(f.Name, "/bin/") && (ext == ".dll" || base == "ffmpeg.exe")
		license := strings.Contains(strings.ToLower(base), "license") || strings.HasPrefix(base, "COPYING")
		if !binary && !license {
			continue
		}
		total += f.UncompressedSize64
		if total > 400<<20 || f.UncompressedSize64 > 160<<20 {
			return "", errors.New("解码组件解压大小异常")
		}
		if filepath.Base(base) != base || base == "." || base == ".." {
			return "", errors.New("组件路径无效")
		}
		target := filepath.Join(stage, base)
		in, err := f.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			in.Close()
			return "", err
		}
		h := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, 160<<20))
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		manifest.Files[base] = hex.EncodeToString(h.Sum(nil))
	}
	if _, ok := manifest.Files["ffmpeg.exe"]; !ok {
		return "", errors.New("组件缺少 ffmpeg.exe")
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err = os.WriteFile(filepath.Join(stage, "runtime.json"), data, 0600); err != nil {
		return "", err
	}
	// A corrupt installation is kept for inspection instead of overwriting any
	// file that may still be loaded by a decoder process.
	if _, err = os.Stat(dir); err == nil {
		return "", errors.New("现有解码组件不完整，请退出程序后清理解码组件目录")
	}
	if err = os.Rename(stage, dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "ffmpeg.exe"), nil
}
