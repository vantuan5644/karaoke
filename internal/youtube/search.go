// Package youtube searches YouTube without an API key, through the same InnerTube endpoint
// youtube.com's own search page calls, and checks which videos may play embedded.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Video is one search result.
type Video struct {
	ID       string  `json:"videoId"`
	Title    string  `json:"title"`
	Channel  string  `json:"artist"`
	Duration float64 `json:"duration"`
	Views    string  `json:"views,omitempty"`
	Thumb    string  `json:"thumb"`
}

// Page is one page of results and the token for the next, if there is one.
type Page struct {
	Videos []Video `json:"results"`
	More   string  `json:"more,omitempty"`
}

const (
	defaultBase   = "https://www.youtube.com"
	clientName    = "WEB"
	clientVersion = "2.20251101.00.00"
	// onlyVideos is the search filter youtube.com sends for Filters > Type > Video.
	onlyVideos  = "EgIQAQ%3D%3D"
	maxBodySize = 8 << 20
	userAgent   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidID reports whether id has the shape of a YouTube video ID.
func ValidID(id string) bool { return videoIDPattern.MatchString(id) }

// Client talks to YouTube. The zero value is not usable; use New.
type Client struct {
	HTTP     *http.Client
	Base     string // https://www.youtube.com, or a test server
	Language string // hl, e.g. "en"
	Region   string // gl, e.g. "US"; empty lets YouTube go by the caller's address
	embeds   *embedCache
}

// New returns a client for youtube.com.
func New() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		Base:     defaultBase,
		Language: "en",
		embeds:   newEmbedCache(12 * time.Hour),
	}
}

// Search returns the first page of videos for query, or the page after more when it is set.
func (c *Client) Search(ctx context.Context, query, more string) (Page, error) {
	clientCtx := map[string]any{"clientName": clientName, "clientVersion": clientVersion, "hl": c.Language}
	if c.Region != "" {
		clientCtx["gl"] = c.Region
	}
	body := map[string]any{"context": map[string]any{"client": clientCtx}}
	if more != "" {
		body["continuation"] = more
	} else {
		body["query"] = query
		body["params"] = onlyVideos
	}
	var raw any
	if err := c.post(ctx, "/youtubei/v1/search?prettyPrint=false", body, &raw); err != nil {
		return Page{}, err
	}
	return parsePage(raw), nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("youtube search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("youtube search: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(out); err != nil {
		return fmt.Errorf("youtube search: %w", err)
	}
	return nil
}

// parsePage walks the whole response instead of following one path through it: YouTube
// moves its renderers around, but a videoRenderer stays a videoRenderer.
func parsePage(raw any) Page {
	page := Page{}
	seen := map[string]bool{}
	walk(raw, func(key string, value map[string]any) {
		switch key {
		case "videoRenderer":
			if v, ok := parseVideo(value); ok && !seen[v.ID] {
				seen[v.ID] = true
				page.Videos = append(page.Videos, v)
			}
		case "continuationCommand":
			if token, ok := value["token"].(string); ok && page.More == "" {
				page.More = token
			}
		}
	})
	return page
}

func walk(node any, visit func(key string, value map[string]any)) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if m, ok := v.(map[string]any); ok {
				visit(k, m)
			}
			walk(v, visit)
		}
	case []any:
		for _, v := range n {
			walk(v, visit)
		}
	}
}

func parseVideo(r map[string]any) (Video, bool) {
	id, _ := r["videoId"].(string)
	if !ValidID(id) {
		return Video{}, false
	}
	length := text(r["lengthText"])
	if length == "" { // a live stream or a premiere: nothing to sing along to yet
		return Video{}, false
	}
	duration, err := ParseDuration(length)
	if err != nil {
		return Video{}, false
	}
	channel := text(r["ownerText"])
	if channel == "" {
		channel = text(r["longBylineText"])
	}
	views := text(r["shortViewCountText"])
	if views == "" {
		views = text(r["viewCountText"])
	}
	return Video{
		ID:       id,
		Title:    text(r["title"]),
		Channel:  channel,
		Duration: duration,
		Views:    views,
		Thumb:    "https://i.ytimg.com/vi/" + id + "/mqdefault.jpg",
	}, true
}

// text reads YouTube's two shapes of text: {"simpleText": "..."} and {"runs": [{"text": "..."}]}.
func text(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["simpleText"].(string); ok {
		return strings.TrimSpace(s)
	}
	runs, _ := m["runs"].([]any)
	var b strings.Builder
	for _, run := range runs {
		if r, ok := run.(map[string]any); ok {
			if s, ok := r["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// ParseDuration reads "4:05" or "1:02:03" as seconds.
func ParseDuration(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, errors.New("not a duration: " + s)
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, errors.New("not a duration: " + s)
		}
		total = total*60 + n
	}
	return float64(total), nil
}

// WithKaraoke adds "karaoke" to a query that does not already ask for it, so a plain song
// name finds sing-along versions first.
func WithKaraoke(query string) string {
	lower := strings.ToLower(query)
	for _, word := range []string{"karaoke", "instrumental", "minus one", "ktv"} {
		if strings.Contains(lower, word) {
			return query
		}
	}
	return strings.TrimSpace(query) + " karaoke"
}
