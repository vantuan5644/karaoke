package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/vantuan5644/karaoke/internal/musickey"
	"github.com/vantuan5644/karaoke/internal/room"
)

// KeyFinder hears the key a song file is in (musickey.Finder), so pitch correction can pull
// a singer to the song's own notes.
type KeyFinder interface {
	Key(ctx context.Context, path string) (musickey.Key, error)
}

// songKeyAnswer is GET /api/songkey.
type songKeyAnswer struct {
	QID     string        `json:"qid"`
	Key     *musickey.Key `json:"key,omitempty"`
	Pending bool          `json:"pending,omitempty"` // the song is downloading, or waiting its turn to be heard: ask again
	Error   string        `json:"error,omitempty"`   // why there is no key
}

// songKey answers with the key of a song in the queue. A YouTube song is heard from its
// download, so asking for one that hasn't been downloaded starts the download. A song already
// playing embedded carries on embedded: the TV keeps the player a song started in.
func (s *Server) songKey(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Keys == nil {
		writeError(w, http.StatusNotFound, "this server can't hear songs' keys: it needs ffmpeg")
		return
	}
	qid := r.URL.Query().Get("qid")
	entry, ok := findEntry(s.cfg.Room.State(), qid)
	if !ok {
		writeError(w, http.StatusNotFound, "no such entry")
		return
	}
	ans := songKeyAnswer{QID: qid}
	path, why := s.songFile(entry.Song)
	switch {
	case why != "":
		ans.Error = why
	case path == "":
		ans.Pending = true
		s.wantForKey(entry.Song.VideoID)
	default:
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		k, err := s.cfg.Keys.Key(ctx, path)
		switch {
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			ans.Pending = true // other songs were being heard first: ask again
		case errors.Is(err, musickey.ErrNoMusic):
			ans.Error = "There is no music in it to hear a key in."
		case err != nil:
			ans.Error = "Couldn't hear its key: " + err.Error()
		default:
			ans.Key = &k
		}
	}
	writeJSON(w, http.StatusOK, ans)
}

// songFile is where a song's sound is on this computer: a path, "" while it downloads, or why
// it can't be had.
func (s *Server) songFile(song room.Song) (path, why string) {
	if song.Source == "library" {
		if s.cfg.Library == nil {
			return "", "This server's library is switched off."
		}
		it, ok := s.cfg.Library.Get(song.LibraryID)
		if !ok {
			return "", "That file is no longer in the library."
		}
		return it.Media, ""
	}
	d := s.cfg.Downloader
	if d == nil || song.VideoID == "" {
		return "", "The song's sound isn't on this computer, and yt-dlp isn't installed to download it."
	}
	if p, ok := d.Path(song.VideoID); ok {
		return p, ""
	}
	if st := d.Status(song.VideoID); st.State == "failed" {
		return "", "Download failed: " + st.Error
	}
	return "", ""
}

// wantForKey downloads a song so its key can be heard. Only songs still in the queue are
// remembered.
func (s *Server) wantForKey(videoID string) {
	st := s.cfg.Room.State()
	inQueue := map[string]bool{}
	for _, e := range append(append([]room.Entry(nil), st.Queue...), currentEntries(st)...) {
		inQueue[e.Song.VideoID] = true
	}
	s.mu.Lock()
	added := !s.keyWant[videoID]
	if s.keyWant == nil {
		s.keyWant = map[string]bool{}
	}
	for id := range s.keyWant {
		if !inQueue[id] {
			delete(s.keyWant, id)
		}
	}
	s.keyWant[videoID] = true
	s.mu.Unlock()
	if added {
		s.roomChanged()
	}
}

func currentEntries(st room.State) []room.Entry {
	if st.Current == nil {
		return nil
	}
	return []room.Entry{*st.Current}
}

// keyWanted is a copy of the songs being downloaded to hear their keys.
func (s *Server) keyWanted() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.keyWant))
	for id := range s.keyWant {
		out[id] = true
	}
	return out
}
