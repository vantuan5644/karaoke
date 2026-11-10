// Package lyrics finds a song's words on LRCLIB (lrclib.net), a free, open lyrics database
// that needs no key, starting from the title of a karaoke video.
package lyrics

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Track is one song's lyrics.
type Track struct {
	ID           int     `json:"id"`
	Track        string  `json:"track"`
	Artist       string  `json:"artist"`
	Album        string  `json:"album,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
	Instrumental bool    `json:"instrumental,omitempty"`
	Lyrics       string  `json:"lyrics,omitempty"` // plain text, one line per line sung
}

// ErrNotFound is LRCLIB having no such record.
var ErrNotFound = errors.New("no such lyrics")

const (
	cacheFor   = 12 * time.Hour
	cacheLimit = 5000
)

// Client talks to LRCLIB. Its methods are safe to call from any goroutine.
type Client struct {
	HTTP      *http.Client
	Base      string        // https://lrclib.net
	UserAgent string        // LRCLIB asks clients to name themselves
	Gap       time.Duration // the least time between two requests; LRCLIB answers 429 to bursts

	gate sync.Mutex // held while waiting for a turn, so requests go out one Gap apart
	next time.Time

	mu       sync.Mutex
	searches map[string]cached[[]Track]
	tracks   map[int]cached[Track]
	matches  map[string]cached[match]
}

type cached[T any] struct {
	value T
	at    time.Time
}

type match struct {
	track Track
	ok    bool
}

// New returns a Client for lrclib.net.
func New(userAgent string) *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 8 * time.Second}, Base: "https://lrclib.net", UserAgent: userAgent,
		Gap:      250 * time.Millisecond,
		searches: map[string]cached[[]Track]{}, tracks: map[int]cached[Track]{}, matches: map[string]cached[match]{},
	}
}

type record struct {
	ID           int     `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

var timestamp = regexp.MustCompile(`(?m)^(\[\d+:\d+(\.\d+)?\])+\s?`)

func (r record) track() Track {
	text := r.PlainLyrics
	if strings.TrimSpace(text) == "" && r.SyncedLyrics != "" {
		text = timestamp.ReplaceAllString(r.SyncedLyrics, "")
	}
	return Track{
		ID: r.ID, Track: r.TrackName, Artist: r.ArtistName, Album: r.AlbumName,
		Duration: r.Duration, Instrumental: r.Instrumental, Lyrics: strings.TrimSpace(text),
	}
}

// Search returns LRCLIB's records for a free-text query, best first.
func (c *Client) Search(ctx context.Context, query string) ([]Track, error) {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil, nil
	}
	if v, ok := lookup(c, c.searches, query); ok {
		return v, nil
	}
	var records []record
	if err := c.get(ctx, "/api/search?q="+url.QueryEscape(query), &records); err != nil {
		return nil, err
	}
	found := make([]Track, 0, len(records))
	for _, r := range records {
		found = append(found, r.track())
	}
	store(c, c.searches, query, found)
	return found, nil
}

// Get returns one record by its LRCLIB id.
func (c *Client) Get(ctx context.Context, id int) (Track, error) {
	if v, ok := lookup(c, c.tracks, id); ok {
		return v, nil
	}
	var r record
	if err := c.get(ctx, "/api/get/"+strconv.Itoa(id), &r); err != nil {
		return Track{}, err
	}
	t := r.track()
	store(c, c.tracks, id, t)
	return t, nil
}

// Match finds the lyrics a karaoke video's title names. ok is false when nothing fits well
// enough to show; that is not an error.
func (c *Client) Match(ctx context.Context, title string, duration float64) (t Track, ok bool, err error) {
	key := fmt.Sprintf("%s|%.0f", title, duration)
	if v, hit := lookup(c, c.matches, key); hit {
		return v.track, v.ok, nil
	}
	parts := Parts(title)
	if len(parts) == 0 {
		return Track{}, false, nil
	}
	plain, loose := queries(parts)
	found, err := c.look(ctx, title, parts, [][]string{plain, loose}, duration, goodEnough)
	if err != nil {
		return Track{}, false, err
	}
	if len(found) > 0 && found[0].score >= minScore {
		t, ok = found[0].Track, true
	}
	store(c, c.matches, key, match{t, ok})
	return t, ok, nil
}

// Find searches for what someone typed. When LRCLIB has nothing for those words, it loosens
// them the way Match does, so a misspelled word ("Khúc" for "Lúc") or a channel's tag still
// finds the song; then only songs the words name come back, best first.
func (c *Client) Find(ctx context.Context, query string) ([]Track, error) {
	found, err := c.Search(ctx, query)
	if err != nil || len(found) > 0 {
		return found, err
	}
	parts := Parts(query)
	plain, loose := queries(parts)
	typed := strings.Join(strings.Fields(punctuation.ReplaceAllString(Fold(query), " ")), " ")
	// LRCLIB ignores accents and capitals, so the folded words are the search just made.
	plain = slices.DeleteFunc(plain, func(q string) bool { return q == typed })
	scored, err := c.look(ctx, query, parts, [][]string{plain, loose}, 0, minScore)
	if err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.Track)
	}
	return out, nil
}

type scored struct {
	Track
	score float64
}

// look searches each query in turn and keeps every track that names the title's words. It
// stops once the best of them scores enough, or at the end of a round of queries once one
// would do. Best first; a tie goes to the one found first.
func (c *Client) look(ctx context.Context, title string, parts []string, rounds [][]string, duration, enough float64) ([]scored, error) {
	words := tokens(strings.Join(parts, " "))
	accented := hasAccents(title)
	var out []scored
	seen := map[int]bool{}
	best := 0.0
search:
	for _, qs := range rounds {
		for _, q := range qs {
			found, err := c.Search(ctx, q)
			if err != nil {
				return nil, err
			}
			for _, t := range found {
				if seen[t.ID] {
					continue
				}
				seen[t.ID] = true
				if s := score(t, words, duration, accented); s > 0 {
					out = append(out, scored{t, s})
					best = max(best, s)
				}
			}
			if best >= enough {
				break search
			}
		}
		if best >= minScore {
			break
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	return out, nil
}

// get fetches one LRCLIB answer, waiting its turn first, and waits out a 429 twice before
// giving up.
func (c *Client) get(ctx context.Context, path string, v any) error {
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		retry, err := c.fetch(ctx, path, v)
		if retry <= 0 || attempt == 2 {
			return err
		}
		select {
		case <-time.After(retry):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) wait(ctx context.Context) error {
	c.gate.Lock()
	defer c.gate.Unlock()
	if d := time.Until(c.next); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.next = time.Now().Add(c.Gap)
	return nil
}

// fetch makes one request. retry is how long LRCLIB asked us to wait, after a 429.
func (c *Client) fetch(ctx context.Context, path string, v any) (retry time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("lrclib: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return 0, ErrNotFound
	case http.StatusTooManyRequests:
		retry = 3 * time.Second
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 30 {
			retry = time.Duration(s) * time.Second
		}
		return retry, fmt.Errorf("lrclib: %s", resp.Status)
	default:
		return 0, fmt.Errorf("lrclib: %s", resp.Status)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(v); err != nil {
		return 0, fmt.Errorf("lrclib: %w", err)
	}
	return 0, nil
}

func lookup[K comparable, V any](c *Client, m map[K]cached[V], k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := m[k]
	if !ok || time.Since(e.at) > cacheFor {
		var zero V
		return zero, false
	}
	return e.value, true
}

func store[K comparable, V any](c *Client, m map[K]cached[V], k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(m) >= cacheLimit {
		clear(m)
	}
	m[k] = cached[V]{v, time.Now()}
}
