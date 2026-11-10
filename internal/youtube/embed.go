package youtube

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Many of the most-watched karaoke videos (Sing King's, for one) refuse to play inside
// another site, and the IFrame player only says so once it fails (error 101 or 150). The
// oEmbed endpoint answers the same question up front: 401 for exactly those videos, 200 for
// the rest (11 of 11 agreed with a real IFrame player on 2026-11-10).

const (
	embedParallel = 8
	embedTimeout  = 4 * time.Second
)

// Embeddable reports, for each of ids, whether it may play in the IFrame player. When
// YouTube cannot be asked, a video counts as embeddable: the player itself has the last word.
func (c *Client) Embeddable(ctx context.Context, ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, embedParallel)
	for _, id := range ids {
		if ok, known := c.embeds.get(id); known {
			out[id] = ok
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			ok, known := c.askEmbeddable(ctx, id)
			if known {
				c.embeds.put(id, ok)
			}
			mu.Lock()
			out[id] = ok
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

// MarkNotEmbeddable records what the IFrame player found out the hard way.
func (c *Client) MarkNotEmbeddable(id string) { c.embeds.put(id, false) }

func (c *Client) askEmbeddable(ctx context.Context, id string) (ok, known bool) {
	ctx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	watch := "https://www.youtube.com/watch?v=" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.Base+"/oembed?format=json&url="+url.QueryEscape(watch), nil)
	if err != nil {
		return true, false
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return true, false
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, true
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, true
	case http.StatusNotFound, http.StatusBadRequest: // private, removed, or not a video
		return false, true
	default:
		return true, false
	}
}

type embedCache struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]embedAnswer
}

type embedAnswer struct {
	ok bool
	at time.Time
}

func newEmbedCache(ttl time.Duration) *embedCache {
	return &embedCache{ttl: ttl, seen: map[string]embedAnswer{}}
}

func (c *embedCache) get(id string) (ok, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, found := c.seen[id]
	if !found || time.Since(a.at) > c.ttl {
		return false, false
	}
	return a.ok, true
}

func (c *embedCache) put(id string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) > 20000 { // a long party, not a leak
		c.seen = map[string]embedAnswer{}
	}
	c.seen[id] = embedAnswer{ok: ok, at: time.Now()}
}
