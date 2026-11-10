package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/vantuan5644/karaoke/internal/room"
	"github.com/vantuan5644/karaoke/internal/youtube"
)

// PlayerStatus is what the TV last reported about the song playing.
type PlayerStatus struct {
	QID      string     `json:"qid"`
	State    string     `json:"state"` // "playing", "paused", "buffering", "waiting", "idle" or "error"
	Position float64    `json:"position"`
	Duration float64    `json:"duration"`
	Volume   int        `json:"volume"`
	Muted    bool       `json:"muted"`
	Started  bool       `json:"started"` // the TV has had the click browsers ask for before sound
	Mic      *MicStatus `json:"mic,omitempty"`
	Lyrics   bool       `json:"lyrics"` // the lyrics panel is open
}

// MicStatus is the TV's mic, so phones can show and nudge it.
type MicStatus struct {
	On        bool    `json:"on"`
	Volume    int     `json:"volume"`
	Echo      int     `json:"echo"`
	Reverb    int     `json:"reverb"`
	Device    string  `json:"device,omitempty"`
	LatencyMs float64 `json:"latencyMs,omitempty"` // the measured round trip
	OutputMs  float64 `json:"outputMs,omitempty"`  // what the browser says its output adds
	Error     string  `json:"error,omitempty"`
}

const statusFresh = 10 * time.Second

var (
	errUnplayable = errors.New("this video can't play embedded, and yt-dlp isn't installed to download it")
	playerActions = map[string]bool{
		"toggle": true, "play": true, "pause": true, "restart": true,
		"seek": true, "volume": true, "mute": true,
		"mic-toggle": true, "mic-volume": true, "mic-echo": true, "mic-reverb": true, "mic-preset": true,
		"lyrics-toggle": true, "lyrics-scroll": true,
	}
	playerStates = map[string]bool{
		"playing": true, "paused": true, "buffering": true, "waiting": true, "idle": true, "error": true,
	}
)

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	// Subscribe and take the first snapshot together: a change in between would otherwise
	// reach neither.
	s.publishing.Lock()
	ch := s.hub.subscribe()
	state, err := json.Marshal(s.snapshot())
	s.publishing.Unlock()
	if err != nil {
		s.hub.unsubscribe(ch)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.serveEvents(w, r, ch, []event{{"state", state}})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"urls": s.cfg.URLs, "version": s.cfg.Version, "features": s.features(),
	})
}

type searchResults struct {
	Results []room.Song `json:"results"`
	More    string      `json:"more,omitempty"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query, more := strings.TrimSpace(q.Get("q")), q.Get("more")
	if source := q.Get("source"); source != "" && source != "youtube" {
		writeError(w, http.StatusBadRequest, "unknown source "+source)
		return
	}
	if len(query) > 200 || len(more) > 4096 {
		writeError(w, http.StatusBadRequest, "query too long")
		return
	}
	if query == "" && more == "" {
		writeJSON(w, http.StatusOK, searchResults{Results: []room.Song{}})
		return
	}
	if !s.limiter.allow() {
		writeError(w, http.StatusTooManyRequests, "too many searches, try again in a moment")
		return
	}
	if q.Get("karaoke") == "1" {
		query = youtube.WithKaraoke(query)
	}
	page, err := s.cfg.YouTube.Search(r.Context(), query, more)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	ids := make([]string, len(page.Videos))
	for i, v := range page.Videos {
		ids[i] = v.ID
	}
	embeds := s.cfg.YouTube.Embeddable(r.Context(), ids)
	out := searchResults{Results: make([]room.Song, len(page.Videos)), More: page.More}
	for i, v := range page.Videos {
		out.Results[i] = room.Song{
			Source: "youtube", Title: v.Title, Artist: v.Channel, Duration: v.Duration,
			Views: v.Views, Thumb: v.Thumb, VideoID: v.ID, Embeddable: embeds[v.ID],
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type enqueueRequest struct {
	Song   room.Song `json:"song"`
	Singer string    `json:"singer"`
	Next   bool      `json:"next"`
}

func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	var req enqueueRequest
	if !readJSON(w, r, &req) {
		return
	}
	song, err := s.checkSong(r.Context(), req.Song)
	if errors.Is(err, errUnplayable) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := s.cfg.Room.Add(song, clean(req.Singer, 40), req.Next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, enqueueResponse{Entry: entry, Ahead: ahead(s.cfg.Room.State(), entry.QID)})
}

type enqueueResponse struct {
	Entry room.Entry `json:"entry"`
	Ahead int        `json:"ahead"` // songs that play before it: 0 when it plays now
}

func ahead(st room.State, qid string) int {
	if st.Current == nil || st.Current.QID == qid {
		return 0
	}
	for i, e := range st.Queue {
		if e.QID == qid {
			return i + 1
		}
	}
	return 0
}

// checkSong rebuilds a song from what a phone sent, keeping only what can be checked or
// is harmless to show, and asks YouTube itself whether the video embeds.
func (s *Server) checkSong(ctx context.Context, in room.Song) (room.Song, error) {
	switch in.Source {
	case "youtube":
		if !youtube.ValidID(in.VideoID) {
			return room.Song{}, errors.New("not a YouTube video ID")
		}
		title := clean(in.Title, 200)
		if title == "" {
			title = "YouTube video " + in.VideoID
		}
		song := room.Song{
			Source: "youtube", Title: title, Artist: clean(in.Artist, 100),
			Duration: clampFloat(in.Duration, 0, 36000), Views: clean(in.Views, 40),
			VideoID: in.VideoID, Thumb: "https://i.ytimg.com/vi/" + in.VideoID + "/mqdefault.jpg",
			Embeddable: s.cfg.YouTube.Embeddable(ctx, []string{in.VideoID})[in.VideoID],
		}
		if !song.Embeddable && s.cfg.Downloader == nil {
			return room.Song{}, errUnplayable
		}
		return song, nil
	default:
		return room.Song{}, errors.New("unknown source " + clean(in.Source, 20))
	}
}

func (s *Server) dequeue(w http.ResponseWriter, r *http.Request) {
	s.roomResult(w, s.cfg.Room.Remove(r.PathValue("qid")))
}

func (s *Server) move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To int `json:"to"`
	}
	if readJSON(w, r, &req) {
		s.roomResult(w, s.cfg.Room.Move(r.PathValue("qid"), req.To))
	}
}

func (s *Server) clearQueue(w http.ResponseWriter, r *http.Request) {
	s.roomResult(w, s.cfg.Room.Clear())
}

func (s *Server) next(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QID string `json:"qid"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	advanced, err := s.cfg.Room.Advance(req.QID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"advanced": advanced})
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string  `json:"action"`
		Value  float64 `json:"value"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !playerActions[req.Action] {
		writeError(w, http.StatusBadRequest, "unknown action")
		return
	}
	data, _ := json.Marshal(map[string]any{"action": req.Action, "value": clampFloat(req.Value, -600, 600)})
	s.hub.publish("command", data)
	writeJSON(w, http.StatusOK, map[string]int{"players": s.hub.count()})
}

func (s *Server) playerStatus(w http.ResponseWriter, r *http.Request) {
	var p PlayerStatus
	if !readJSON(w, r, &p) {
		return
	}
	if !playerStates[p.State] {
		writeError(w, http.StatusBadRequest, "unknown state")
		return
	}
	p.QID = clean(p.QID, 32)
	p.Position = clampFloat(p.Position, 0, 36000)
	p.Duration = clampFloat(p.Duration, 0, 36000)
	p.Volume = int(clampFloat(float64(p.Volume), 0, 100))
	if m := p.Mic; m != nil {
		m.Volume = int(clampFloat(float64(m.Volume), 0, 200))
		m.Echo = int(clampFloat(float64(m.Echo), 0, 100))
		m.Reverb = int(clampFloat(float64(m.Reverb), 0, 100))
		m.Device = clean(m.Device, 100)
		m.LatencyMs = clampFloat(m.LatencyMs, 0, 5000)
		m.OutputMs = clampFloat(m.OutputMs, 0, 5000)
		m.Error = clean(m.Error, 200)
	}
	s.mu.Lock()
	s.status, s.statusAt = p, time.Now()
	s.mu.Unlock()
	data, _ := json.Marshal(p)
	s.hub.publish("player", data)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) player() (PlayerStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, !s.statusAt.IsZero() && time.Since(s.statusAt) < statusFresh
}

// embedFailed is the TV saying the IFrame player refused a video (error 101 or 150), which
// oEmbed missed: remember it, and download the video if yt-dlp is there.
func (s *Server) embedFailed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QID  string `json:"qid"`
		Code int    `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	st := s.cfg.Room.State()
	videoID := ""
	if st.Current != nil && st.Current.QID == req.QID {
		videoID = st.Current.Song.VideoID
	}
	if videoID == "" {
		writeError(w, http.StatusNotFound, "that entry is not playing")
		return
	}
	// Code 0 is the TV's own trouble (YouTube's player never loaded there), not the video's:
	// this play goes to a download, but searches keep calling the video embeddable.
	if req.Code == 0 {
		log.Printf("the TV could not load YouTube's player for %s; playing a download instead", videoID)
	} else {
		log.Printf("YouTube's player refused to embed %s (error %d)", videoID, req.Code)
		s.cfg.YouTube.MarkNotEmbeddable(videoID)
	}
	s.roomResult(w, s.cfg.Room.MarkNotEmbeddable(videoID))
}

func (s *Server) setSettings(w http.ResponseWriter, r *http.Request) {
	var settings room.Settings
	if !readJSON(w, r, &settings) {
		return
	}
	if settings.AlwaysDownload && s.cfg.Downloader == nil {
		writeError(w, http.StatusUnprocessableEntity, "downloads need yt-dlp, which isn't installed")
		return
	}
	s.roomResult(w, s.cfg.Room.SetSettings(settings))
}

func (s *Server) youtubeFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.cfg.Downloader == nil || !youtube.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	path, ok := s.cfg.Downloader.Path(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

func (s *Server) roomResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, room.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, room.ErrBadMove):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// clean trims s, drops control characters, and cuts it to at most max characters.
func clean(s string, max int) string {
	out := make([]rune, 0, len(s))
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) {
			continue
		}
		if len(out) == max {
			break
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Max(lo, math.Min(hi, v))
}

// limiter is a token bucket: rate a second, up to burst at once. It keeps a busy party from
// sending YouTube enough searches to get the server's address throttled.
type limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens = math.Min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (s *Server) setKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key *int `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Key == nil {
		writeError(w, http.StatusBadRequest, "key is semitones from the original, -6 to 6")
		return
	}
	s.roomResult(w, s.cfg.Room.SetKey(r.PathValue("qid"), *req.Key))
}
