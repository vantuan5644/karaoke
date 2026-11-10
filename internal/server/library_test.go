package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/vantuan5644/karaoke/internal/library"
)

// libraryHarness serves a library folder holding an MP3+G and a video.
func libraryHarness(t *testing.T) (*harness, string, *library.Library) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{
		"SC8125-01 - Queen - Bohemian Rhapsody.mp3": "mp3 audio bytes",
		"SC8125-01 - Queen - Bohemian Rhapsody.cdg": "cdg graphics bytes",
		"Abba - Dancing Queen.mp4":                  "mp4 video bytes",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := library.New([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWith(t, false, func(c *Config) { c.Library = lib })
	return h, dir, lib
}

func TestLibrarySearchQueueAndPlay(t *testing.T) {
	h, dir, lib := libraryHarness(t)
	if got := h.state().Features.Library; got != 2 {
		t.Errorf("features.library = %d, want 2", got)
	}
	_, body := h.do("GET", "/api/search?source=library&q=queen", "")
	var res searchResults
	if err := json.Unmarshal(body, &res); err != nil || len(res.Results) != 2 {
		t.Fatalf("search = %s (%v)", body, err)
	}
	var cdg string
	for _, s := range res.Results {
		if s.Source != "library" || s.LibraryID == "" {
			t.Errorf("result %+v", s)
		}
		if s.Format == "cdg" {
			cdg = s.LibraryID
		}
	}

	// The phone's copy of the song is not trusted: the library says what it is.
	resp, body := h.do("POST", "/api/queue", `{"song":{"source":"library","libraryId":"`+cdg+`","title":"spoofed"},"singer":"An"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("enqueue = %d %s", resp.StatusCode, body)
	}
	cur := h.state().Current
	if cur.Song.Title != "Bohemian Rhapsody" || cur.Song.Artist != "Queen" || cur.Song.Format != "cdg" {
		t.Errorf("queued %+v", cur.Song)
	}
	if p := cur.Play; p.Mode != "file" || p.URL != "/media/lib/"+cdg || p.CDG != "/media/lib/"+cdg+"/cdg" {
		t.Errorf("play = %+v", p)
	}

	// The files, with ranges (the TV seeks).
	req, _ := http.NewRequest("GET", h.web.URL+"/media/lib/"+cdg, nil)
	req.Header.Set("Range", "bytes=0-2")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 206 || string(got) != "mp3" {
		t.Errorf("range = %d %q", r.StatusCode, got)
	}
	if resp, body := h.do("GET", "/media/lib/"+cdg+"/cdg", ""); resp.StatusCode != 200 || string(body) != "cdg graphics bytes" {
		t.Errorf("cdg = %d %q", resp.StatusCode, body)
	}
	video := lib.Search("dancing", 1)[0].ID
	for path, want := range map[string]int{
		"/media/lib/" + video + "/cdg":       404, // a video has no graphics
		"/media/lib/0123456789abcdef":        404,
		"/media/lib/..%2f..%2fetc%2fpasswd":  404,
		"/media/lib/" + video:                200,
		"/api/search?source=library&q=":      200,
		"/api/search?source=nowhere&q=queen": 400,
	} {
		if resp, _ := h.do("GET", path, ""); resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}

	// A song whose file goes away says so instead of playing silence.
	os.Remove(filepath.Join(dir, "SC8125-01 - Queen - Bohemian Rhapsody.mp3"))
	lib.Scan()
	if p := h.state().Current.Play; p.Mode != "error" {
		t.Errorf("after the file went: %+v", p)
	}
	if resp, _ := h.do("POST", "/api/queue", `{"song":{"source":"library","libraryId":"`+cdg+`"},"singer":"An"}`); resp.StatusCode != 422 {
		t.Errorf("queueing a song no longer there = %d, want 422", resp.StatusCode)
	}
}

func TestNoLibrary(t *testing.T) {
	h := newHarness(t, false)
	if resp, _ := h.do("GET", "/api/search?source=library&q=queen", ""); resp.StatusCode != 404 {
		t.Errorf("search = %d, want 404", resp.StatusCode)
	}
	if resp, _ := h.do("POST", "/api/queue", `{"song":{"source":"library","libraryId":"0123456789abcdef"},"singer":"An"}`); resp.StatusCode != 422 {
		t.Errorf("enqueue = %d, want 422", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/media/lib/0123456789abcdef", ""); resp.StatusCode != 404 {
		t.Errorf("file = %d, want 404", resp.StatusCode)
	}
}

func TestLyricsForALibrarySongUseItsArtist(t *testing.T) {
	if got := lyricsTitle(librarySong(library.Item{Title: "Hello", Artist: "Adele"})); got != "Adele - Hello" {
		t.Errorf("lyricsTitle = %q", got)
	}
}
