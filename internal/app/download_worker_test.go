package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitDownload(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("下载状态未在 5 秒内完成预期变化")
}

func TestDownloadDirectResumeAndRangeIgnored(t *testing.T) {
	content := bytes.Repeat([]byte("mp4-bytes"), 8192)
	for _, honorRange := range []bool{true, false} {
		t.Run(fmt.Sprint("honorRange=", honorRange), func(t *testing.T) {
			var requested atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested.Store(r.Header.Get("Range") == "bytes=1024-")
				w.Header().Set("Content-Type", "video/mp4")
				if honorRange {
					http.ServeContent(w, r, "test.mp4", time.Time{}, bytes.NewReader(content))
					return
				}
				_, _ = w.Write(content)
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "video.part")
			if err := os.WriteFile(path, content[:1024], 0o600); err != nil {
				t.Fatal(err)
			}
			done, total, err := downloadDirect(context.Background(), srv.Client(), srv.URL, path, nil)
			if err != nil || done != int64(len(content)) || total != int64(len(content)) {
				t.Fatalf("downloadDirect = %d, %d, %v", done, total, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, content) || !requested.Load() {
				t.Fatalf("续传内容不一致，Range=%v, err=%v", requested.Load(), err)
			}
		})
	}
}

func TestDownloadDirectRejectsErrorsAndTruncation(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType, body string
		status, declared      int
	}{
		{"http-error", "video/mp4", "failure", 403, 0},
		{"html", "text/html", "<html>login</html>", 200, 0},
		{"hls", "application/vnd.apple.mpegurl", "#EXTM3U", 200, 0},
		{"truncated", "video/mp4", "short", 200, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mediaType)
				if tc.declared > 0 {
					w.Header().Set("Content-Length", strconv.Itoa(tc.declared))
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, _, err := downloadDirect(context.Background(), srv.Client(), srv.URL, filepath.Join(t.TempDir(), "partial"), nil)
			if err == nil {
				t.Fatal("错误响应被当成下载成功")
			}
		})
	}
}

func TestDownloadDirectCompletedPartialCanFinish(t *testing.T) {
	content := []byte("complete video")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "test.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "video.part")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	done, total, err := downloadDirect(context.Background(), srv.Client(), srv.URL, path, nil)
	if err != nil || done != int64(len(content)) || total != done {
		t.Fatalf("完整的临时文件无法提交: %d %d %v", done, total, err)
	}
}

func TestDownloadQueuePauseResumeAndPersistence(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), 32768)
	var rangeSeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if header := r.Header.Get("Range"); header != "" {
			rangeSeen.Store(true)
			_, _ = fmt.Sscanf(header, "bytes=%d-", &start)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)-start))
		if start > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(content)-1, len(content)))
			w.WriteHeader(http.StatusPartialContent)
		}
		for cursor := start; cursor < len(content); cursor += 4096 {
			end := cursor + 4096
			if end > len(content) {
				end = len(content)
			}
			if _, err := w.Write(content[cursor:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	queuePath := filepath.Join(dir, "downloads.json")
	resolver := func(context.Context, DownloadItem) (string, error) { return srv.URL, nil }
	q, err := newDownloadQueue(queuePath, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer q.close()
	path := filepath.Join(dir, "out", "episode.mp4")
	if _, err := q.add([]DownloadItem{{ID: "one", VideoID: "v1", State: "pending", FilePath: path}}); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, func() bool { list := q.list(); return len(list) == 1 && list[0].DoneBytes > 0 })
	if err := q.pause([]string{"one"}); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.active["one"] == nil
	})
	if list := q.list(); list[0].State != "paused" {
		t.Fatalf("暂停后的状态: %+v", list[0])
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("暂停任务不应有已完成的最终文件")
	}
	if err := q.resume([]string{"one"}); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, func() bool { return q.list()[0].State == "done" })
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, content) || !rangeSeen.Load() {
		t.Fatalf("恢复后的文件校验失败，Range=%v, err=%v", rangeSeen.Load(), err)
	}
	q2, err := newDownloadQueue(queuePath, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer q2.close()
	if list := q2.list(); len(list) != 1 || list[0].State != "done" || list[0].Progress != 100 {
		t.Fatalf("重启后下载记录不一致: %+v", list)
	}
	if err := q.remove([]string{"one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("删除任务后文件仍然存在")
	}
}

func TestDownloadQueueHasTwoWorkersAndCancelsDeletion(t *testing.T) {
	var active, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	dir := t.TempDir()
	q, err := newDownloadQueue(filepath.Join(dir, "queue.json"), func(context.Context, DownloadItem) (string, error) { return srv.URL, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer q.close()
	var tasks []DownloadItem
	var ids []string
	for i := 0; i < 6; i++ {
		id := strconv.Itoa(i)
		ids = append(ids, id)
		tasks = append(tasks, DownloadItem{ID: id, State: "pending", FilePath: filepath.Join(dir, id+".mp4")})
	}
	if _, err := q.add(tasks); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, func() bool { return active.Load() == 2 })
	if err := q.remove(ids); err != nil {
		t.Fatal(err)
	}
	if peak.Load() > 2 || len(q.list()) != 0 {
		t.Fatalf("并发或删除失败: peak=%d tasks=%v", peak.Load(), q.list())
	}
}

func TestDownloadNameCannotEscapeDirectory(t *testing.T) {
	for _, input := range []string{"../../outside", "C:\\Windows\\file", "CON", "", "a/b", "a_b"} {
		name := safeDownloadName(input)
		if strings.ContainsAny(name, `/\\:.`) || !strings.HasPrefix(name, "hg-") {
			t.Fatalf("不安全的文件名: %q => %q", input, name)
		}
	}
	if safeDownloadName("a/b") == safeDownloadName("a_b") {
		t.Fatal("不同 ID 不应映射到同一个下载文件")
	}
}
