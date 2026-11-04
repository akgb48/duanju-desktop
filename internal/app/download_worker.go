package app

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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type downloadResolver func(context.Context, DownloadItem) (string, error)

type activeDownload struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type downloadQueue struct {
	addRemoveMu sync.Mutex // Keep re-added tasks from sharing files with a deletion.
	mu          sync.Mutex
	tasks       map[string]DownloadItem
	active      map[string]*activeDownload
	path        string
	resolve     downloadResolver
	client      *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	wake        chan struct{}
	wg          sync.WaitGroup
	lastSave    time.Time
}

var downloadQueues = struct {
	sync.Mutex
	items map[*App]*downloadQueue
}{items: make(map[*App]*downloadQueue)}

func (a *App) downloadManager() (*downloadQueue, error) {
	downloadQueues.Lock()
	defer downloadQueues.Unlock()
	if q := downloadQueues.items[a]; q != nil {
		return q, nil
	}
	a.mu.RLock()
	dir := a.dataDir
	a.mu.RUnlock()
	if dir == "" {
		return nil, errors.New("下载队列尚未初始化")
	}
	q, err := newDownloadQueue(filepath.Join(dir, "downloads.json"), func(ctx context.Context, task DownloadItem) (string, error) {
		svc, err := a.service()
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		info, err := svc.VideoDetail(ctx, task.VideoID)
		if err != nil {
			return "", err
		}
		if len(info.Sources) == 0 {
			return "", errors.New("该集没有可下载的播放地址")
		}
		source := info.Sources[0]
		for _, candidate := range info.Sources {
			if candidate.Quality == task.Quality {
				source = candidate
				break
			}
		}
		if strings.EqualFold(source.Format, "hls") {
			return "", errors.New("当前下载器支持直连视频，暂不支持 HLS 分片")
		}
		if len(source.ContentKey) > 0 {
			a.mu.RLock()
			proxy := a.proxyBase
			a.mu.RUnlock()
			if proxy == "" {
				return "", errors.New("解密视频需要本地媒体代理")
			}
			return proxy + "/media/" + task.VideoID + "/" + source.Quality, nil
		}
		return source.URL, nil
	})
	if err != nil {
		return nil, err
	}
	downloadQueues.items[a] = q
	return q, nil
}

func (a *App) startDownloads() error {
	_, err := a.downloadManager()
	return err
}

func (a *App) stopDownloads() {
	downloadQueues.Lock()
	q := downloadQueues.items[a]
	delete(downloadQueues.items, a)
	downloadQueues.Unlock()
	if q != nil {
		q.close()
	}
}

func newDownloadQueue(path string, resolver downloadResolver) (*downloadQueue, error) {
	ctx, cancel := context.WithCancel(context.Background())
	q := &downloadQueue{
		tasks: make(map[string]DownloadItem), active: make(map[string]*activeDownload),
		path: path, resolve: resolver, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 2),
		client: &http.Client{Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConns: 4, IdleConnTimeout: 60 * time.Second,
		}},
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		cancel()
		return nil, fmt.Errorf("读取下载队列失败: %w", err)
	}
	if len(data) != 0 {
		var tasks []DownloadItem
		if err := json.Unmarshal(data, &tasks); err != nil {
			cancel()
			return nil, fmt.Errorf("下载队列文件损坏，已保留原文件: %w", err)
		}
		for _, task := range tasks {
			if task.ID == "" || task.FilePath == "" {
				continue
			}
			if task.State == "running" {
				task.State = "paused"
			}
			task.Speed = 0
			q.tasks[task.ID] = task
		}
	}
	for i := 0; i < 2; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	q.notify()
	return q, nil
}

func safeDownloadName(value string) string {
	var name strings.Builder
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			name.WriteRune(c)
		} else {
			name.WriteByte('_')
		}
		if name.Len() >= 80 {
			break
		}
	}
	// The prefix prevents Windows reserved device names (CON, AUX, NUL...).
	sum := sha256.Sum256([]byte(value))
	return "hg-" + name.String() + "-" + hex.EncodeToString(sum[:4])
}

func (q *downloadQueue) listLocked() []DownloadItem {
	list := make([]DownloadItem, 0, len(q.tasks))
	for _, task := range q.tasks {
		list = append(list, task)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt == list[j].CreatedAt {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt < list[j].CreatedAt
	})
	return list
}

func (q *downloadQueue) list() []DownloadItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.listLocked()
}

func (q *downloadQueue) persistLocked() error {
	data, err := json.MarshalIndent(q.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("保存下载队列失败: %w", err)
	}
	if err = os.Rename(tmp, q.path); err != nil {
		return fmt.Errorf("提交下载队列失败: %w", err)
	}
	q.lastSave = time.Now()
	return nil
}

func (q *downloadQueue) notify() {
	for i := 0; i < 2; i++ {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
}

func (q *downloadQueue) add(tasks []DownloadItem) (int, error) {
	q.addRemoveMu.Lock()
	defer q.addRemoveMu.Unlock()
	q.mu.Lock()
	var added []string
	for _, task := range tasks {
		if _, exists := q.tasks[task.ID]; !exists {
			q.tasks[task.ID] = task
			added = append(added, task.ID)
		}
	}
	err := q.persistLocked()
	if err != nil {
		for _, id := range added {
			delete(q.tasks, id)
		}
	}
	q.mu.Unlock()
	if err != nil {
		return 0, err
	}
	q.notify()
	return len(added), nil
}

func (q *downloadQueue) pause(ids []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		task, exists := q.tasks[id]
		if !exists || task.State != "running" && task.State != "pending" {
			continue
		}
		task.State, task.Speed = "paused", 0
		q.tasks[id] = task
		if job := q.active[id]; job != nil {
			job.cancel()
		}
	}
	return q.persistLocked()
}

func (q *downloadQueue) resume(ids []string) error {
	q.mu.Lock()
	for _, id := range ids {
		task, exists := q.tasks[id]
		if exists && (task.State == "paused" || task.State == "failed") {
			task.State, task.Error = "pending", ""
			q.tasks[id] = task
		}
	}
	err := q.persistLocked()
	q.mu.Unlock()
	if err == nil {
		q.notify()
	}
	return err
}

func (q *downloadQueue) remove(ids []string) error {
	q.addRemoveMu.Lock()
	defer q.addRemoveMu.Unlock()
	q.mu.Lock()
	var removed []DownloadItem
	for _, id := range ids {
		if task, exists := q.tasks[id]; exists {
			removed = append(removed, task)
			delete(q.tasks, id)
		}
	}
	if err := q.persistLocked(); err != nil {
		for _, task := range removed {
			q.tasks[task.ID] = task
		}
		q.mu.Unlock()
		return err
	}
	var jobs []*activeDownload
	for _, task := range removed {
		if job := q.active[task.ID]; job != nil {
			job.cancel()
			jobs = append(jobs, job)
		}
	}
	q.mu.Unlock()
	// On Windows, wait for the response and file handles to close before removal.
	for _, job := range jobs {
		<-job.done
	}
	var err error
	for _, task := range removed {
		path := task.FilePath
		for _, candidate := range []string{path, path + ".part", downloadResumePath(path + ".part")} {
			if e := os.Remove(candidate); e != nil && !os.IsNotExist(e) {
				err = errors.Join(err, fmt.Errorf("删除下载文件失败: %w", e))
			}
		}
	}
	return err
}

func (q *downloadQueue) next() (DownloadItem, *activeDownload, context.Context, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx.Err() != nil {
		return DownloadItem{}, nil, nil, false
	}
	for _, task := range q.listLocked() {
		if task.State != "pending" || q.active[task.ID] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(q.ctx)
		job := &activeDownload{cancel: cancel, done: make(chan struct{})}
		q.active[task.ID] = job
		task.State, task.Error = "running", ""
		q.tasks[task.ID] = task
		if err := q.persistLocked(); err != nil {
			task.State, task.Error = "failed", err.Error()
			q.tasks[task.ID] = task
			delete(q.active, task.ID)
			cancel()
			close(job.done)
			continue
		}
		return task, job, ctx, true
	}
	return DownloadItem{}, nil, nil, false
}

func (q *downloadQueue) worker() {
	defer q.wg.Done()
	for {
		task, job, ctx, ok := q.next()
		if !ok {
			select {
			case <-q.ctx.Done():
				return
			case <-q.wake:
				continue
			}
		}
		q.run(ctx, task)
		job.cancel()
		q.mu.Lock()
		delete(q.active, task.ID)
		close(job.done)
		q.mu.Unlock()
		q.notify()
	}
}

func (q *downloadQueue) run(ctx context.Context, task DownloadItem) {
	address, err := q.resolve(ctx, task)
	var bytes, total int64
	started := time.Now()
	if err == nil {
		bytes, total, err = downloadDirect(ctx, q.client, address, task.FilePath+".part", func(done, size, received int64) {
			q.mu.Lock()
			defer q.mu.Unlock()
			current, exists := q.tasks[task.ID]
			if !exists || current.State != "running" {
				return
			}
			current.DoneBytes, current.TotalBytes = done, size
			current.Speed = int64(float64(received) / time.Since(started).Seconds())
			if size > 0 {
				current.Progress = int(done * 100 / size)
				if current.Progress > 99 {
					current.Progress = 99
				}
			}
			q.tasks[task.ID] = current
			if time.Since(q.lastSave) >= time.Second {
				if persistErr := q.persistLocked(); persistErr != nil {
					current.Error = persistErr.Error()
					q.tasks[task.ID] = current
				}
			}
		})
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	current, exists := q.tasks[task.ID]
	if !exists || current.State != "running" {
		return
	}
	current.Speed = 0
	if ctx.Err() != nil {
		current.State = "paused"
	} else {
		if err == nil {
			err = os.Rename(task.FilePath+".part", task.FilePath)
			if err == nil {
				_ = os.Remove(downloadResumePath(task.FilePath + ".part"))
			}
		}
		if err != nil {
			current.State, current.Error = "failed", err.Error()
		} else {
			current.State, current.Error, current.Progress = "done", "", 100
			current.DoneBytes, current.TotalBytes = bytes, total
		}
	}
	q.tasks[task.ID] = current
	if err := q.persistLocked(); err != nil {
		current.State, current.Error = "failed", err.Error()
		q.tasks[task.ID] = current
	}
}

func (q *downloadQueue) close() {
	q.cancel()
	q.mu.Lock()
	for id, task := range q.tasks {
		if task.State == "running" || task.State == "pending" {
			task.State, task.Speed = "paused", 0
			q.tasks[id] = task
		}
	}
	if err := q.persistLocked(); err != nil {
		dbg("关闭下载队列: %v", err)
	}
	q.mu.Unlock()
	q.wg.Wait()
	q.client.CloseIdleConnections()
}

type downloadValidator struct {
	Source   string `json:"source"`
	Resource string `json:"resource"`
	ETag     string `json:"etag"`
}

func downloadResumePath(partialPath string) string {
	return partialPath + ".resume.json"
}

func downloadURLHash(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

func responseDownloadValidator(source string, res *http.Response) downloadValidator {
	if res.Request == nil || res.Request.URL == nil {
		return downloadValidator{}
	}
	return downloadValidator{Source: source, Resource: downloadURLHash(res.Request.URL.String()), ETag: res.Header.Get("ETag")}
}

func (v downloadValidator) valid() bool {
	// Weak tags and dates cannot prove that the byte representation matches.
	return v.Resource != "" && len(v.ETag) >= 2 && v.ETag[0] == '"' && v.ETag[len(v.ETag)-1] == '"' &&
		!strings.Contains(v.ETag[1:len(v.ETag)-1], "\"")
}

// downloadDirect resumes only when a persisted validator identifies the exact
// resource and the server confirms it. Other partial files restart at byte zero.
func downloadDirect(ctx context.Context, client *http.Client, address, path string, progress func(int64, int64, int64)) (done, total int64, err error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return 0, 0, errors.New("下载地址不是有效的 HTTP 视频地址")
	}
	if strings.EqualFold(filepath.Ext(u.Path), ".m3u8") {
		return 0, 0, errors.New("暂不支持 HLS 分片下载")
	}
	source := downloadURLHash(u.String())
	resumePath := downloadResumePath(path)
	var offset int64
	var previous downloadValidator
	if info, e := os.Stat(path); e == nil {
		if data, e := os.ReadFile(resumePath); e == nil && json.Unmarshal(data, &previous) == nil &&
			previous.Source == source && previous.valid() {
			offset = info.Size()
		}
	} else if !os.IsNotExist(e) {
		return 0, 0, e
	}
	fetch := func(offset int64) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Referer", "https://www.iesdouyin.com/")
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			req.Header.Set("If-Range", previous.ETag)
		}
		return client.Do(req)
	}
	res, err := fetch(offset)
	if err != nil {
		return offset, 0, err
	}
	validator := responseDownloadValidator(source, res)
	if offset > 0 && (res.StatusCode == http.StatusRequestedRangeNotSatisfiable ||
		res.StatusCode == http.StatusPartialContent && validator != previous) {
		res.Body.Close()
		offset = 0
		res, err = fetch(0)
		if err != nil {
			return 0, 0, err
		}
		validator = responseDownloadValidator(source, res)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		return offset, 0, fmt.Errorf("下载服务器返回 HTTP %d", res.StatusCode)
	}
	mediaType := strings.ToLower(res.Header.Get("Content-Type"))
	encoding := res.Header.Get("Content-Encoding")
	if encoding != "" && !strings.EqualFold(encoding, "identity") ||
		strings.Contains(mediaType, "mpegurl") || strings.Contains(mediaType, "text/") || strings.Contains(mediaType, "json") {
		return offset, 0, errors.New("服务器没有返回可下载的视频文件")
	}
	if res.StatusCode == http.StatusPartialContent {
		start, end, size, e := parseDownloadRange(res.Header.Get("Content-Range"))
		if e != nil || offset == 0 || start != offset || end != size-1 ||
			res.ContentLength >= 0 && res.ContentLength != size-offset {
			return offset, 0, errors.New("服务器返回了不匹配的续传范围")
		}
		total = size
	} else {
		offset = 0
		total = res.ContentLength
	}
	if total < 0 {
		total = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return offset, total, err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
		// Invalidate the old marker before replacing any bytes.
		if err := os.Remove(resumePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, total, err
		}
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return offset, total, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	if offset == 0 && validator.valid() {
		data, err := json.Marshal(validator)
		if err != nil {
			return 0, total, err
		}
		if err := writeFileAtomically(resumePath, data); err != nil {
			return 0, total, err
		}
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return offset, total, err
	}
	done = offset
	buf := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			return done, total, err
		}
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			written, writeErr := f.Write(buf[:n])
			done += int64(written)
			if writeErr != nil {
				return done, total, writeErr
			}
			if written != n {
				return done, total, io.ErrShortWrite
			}
			if progress != nil {
				progress(done, total, done-offset)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return done, total, readErr
			}
			break
		}
	}
	if total > 0 && done != total {
		return done, total, fmt.Errorf("视频下载不完整: %d / %d 字节", done, total)
	}
	if done == 0 {
		return 0, total, errors.New("服务器返回了空视频")
	}
	if total == 0 {
		total = done
	}
	err = f.Sync()
	return done, total, err
}

func parseDownloadRange(header string) (start, end, total int64, err error) {
	span, size, ok := strings.Cut(strings.TrimPrefix(header, "bytes "), "/")
	if !ok || !strings.HasPrefix(header, "bytes ") {
		return 0, 0, 0, errors.New("缺少 Content-Range")
	}
	first, last, ok := strings.Cut(span, "-")
	if !ok {
		return 0, 0, 0, errors.New("无效 Content-Range")
	}
	start, err = strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, 0, errors.New("无效续传起点")
	}
	end, err = strconv.ParseInt(last, 10, 64)
	if err != nil || end < start {
		return 0, 0, 0, errors.New("无效续传终点")
	}
	total, err = strconv.ParseInt(size, 10, 64)
	if err != nil || total <= end {
		return 0, 0, 0, errors.New("无效视频总长度")
	}
	return start, end, total, nil
}
