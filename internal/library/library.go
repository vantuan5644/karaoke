// Package library finds karaoke files in folders on this computer and searches them by name:
// videos, MP3+G (an audio file beside a .cdg graphics file of the same name), and plain
// audio backing tracks.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Item is one song in the library.
type Item struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist,omitempty"`
	Format   string  `json:"format"`             // "video", "cdg" or "audio"
	Where    string  `json:"where"`              // its folder, under the library's, to tell versions apart
	Duration float64 `json:"duration,omitempty"` // seconds, once something has measured it

	Media string `json:"-"` // the video or audio file
	CDG   string `json:"-"` // the graphics, for "cdg"
	words []string
	name  string // folded artist and title, for ranking
}

// What each format is made of. Browsers play these in a <video> element; .mkv and .mov
// play when what is inside them is H.264, VP9 or AV1, as it usually is.
var (
	videoExts = []string{".mp4", ".m4v", ".webm", ".mkv", ".mov"}
	audioExts = []string{".mp3", ".m4a", ".ogg", ".opus", ".flac", ".wav", ".aac"}
)

// Library is a set of folders, scanned now and then. Its methods are safe to call from any
// goroutine.
type Library struct {
	FFprobe string // ffprobe's path, to measure songs; empty without it

	roots []string

	scanning sync.Mutex // one scan at a time
	mu       sync.RWMutex
	items    map[string]Item
	sorted   []Item // by artist, then title
	scanned  time.Time
}

// New returns a library of these folders, without scanning them yet.
func New(roots []string) (*Library, error) {
	if len(roots) == 0 {
		return nil, errors.New("library: no folders")
	}
	abs := make([]string, 0, len(roots))
	for _, r := range roots {
		a, err := filepath.Abs(r)
		if err != nil {
			return nil, fmt.Errorf("library: %w", err)
		}
		info, err := os.Stat(a)
		if err != nil {
			return nil, fmt.Errorf("library: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("library: %s is not a folder", a)
		}
		abs = append(abs, a)
	}
	return &Library{roots: abs, items: map[string]Item{}}, nil
}

// Run scans the folders again every interval, until ctx ends, so songs added to them show
// up without a restart. Scan once first: Run waits before its first scan.
func (l *Library) Run(ctx context.Context, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		start := time.Now()
		n, err := l.Scan()
		if err != nil {
			log.Printf("library: %v", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			log.Printf("library: scanning %d songs took %s", n, d.Round(time.Millisecond))
		}
	}
}

// Scan reads the folders again and returns how many songs they hold. A folder that can't
// be read is reported, and the songs found in the others are kept.
func (l *Library) Scan() (int, error) {
	l.scanning.Lock()
	defer l.scanning.Unlock()
	items := map[string]Item{}
	var errs []error
	for _, root := range l.roots {
		if err := scanRoot(root, items); err != nil {
			errs = append(errs, err)
		}
	}
	l.mu.RLock()
	for id, old := range l.items {
		if it, ok := items[id]; ok && it.Duration == 0 {
			it.Duration = old.Duration
			items[id] = it
		}
	}
	l.mu.RUnlock()
	sorted := make([]Item, 0, len(items))
	for _, it := range items {
		sorted = append(sorted, it)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].name != sorted[j].name {
			return sorted[i].name < sorted[j].name
		}
		return sorted[i].ID < sorted[j].ID
	})
	l.mu.Lock()
	l.items, l.sorted, l.scanned = items, sorted, time.Now()
	l.mu.Unlock()
	return len(items), errors.Join(errs...)
}

// scanRoot adds the songs under one folder to items.
func scanRoot(root string, items map[string]Item) error {
	// Group files by folder and name without extension: an MP3+G is two files.
	type group map[string]string // extension -> path
	groups := map[string]group{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			log.Printf("library: skipping %s: %v", path, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// Hidden folders, and the thumbnail folders NAS boxes add.
			if path != root && (strings.HasPrefix(name, ".") || name == "@eaDir" || name == "#recycle") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !known(ext) {
			return nil
		}
		key := filepath.Join(filepath.Dir(path), strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
		if groups[key] == nil {
			groups[key] = group{}
		}
		groups[key][ext] = path
		return nil
	})
	for _, g := range groups {
		it, ok := itemFor(root, g)
		if ok {
			items[it.ID] = it
		}
	}
	if err != nil {
		return fmt.Errorf("scanning %s: %w", root, err)
	}
	return nil
}

func known(ext string) bool {
	if ext == ".cdg" {
		return true
	}
	for _, e := range append(videoExts, audioExts...) {
		if ext == e {
			return true
		}
	}
	return false
}

// itemFor makes a song from the files that share one name: graphics with audio is an MP3+G,
// else a video, else plain audio. Graphics with no audio are not a song.
func itemFor(root string, files map[string]string) (Item, bool) {
	first := func(exts []string) string {
		for _, e := range exts {
			if p, ok := files[e]; ok {
				return p
			}
		}
		return ""
	}
	it := Item{}
	audio, video := first(audioExts), first(videoExts)
	switch {
	case files[".cdg"] != "" && audio != "":
		it.Format, it.Media, it.CDG = "cdg", audio, files[".cdg"]
	case video != "":
		it.Format, it.Media = "video", video
	case audio != "":
		it.Format, it.Media = "audio", audio
	default:
		return Item{}, false
	}
	rel, err := filepath.Rel(root, it.Media)
	if err != nil {
		return Item{}, false
	}
	rel = filepath.ToSlash(rel)
	sum := sha256.Sum256([]byte(root + "\x00" + rel))
	it.ID = hex.EncodeToString(sum[:8])
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	it.Artist, it.Title = parseName(stem)
	if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
		it.Where = dir
	}
	it.words = words(it.Artist + " " + it.Title + " " + it.Where)
	it.name = strings.Join(words(it.Artist+" "+it.Title), " ")
	return it, true
}

// Get returns the song with that ID.
func (l *Library) Get(id string) (Item, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	it, ok := l.items[id]
	return it, ok
}

// SetDuration records a song's length once something has measured it.
func (l *Library) SetDuration(id string, seconds float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if it, ok := l.items[id]; ok {
		it.Duration = seconds
		l.items[id] = it
	}
}

// Len is how many songs the last scan found.
func (l *Library) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.items)
}

// Roots are the library's folders.
func (l *Library) Roots() []string { return append([]string(nil), l.roots...) }

// Measure returns a song's length, asking ffprobe the first time when it is installed (it
// comes with ffmpeg). It returns 0 when the length can't be had; nothing depends on it but
// the queue's times and how well lyrics match.
func (l *Library) Measure(ctx context.Context, id string) float64 {
	it, ok := l.Get(id)
	if !ok || it.Duration > 0 || l.FFprobe == "" {
		return it.Duration
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, l.FFprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", it.Media).Output()
	if err != nil {
		log.Printf("library: measuring %s: %v", it.Media, err)
		return 0
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || d <= 0 || d > 36000 {
		return 0
	}
	l.SetDuration(id, d)
	return d
}
