package musickey

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// listen is how much of a song is heard: enough for its key, without decoding a long file.
const listen = 6 * time.Minute

// ErrNoMusic is a file with no tonal sound in it (silence, or speech).
var ErrNoMusic = errors.New("no music to hear a key in")

// Finder works out the keys of song files with ffmpeg, remembering each file's.
type Finder struct {
	FFmpeg string

	mu      sync.Mutex
	known   map[string]Key
	working map[string]chan struct{} // files being heard now, closed when done
	busy    chan struct{}            // one file at a time: it takes a core for a second or two
}

// Key returns a file's key, hearing it the first time.
func (f *Finder) Key(ctx context.Context, path string) (Key, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Key{}, err
	}
	// A file is known by its path and size, not its modification time: the download cache
	// touches a file each time it is used, to keep the songs in use.
	id := path + "|" + strconv.FormatInt(info.Size(), 10)
	for {
		f.mu.Lock()
		if k, ok := f.known[id]; ok {
			f.mu.Unlock()
			return k, nil
		}
		wait, running := f.working[id]
		if !running {
			if f.working == nil {
				f.working, f.known, f.busy = map[string]chan struct{}{}, map[string]Key{}, make(chan struct{}, 1)
			}
			done := make(chan struct{})
			f.working[id] = done
			f.mu.Unlock()
			k, err := f.hear(ctx, path)
			f.mu.Lock()
			if err == nil {
				f.known[id] = k
			}
			delete(f.working, id)
			close(done)
			f.mu.Unlock()
			return k, err
		}
		f.mu.Unlock()
		select {
		case <-wait: // the other caller finished: take its answer (or try again if it failed)
		case <-ctx.Done():
			return Key{}, ctx.Err()
		}
	}
}

// hear runs ffmpeg on a file, once the files before it are done; an error is ctx's when
// the caller gave up waiting.
func (f *Finder) hear(ctx context.Context, path string) (Key, error) {
	select {
	case f.busy <- struct{}{}:
		defer func() { <-f.busy }()
	case <-ctx.Done():
		return Key{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.FFmpeg, "-nostdin", "-v", "error", "-i", path,
		"-t", strconv.Itoa(int(listen.Seconds())), "-vn", "-ac", "1", "-ar", strconv.Itoa(Rate), "-f", "f32le", "pipe:1")
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return Key{}, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return Key{}, fmt.Errorf("ffmpeg: %s", lastLine(exit.Stderr))
		}
		return Key{}, fmt.Errorf("ffmpeg: %w", err)
	}
	x := make([]float32, len(out)/4)
	for i := range x {
		x[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:]))
	}
	k, ok := Analyze(x)
	if !ok {
		return Key{}, ErrNoMusic
	}
	return k, nil
}

func lastLine(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return s[i+1:]
		}
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
