package media

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const diskMediaBytes = 256 << 20
const diskMediaTTL = time.Hour

type diskRangeInfo struct {
	Key    string      `json:"key"`
	Start  int64       `json:"start"`
	Total  int64       `json:"total"`
	Size   int         `json:"size"`
	Hash   string      `json:"sha256"`
	Header http.Header `json:"header"`
	file   string
	used   time.Time
}
type diskRangeWrite struct {
	entry      *cachedMediaRange
	generation uint64
}
type diskRangeCache struct {
	mu         sync.Mutex
	dir        string
	records    map[string]diskRangeInfo
	bytes      int64
	queue      chan diskRangeWrite
	done       chan struct{}
	generation uint64
	closed     bool
}

func mediaDiskKey(url, referer string) string {
	hash := sha256.Sum256([]byte(url + "\x00" + referer))
	return hex.EncodeToString(hash[:])
}

func newDiskRangeCache(dir string) *diskRangeCache {
	d := &diskRangeCache{dir: dir, records: map[string]diskRangeInfo{}, queue: make(chan diskRangeWrite, 4), done: make(chan struct{})}
	if os.MkdirAll(dir, 0700) != nil {
		return nil
	}
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".chunk") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		line, readErr := bufio.NewReaderSize(f, 8192).ReadSlice('\n')
		var info diskRangeInfo
		err = json.Unmarshal(line, &info)
		stat, statErr := f.Stat()
		f.Close()
		if readErr != nil || err != nil || statErr != nil || !validDiskInfo(info) || stat.Size() != int64(len(line)+info.Size) || time.Since(stat.ModTime()) > diskMediaTTL {
			os.Remove(path)
			continue
		}
		if file.Name() != info.Key+"-"+strconv.FormatInt(info.Start, 10)+".chunk" {
			continue
		}
		info.file = path
		info.used = stat.ModTime()
		d.records[path] = info
		d.bytes += int64(info.Size)
	}
	d.pruneLocked()
	go func() {
		defer close(d.done)
		for work := range d.queue {
			d.store(work)
		}
	}()
	return d
}
func validDiskInfo(info diskRangeInfo) bool {
	key, err := hex.DecodeString(info.Key)
	hash, hashErr := hex.DecodeString(info.Hash)
	return err == nil && hashErr == nil && len(key) == 32 && len(hash) == 32 && info.Start >= 0 && info.Total > info.Start && info.Size > 0 && info.Size <= mediaRangeBytes && int64(info.Size) <= info.Total-info.Start
}
func (d *diskRangeCache) enqueue(e *cachedMediaRange) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	select {
	case d.queue <- diskRangeWrite{e, d.generation}:
	default:
	} // Playback never waits for a slow disk.
}
func (d *diskRangeCache) store(work diskRangeWrite) {
	e := work.entry
	sum := sha256.Sum256(e.data)
	info := diskRangeInfo{Key: mediaDiskKey(e.url, e.referer), Start: e.start, Total: e.total, Size: len(e.data), Hash: hex.EncodeToString(sum[:]), Header: make(http.Header)}
	// Persist no signed URL, account headers or cookies. Only file validators and MIME.
	for _, key := range []string{"ETag", "Last-Modified", "Content-Type", "Accept-Ranges"} {
		if value := e.header.Get(key); value != "" {
			info.Header.Set(key, value)
		}
	}
	raw, _ := json.Marshal(info)
	if len(raw) > 8000 {
		return
	}
	f, err := os.CreateTemp(d.dir, ".chunk-*")
	if err != nil {
		return
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		_, err = f.Write(e.data)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if work.generation != d.generation {
		return
	}
	path := filepath.Join(d.dir, info.Key+"-"+strconv.FormatInt(info.Start, 10)+".chunk")
	// Cached files are disposable; a replacement never touches a media source.
	if old, ok := d.records[path]; ok {
		os.Remove(path)
		d.bytes -= int64(old.Size)
		delete(d.records, path)
	}
	if os.Rename(temp, path) != nil {
		return
	}
	info.file = path
	info.used = time.Now()
	d.records[path] = info
	d.bytes += int64(info.Size)
	d.pruneLocked()
}
func (d *diskRangeCache) pruneLocked() {
	now := time.Now()
	for path, info := range d.records {
		if now.Sub(info.used) > diskMediaTTL {
			os.Remove(path)
			delete(d.records, path)
			d.bytes -= int64(info.Size)
		}
	}
	for len(d.records) > 0 && (d.bytes > diskMediaBytes || len(d.records) > 256) {
		oldest := ""
		var used time.Time
		for path, info := range d.records {
			if oldest == "" || info.used.Before(used) {
				oldest = path
				used = info.used
			}
		}
		info := d.records[oldest]
		os.Remove(oldest)
		delete(d.records, oldest)
		d.bytes -= int64(info.Size)
	}
}
func (d *diskRangeCache) lookup(req *http.Request, prefix bool) *http.Response {
	// The memory layer already rejects unsupported Range/conditional requests.
	parts := strings.SplitN(strings.TrimPrefix(req.Header.Get("Range"), "bytes="), "-", 2)
	if len(parts) != 2 {
		return nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil
	}
	end := int64(-1)
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return nil
		}
	}
	key := mediaDiskKey(req.URL.String(), req.Header.Get("Referer"))
	d.mu.Lock()
	defer d.mu.Unlock()
	for path, info := range d.records {
		if info.Key != key || start < info.Start || start >= info.Start+int64(info.Size) || time.Since(info.used) > diskMediaTTL {
			continue
		}
		stop := end
		if stop < 0 || stop >= info.Total {
			stop = info.Total - 1
		}
		if stop < start || (!prefix && stop >= info.Start+int64(info.Size)) {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		reader := bufio.NewReaderSize(f, 8192)
		_, err = reader.ReadSlice('\n')
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(reader, int64(mediaRangeBytes)+1))
		}
		f.Close()
		hash := sha256.Sum256(data)
		if err != nil || len(data) != info.Size || hex.EncodeToString(hash[:]) != info.Hash {
			os.Remove(path)
			delete(d.records, path)
			d.bytes -= int64(info.Size)
			continue
		}
		info.used = time.Now()
		d.records[path] = info
		// The existing range formatter handles offsets and headers identically.
		c := mediaRangeCache{entries: []*cachedMediaRange{{url: req.URL.String(), referer: req.Header.Get("Referer"), start: info.Start, total: info.Total, header: info.Header, data: data, used: time.Now()}}}
		return c.lookup(req, prefix)
	}
	return nil
}
func (d *diskRangeCache) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	for path := range d.records {
		os.Remove(path)
	}
	d.records = map[string]diskRangeInfo{}
	d.bytes = 0
}
func (d *diskRangeCache) close() {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.queue)
	}
	d.mu.Unlock()
	<-d.done
}
