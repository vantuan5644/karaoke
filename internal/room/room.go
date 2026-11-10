package room

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Song is what a search result describes and what a queue entry plays.
type Song struct {
	Source     string  `json:"source"` // "youtube", "library" or "lyrics"
	Title      string  `json:"title"`
	Artist     string  `json:"artist,omitempty"` // the channel, for YouTube
	Duration   float64 `json:"duration,omitempty"`
	Thumb      string  `json:"thumb,omitempty"`
	Views      string  `json:"views,omitempty"`
	VideoID    string  `json:"videoId,omitempty"`    // the YouTube song, or a lyrics song's backing track
	Embeddable bool    `json:"embeddable,omitempty"` // VideoID plays in YouTube's IFrame player
	LibraryID  string  `json:"libraryId,omitempty"`
	Format     string  `json:"format,omitempty"`   // library: "video" or "cdg"
	LyricsID   int     `json:"lyricsId,omitempty"` // LRCLIB record
}

// Entry is one song in the queue, with whoever is singing it.
type Entry struct {
	QID      string    `json:"qid"`
	Song     Song      `json:"song"`
	Singer   string    `json:"singer,omitempty"`
	AddedAt  time.Time `json:"addedAt"`
	PlayedAt time.Time `json:"playedAt,omitempty"`
}

// Settings are the room's, shared by every phone and the TV.
type Settings struct {
	AlwaysDownload bool `json:"alwaysDownload"` // play YouTube from a download even when it embeds (no ads)
	UpNextSeconds  int  `json:"upNextSeconds"`  // how long the TV shows "up next" before a song
}

const (
	historyKept     = 100
	defaultUpNext   = 6
	maxUpNextSecond = 30
)

var (
	ErrNotFound = errors.New("no such entry")
	ErrBadMove  = errors.New("position out of range")
)

// Room is the one karaoke room: the song playing now, the songs after it, and what played
// before. Every change is written to disk and announced through onChange.
type Room struct {
	mu       sync.Mutex
	path     string
	current  *Entry
	queue    []Entry
	history  []Entry
	settings Settings
	version  int64
	onChange func()
	now      func() time.Time
}

type roomFile struct {
	Current  *Entry   `json:"current"`
	Queue    []Entry  `json:"queue"`
	History  []Entry  `json:"history"`
	Settings Settings `json:"settings"`
}

// State is a copy of the room at one version; callers may keep it.
type State struct {
	Version  int64
	Current  *Entry
	Queue    []Entry
	History  []Entry
	Settings Settings
}

// OpenRoom loads the room saved at path, or starts an empty one if there is none yet.
func OpenRoom(path string) (*Room, error) {
	r := &Room{path: path, settings: Settings{UpNextSeconds: defaultUpNext}, now: time.Now}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var saved roomFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	r.current, r.queue, r.history = saved.Current, saved.Queue, saved.History
	r.settings = normalizeSettings(saved.Settings)
	if r.current == nil && len(r.queue) > 0 {
		r.promote()
	}
	return r, nil
}

// OnChange sets what runs after every change, outside the room's lock.
func (r *Room) OnChange(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = f
}

// State returns a copy of the room.
func (r *Room) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{
		Version:  r.version,
		Current:  copyEntry(r.current),
		Queue:    append([]Entry(nil), r.queue...),
		History:  append([]Entry(nil), r.history...),
		Settings: r.settings,
	}
}

// Add queues a song, at the front when next is set. With nothing playing it plays at once.
func (r *Room) Add(song Song, singer string, next bool) (Entry, error) {
	entry := Entry{QID: newID(), Song: song, Singer: singer}
	err := r.change(func() error {
		entry.AddedAt = r.now()
		if next {
			r.queue = append([]Entry{entry}, r.queue...)
		} else {
			r.queue = append(append([]Entry(nil), r.queue...), entry)
		}
		if r.current == nil {
			r.promote()
		}
		return nil
	})
	return entry, err
}

// Remove takes an upcoming entry out of the queue. Removing the one playing skips it.
func (r *Room) Remove(qid string) error {
	return r.change(func() error {
		if r.current != nil && r.current.QID == qid {
			r.finishCurrent()
			return nil
		}
		i := r.indexOf(qid)
		if i < 0 {
			return ErrNotFound
		}
		r.queue = append(append([]Entry(nil), r.queue[:i]...), r.queue[i+1:]...)
		return nil
	})
}

// Move puts an upcoming entry at position to (0 plays next).
func (r *Room) Move(qid string, to int) error {
	return r.change(func() error {
		i := r.indexOf(qid)
		if i < 0 {
			return ErrNotFound
		}
		if to < 0 || to >= len(r.queue) {
			return ErrBadMove
		}
		moved := r.queue[i]
		rest := append(append([]Entry(nil), r.queue[:i]...), r.queue[i+1:]...)
		r.queue = append(append(append([]Entry(nil), rest[:to]...), moved), rest[to:]...)
		return nil
	})
}

// Advance finishes the entry playing and starts the next, but only if the entry playing is
// still from: two TVs, or a TV and a phone's skip, then move on once between them.
func (r *Room) Advance(from string) (bool, error) {
	advanced := false
	err := r.change(func() error {
		if r.current == nil || r.current.QID != from {
			return errStale
		}
		r.finishCurrent()
		advanced = true
		return nil
	})
	if errors.Is(err, errStale) {
		return false, nil
	}
	return advanced, err
}

// Clear empties the upcoming queue; the song playing keeps playing.
func (r *Room) Clear() error {
	return r.change(func() error {
		if len(r.queue) == 0 {
			return errStale
		}
		r.queue = nil
		return nil
	})
}

// SetSettings replaces the room's settings.
func (r *Room) SetSettings(s Settings) error {
	return r.change(func() error {
		r.settings = normalizeSettings(s)
		return nil
	})
}

// MarkNotEmbeddable records that videoID refused to play embedded, on every entry using it.
func (r *Room) MarkNotEmbeddable(videoID string) error {
	return r.change(func() error {
		changed := false
		fix := func(e Entry) Entry {
			if e.Song.VideoID == videoID && e.Song.Embeddable {
				e.Song.Embeddable = false
				changed = true
			}
			return e
		}
		if r.current != nil {
			c := fix(*r.current)
			r.current = &c
		}
		queue := make([]Entry, len(r.queue))
		for i, e := range r.queue {
			queue[i] = fix(e)
		}
		r.queue = queue
		if !changed {
			return errStale
		}
		return nil
	})
}

// errStale marks a change that turned out to change nothing; change() then skips the save.
var errStale = errors.New("nothing changed")

func (r *Room) change(apply func() error) error {
	r.mu.Lock()
	if err := apply(); err != nil {
		r.mu.Unlock()
		if errors.Is(err, errStale) {
			return nil
		}
		return err
	}
	r.version++
	err := r.save()
	notify := r.onChange
	r.mu.Unlock()
	if notify != nil {
		notify()
	}
	return err
}

func (r *Room) finishCurrent() {
	if r.current != nil {
		r.history = append([]Entry{*r.current}, r.history...)
		if len(r.history) > historyKept {
			r.history = r.history[:historyKept]
		}
		r.current = nil
	}
	r.promote()
}

func (r *Room) promote() {
	if len(r.queue) == 0 {
		return
	}
	next := r.queue[0]
	next.PlayedAt = r.now()
	r.current = &next
	r.queue = append([]Entry(nil), r.queue[1:]...)
}

func (r *Room) indexOf(qid string) int {
	for i, e := range r.queue {
		if e.QID == qid {
			return i
		}
	}
	return -1
}

func (r *Room) save() error {
	if r.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(roomFile{Current: r.current, Queue: r.queue, History: r.history, Settings: r.settings}, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(r.path, data, 0o600)
}

func normalizeSettings(s Settings) Settings {
	if s.UpNextSeconds <= 0 {
		s.UpNextSeconds = defaultUpNext
	}
	if s.UpNextSeconds > maxUpNextSecond {
		s.UpNextSeconds = maxUpNextSecond
	}
	return s
}

func copyEntry(e *Entry) *Entry {
	if e == nil {
		return nil
	}
	c := *e
	return &c
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// writeFileAtomic writes through a temporary file and a rename, so a crash never leaves
// half a file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
