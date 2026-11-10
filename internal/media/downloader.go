// Package media fetches YouTube videos with yt-dlp into a cache folder, for videos that
// refuse to play embedded, and for every video when the room asks for downloads.
package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status is where one video's download stands.
type Status struct {
	State    string  `json:"state"`              // "", "queued", "downloading", "ready" or "failed"
	Progress float64 `json:"progress,omitempty"` // 0 to 1 while downloading
	Error    string  `json:"error,omitempty"`
}

// Config sets up a Downloader.
type Config struct {
	Bin       string        // yt-dlp
	Args      []string      // added to every run, e.g. --js-runtimes node
	Dir       string        // the cache folder
	MaxBytes  int64         // the cache is trimmed to this after every download; 0 keeps everything
	MaxHeight int           // the tallest video fetched; 720 when zero
	Parallel  int           // downloads at once; 2 when zero
	Timeout   time.Duration // a download that takes longer is stopped; 15 minutes when zero
	OnChange  func()        // runs after a status changes, at most every progressEvery for progress
	Logf      func(format string, args ...any)
	Attempts  int           // tries per video before it counts as failed; 3 when zero
	Backoff   time.Duration // the pause before the second try, doubled for each one after; 2s when zero
}

const (
	progressEvery = 700 * time.Millisecond
	retryAfter    = 2 * time.Minute
	tmpDir        = ".partial"
)

var (
	// The extensions a finished download can have; --remux-video makes it mp4 nearly always.
	mediaExts       = []string{".mp4", ".webm", ".mkv"}
	progressPattern = regexp.MustCompile(`KDL\s+([0-9.]+)%`)
)

// Downloader runs yt-dlp. Its methods are safe to call from any goroutine.
type Downloader struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	files    map[string]string // id: finished file
	active   map[string]*job   // queued or running
	pending  []string          // queued ids, in the order wanted
	failed   map[string]failure
	wanted   map[string]bool
	running  int
	notified time.Time
	wg       sync.WaitGroup
}

type job struct {
	running  bool
	progress float64
}

type failure struct {
	err string
	at  time.Time
}

// New prepares the cache folder, picks up the files already in it, and clears what an
// interrupted download left behind.
func New(cfg Config) (*Downloader, error) {
	if cfg.MaxHeight <= 0 {
		cfg.MaxHeight = 720
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Minute
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 2 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(cfg.Dir, tmpDir)); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Downloader{
		cfg: cfg, ctx: ctx, cancel: cancel,
		files: map[string]string{}, active: map[string]*job{},
		failed: map[string]failure{}, wanted: map[string]bool{},
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.Type().IsRegular() && isMediaExt(ext) {
			d.files[strings.TrimSuffix(e.Name(), ext)] = filepath.Join(cfg.Dir, e.Name())
		}
	}
	return d, nil
}

// Close stops the downloads running and waits for them to end.
func (d *Downloader) Close() {
	d.cancel()
	d.wg.Wait()
}

// Status reports where id's download stands.
func (d *Downloader) Status(id string) Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.files[id]; ok {
		return Status{State: "ready"}
	}
	if j, ok := d.active[id]; ok {
		if j.running {
			return Status{State: "downloading", Progress: j.progress}
		}
		return Status{State: "queued"}
	}
	if f, ok := d.failed[id]; ok {
		return Status{State: "failed", Error: f.err}
	}
	return Status{}
}

// Path returns id's finished file and marks it recently used, so the cache keeps it longest.
func (d *Downloader) Path(id string) (string, bool) {
	d.mu.Lock()
	path, ok := d.files[id]
	d.mu.Unlock()
	if ok {
		now := time.Now()
		os.Chtimes(path, now, now)
	}
	return path, ok
}

// Want makes ids, in that order, the videos to have on disk: missing ones are queued (failed
// ones again after a while), queued ones no longer wanted are dropped, and the cache never
// evicts a wanted file.
func (d *Downloader) Want(ids []string) {
	d.mu.Lock()
	wanted := make(map[string]bool, len(ids))
	var pending []string
	changed := false
	for _, id := range ids {
		if wanted[id] {
			continue
		}
		wanted[id] = true
		if _, ok := d.files[id]; ok {
			continue
		}
		if f, ok := d.failed[id]; ok && time.Since(f.at) < retryAfter {
			continue
		}
		if j, ok := d.active[id]; ok && j.running {
			continue
		}
		if _, ok := d.active[id]; !ok {
			d.active[id] = &job{}
			changed = true
		}
		pending = append(pending, id)
	}
	for _, id := range d.pending {
		if !wanted[id] {
			delete(d.active, id)
			changed = true
		}
	}
	d.pending = pending
	d.wanted = wanted
	d.startLocked()
	d.mu.Unlock()
	// Only a change notifies: the caller usually calls Want from its own change handler.
	if changed {
		d.notify(true)
	}
}

func (d *Downloader) startLocked() {
	for d.running < d.cfg.Parallel && len(d.pending) > 0 {
		id := d.pending[0]
		d.pending = d.pending[1:]
		d.active[id].running = true
		delete(d.failed, id)
		d.running++
		d.wg.Add(1)
		go d.run(id)
	}
}

func (d *Downloader) run(id string) {
	defer d.wg.Done()
	path, err := d.fetchWithRetries(id)
	d.mu.Lock()
	delete(d.active, id)
	d.running--
	if err != nil {
		d.failed[id] = failure{err: err.Error(), at: time.Now()}
	} else {
		d.files[id] = path
		d.trimLocked()
	}
	d.startLocked()
	d.mu.Unlock()
	d.notify(true)
}

// fetchWithRetries tries again after a failure: YouTube's video servers now and then answer
// one request with 403 and the next one with the video.
func (d *Downloader) fetchWithRetries(id string) (string, error) {
	pause := d.cfg.Backoff
	var err error
	for attempt := 1; ; attempt++ {
		var path string
		path, err = d.fetch(id)
		if err == nil {
			return path, nil
		}
		d.cfg.Logf("download %s, try %d of %d: %v", id, attempt, d.cfg.Attempts, err)
		if attempt == d.cfg.Attempts || d.ctx.Err() != nil {
			return "", err
		}
		select {
		case <-time.After(pause):
		case <-d.ctx.Done():
			return "", err
		}
		pause *= 2
	}
}

func (d *Downloader) fetch(id string) (string, error) {
	work := filepath.Join(d.cfg.Dir, tmpDir, id)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	ctx, cancel := context.WithTimeout(d.ctx, d.cfg.Timeout)
	defer cancel()

	h := strconv.Itoa(d.cfg.MaxHeight)
	format := "bv*[height<=" + h + "][vcodec^=avc1]+ba[acodec^=mp4a]/b[height<=" + h + "][ext=mp4]/bv*[height<=" + h + "]+ba/b"
	args := []string{
		"--no-playlist", "--newline", "--no-warnings", "--no-mtime",
		"-f", format, "--merge-output-format", "mp4", "--remux-video", "mp4",
		"--progress-template", "download:KDL %(progress._percent_str)s",
		"-o", filepath.Join(work, "%(id)s.%(ext)s"),
	}
	args = append(append(args, d.cfg.Args...), "--", id)
	cmd := exec.CommandContext(ctx, d.cfg.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("yt-dlp: %w", err)
	}
	d.readProgress(id, stdout)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("download stopped: " + ctx.Err().Error())
		}
		return "", errors.New(stderr.lastError(err))
	}
	return d.keep(id, work)
}

// readProgress turns yt-dlp's per-stream percentages into one figure: the video stream is
// most of the bytes, the audio stream that follows it the rest.
func (d *Downloader) readProgress(id string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	stream, last := 0, 0.0
	for scanner.Scan() {
		m := progressPattern.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		p, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		if p+20 < last {
			stream++
		}
		last = p
		overall := p / 100 * 0.9
		if stream > 0 {
			overall = 0.9 + p/100*0.1
		}
		d.mu.Lock()
		if j, ok := d.active[id]; ok {
			j.progress = overall
		}
		d.mu.Unlock()
		d.notify(false)
	}
}

func (d *Downloader) keep(id, work string) (string, error) {
	for _, ext := range mediaExts {
		src := filepath.Join(work, id+ext)
		if _, err := os.Stat(src); err == nil {
			dst := filepath.Join(d.cfg.Dir, id+ext)
			if err := os.Rename(src, dst); err != nil {
				return "", err
			}
			return dst, nil
		}
	}
	return "", errors.New("yt-dlp finished without a video file")
}

// trimLocked deletes the least recently used files until the cache fits, never a wanted one.
func (d *Downloader) trimLocked() {
	if d.cfg.MaxBytes <= 0 {
		return
	}
	type cached struct {
		id   string
		size int64
		used time.Time
	}
	var all []cached
	var total int64
	for id, path := range d.files {
		info, err := os.Stat(path)
		if err != nil {
			delete(d.files, id)
			continue
		}
		all = append(all, cached{id, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].used.Before(all[j].used) })
	for _, c := range all {
		if total <= d.cfg.MaxBytes {
			return
		}
		if d.wanted[c.id] {
			continue
		}
		if err := os.Remove(d.files[c.id]); err == nil {
			delete(d.files, c.id)
			total -= c.size
		}
	}
}

func (d *Downloader) notify(always bool) {
	if d.cfg.OnChange == nil {
		return
	}
	d.mu.Lock()
	if !always && time.Since(d.notified) < progressEvery {
		d.mu.Unlock()
		return
	}
	d.notified = time.Now()
	d.mu.Unlock()
	d.cfg.OnChange()
}

func isMediaExt(ext string) bool {
	for _, e := range mediaExts {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

// tailBuffer keeps the end of yt-dlp's stderr, where its ERROR line is.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8192 {
		t.buf = t.buf[len(t.buf)-8192:]
	}
	return len(p), nil
}

func (t *tailBuffer) lastError(exit error) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "ERROR:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
		}
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		return "yt-dlp: " + lines[len(lines)-1]
	}
	return "yt-dlp: " + exit.Error()
}
