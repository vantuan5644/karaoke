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
	busy    sync.Mutex               // one file at a time: it takes a core for a second or two
}

// Key returns a file's key, hearing it the first time.
func (f *Finder) Key(ctx context.Context, path string) (Key, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Key{}, err
	}
	id := path + "|" + strconv.FormatInt(info.Size(), 10) + "|" + info.ModTime().UTC().Format(time.RFC3339Nano)
	for {
		f.mu.Lock()
		if k, ok := f.known[id]; ok {
			f.mu.Unlock()
			return k, nil
		}
		wait, running := f.working[id]
		if !running {
			if f.working == nil {
				f.working, f.known = map[string]chan struct{}{}, map[string]Key{}
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

func (f *Finder) hear(ctx context.Context, path string) (Key, error) {
	f.busy.Lock()
	defer f.busy.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.FFmpeg, "-nostdin", "-v", "error", "-i", path,
		"-t", strconv.Itoa(int(listen.Seconds())), "-vn", "-ac", "1", "-ar", strconv.Itoa(Rate), "-f", "f32le", "pipe:1")
	out, err := cmd.Output()
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
