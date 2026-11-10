package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vantuan5644/karaoke/internal/lyrics"
	"github.com/vantuan5644/karaoke/internal/room"
)

// LyricsSource is the part of lyrics.Client the server uses.
type LyricsSource interface {
	Search(ctx context.Context, query string) ([]lyrics.Track, error)
	Get(ctx context.Context, id int) (lyrics.Track, error)
	Match(ctx context.Context, title string, duration float64) (lyrics.Track, bool, error)
}

// lyricsAnswer is what GET /api/lyrics says about one entry's lyrics.
type lyricsAnswer struct {
	QID    string        `json:"qid"`
	Track  *lyrics.Track `json:"track"`  // null when there are none to show
	Chosen bool          `json:"chosen"` // someone picked these, rather than the automatic match
	Off    bool          `json:"off"`    // someone chose no lyrics for this song
	Query  string        `json:"query"`  // where a search by hand could start
}

// lyricsCandidate is one search result, without the whole text.
type lyricsCandidate struct {
	ID       int     `json:"id"`
	Track    string  `json:"track"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Lines    int     `json:"lines"`
	Preview  string  `json:"preview"`
}

func findEntry(st room.State, qid string) (room.Entry, bool) {
	if st.Current != nil && st.Current.QID == qid {
		return *st.Current, true
	}
	for _, list := range [][]room.Entry{st.Queue, st.History} {
		for _, e := range list {
			if e.QID == qid {
				return e, true
			}
		}
	}
	return room.Entry{}, false
}

func (s *Server) lyricsFor(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Lyrics == nil {
		writeError(w, http.StatusNotFound, "lyrics are switched off on this server")
		return
	}
	qid := r.URL.Query().Get("qid")
	entry, ok := findEntry(s.cfg.Room.State(), qid)
	if !ok {
		writeError(w, http.StatusNotFound, "no such entry")
		return
	}
	ans := lyricsAnswer{QID: qid, Query: strings.Join(lyrics.Parts(lyricsTitle(entry.Song)), " ")}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	switch id := entry.Song.LyricsID; {
	case id == room.NoLyrics:
		ans.Off = true
	case id > 0:
		t, err := s.cfg.Lyrics.Get(ctx, id)
		if err != nil && !errors.Is(err, lyrics.ErrNotFound) {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if err == nil {
			ans.Track, ans.Chosen = &t, true
		}
	default:
		t, found, err := s.cfg.Lyrics.Match(ctx, lyricsTitle(entry.Song), entry.Song.Duration)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if found {
			ans.Track = &t
		}
	}
	writeJSON(w, http.StatusOK, ans)
}

func (s *Server) lyricsSearch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Lyrics == nil {
		writeError(w, http.StatusNotFound, "lyrics are switched off on this server")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 200 {
		writeError(w, http.StatusBadRequest, "query too long")
		return
	}
	if !s.limiter.allow() {
		writeError(w, http.StatusTooManyRequests, "too many searches; try again in a moment")
		return
	}
	found, err := s.cfg.Lyrics.Search(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := []lyricsCandidate{}
	for _, t := range found {
		if t.Instrumental || t.Lyrics == "" {
			continue
		}
		var preview []string
		for _, line := range strings.Split(t.Lyrics, "\n") {
			if line = strings.TrimSpace(line); line != "" && len(preview) < 2 {
				preview = append(preview, line)
			}
		}
		out = append(out, lyricsCandidate{
			ID: t.ID, Track: t.Track, Artist: t.Artist, Album: t.Album, Duration: t.Duration,
			Lines: strings.Count(t.Lyrics, "\n") + 1, Preview: strings.Join(preview, " / "),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

func (s *Server) setLyrics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID *int `json:"id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID == nil || *req.ID < room.NoLyrics {
		writeError(w, http.StatusBadRequest, "id is an LRCLIB id, 0 for automatic, or -1 for none")
		return
	}
	s.roomResult(w, s.cfg.Room.SetLyrics(r.PathValue("qid"), *req.ID))
}
