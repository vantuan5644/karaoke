package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/vantuan5644/karaoke/internal/library"
	"github.com/vantuan5644/karaoke/internal/room"
)

// Library is the songs in folders on this computer (see internal/library).
type Library interface {
	Get(id string) (library.Item, bool)
	Search(query string, limit int) []library.Item
	Measure(ctx context.Context, id string) float64
	Len() int
}

const librarySearchLimit = 40

func librarySong(it library.Item) room.Song {
	return room.Song{
		Source: "library", Title: it.Title, Artist: it.Artist, Duration: it.Duration,
		LibraryID: it.ID, Format: it.Format, Where: it.Where,
	}
}

// searchLibrary answers a search of the library: local, so it needs no rate limit.
func (s *Server) searchLibrary(w http.ResponseWriter, query string) {
	if s.cfg.Library == nil {
		writeError(w, http.StatusNotFound, "this server has no library (start it with -library)")
		return
	}
	found := s.cfg.Library.Search(query, librarySearchLimit)
	out := searchResults{Results: make([]room.Song, len(found))}
	for i, it := range found {
		out.Results[i] = librarySong(it)
	}
	writeJSON(w, http.StatusOK, out)
}

// librarySongFor rebuilds a song a phone picked from the library, from the library itself.
func (s *Server) librarySongFor(ctx context.Context, id string) (room.Song, error) {
	if s.cfg.Library == nil {
		return room.Song{}, errNoLibrary
	}
	it, ok := s.cfg.Library.Get(id)
	if !ok {
		return room.Song{}, errNotInLibrary
	}
	song := librarySong(it)
	song.Duration = s.cfg.Library.Measure(ctx, id)
	return song, nil
}

// libraryFile serves a library song's audio or video, or its CD+G graphics. Only files the
// scan found can be asked for, by ID: no path from the request reaches the disk.
func (s *Server) libraryFile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Library == nil {
		http.NotFound(w, r)
		return
	}
	it, ok := s.cfg.Library.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	path := it.Media
	if strings.HasSuffix(r.URL.Path, "/cdg") {
		if it.CDG == "" {
			http.NotFound(w, r)
			return
		}
		path = it.CDG
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

// lyricsTitle is what lyrics are matched against: a YouTube video's title already names the
// artist, a library song's file name was split into artist and title.
func lyricsTitle(song room.Song) string {
	if song.Source == "library" && song.Artist != "" {
		return song.Artist + " - " + song.Title
	}
	return song.Title
}
