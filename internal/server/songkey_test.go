package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/vantuan5644/karaoke/internal/library"
	"github.com/vantuan5644/karaoke/internal/media"
	"github.com/vantuan5644/karaoke/internal/musickey"
)

type fakeKeys struct {
	mu    sync.Mutex
	heard []string
	err   error
}

func (f *fakeKeys) Key(ctx context.Context, path string) (musickey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heard = append(f.heard, path)
	if f.err != nil {
		return musickey.Key{}, f.err
	}
	return musickey.Key{Tonic: 9, Minor: true, Name: "A minor", Notes: 2741, Fit: 0.9}, nil
}

func (f *fakeKeys) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.heard...)
}

func (h *harness) songKey(qid string) songKeyAnswer {
	h.t.Helper()
	resp, body := h.do("GET", "/api/songkey?qid="+qid, "")
	var ans songKeyAnswer
	if err := json.Unmarshal(body, &ans); err != nil || resp.StatusCode != 200 {
		h.t.Fatalf("songkey %s: %d %s", qid, resp.StatusCode, body)
	}
	return ans
}

func TestSongKeyOfALibraryFile(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "Adele - Someone Like You.mp3")
	if err := os.WriteFile(song, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := library.New([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	lib.Scan()
	keys := &fakeKeys{}
	h := newHarnessWith(t, false, func(c *Config) { c.Library = lib; c.Keys = keys })
	if !h.state().Features.Keys {
		t.Error("features.keys is off with a key finder")
	}
	_, body := h.do("GET", "/api/search?source=library&q=someone", "")
	var res searchResults
	json.Unmarshal(body, &res)
	if len(res.Results) != 1 {
		t.Fatalf("search: %s", body)
	}
	_, body = h.do("POST", "/api/queue", `{"song":{"source":"library","libraryId":"`+res.Results[0].LibraryID+`"},"singer":"An"}`)
	var added enqueueResponse
	json.Unmarshal(body, &added)
	ans := h.songKey(added.Entry.QID)
	if ans.Key == nil || ans.Key.Name != "A minor" || ans.Key.Notes != 2741 || ans.Pending {
		t.Errorf("answer %+v", ans)
	}
	if got := keys.paths(); !slices.Equal(got, []string{song}) {
		t.Errorf("heard %v, want the library file", got)
	}
	if resp, _ := h.do("GET", "/api/songkey?qid=nope", ""); resp.StatusCode != 404 {
		t.Errorf("an unknown entry: %d", resp.StatusCode)
	}
	keys.err = musickey.ErrNoMusic
	if ans := h.songKey(added.Entry.QID); ans.Key != nil || ans.Error == "" {
		t.Errorf("no music: %+v", ans)
	}
}

func TestSongKeyDownloadsAYouTubeSongToHearIt(t *testing.T) {
	keys := &fakeKeys{}
	h := newHarnessWith(t, true, func(c *Config) { c.Keys = keys })
	h.add("playing0001", "An") // plays embedded: nothing to download
	next := h.add("nextsong001", "Binh")
	if got := h.dl.lastWanted(); len(got) != 0 {
		t.Fatalf("downloads before anyone asked for a key: %v", got)
	}
	ans := h.songKey(next.Entry.QID)
	if !ans.Pending || ans.Key != nil {
		t.Errorf("before the download: %+v", ans)
	}
	if got := h.dl.lastWanted(); !slices.Equal(got, []string{"nextsong001"}) {
		t.Errorf("downloading %v, want the song asked about", got)
	}
	if len(keys.paths()) != 0 {
		t.Error("heard a file that isn't there yet")
	}
	path := filepath.Join(t.TempDir(), "nextsong001.mp4")
	h.dl.mu.Lock()
	h.dl.files["nextsong001"] = path
	h.dl.status["nextsong001"] = media.Status{State: "ready"}
	h.dl.mu.Unlock()
	if ans := h.songKey(next.Entry.QID); ans.Key == nil || ans.Key.Name != "A minor" {
		t.Errorf("after the download: %+v", ans)
	}
	if got := keys.paths(); !slices.Equal(got, []string{path}) {
		t.Errorf("heard %v", got)
	}

	// A download that failed says so, and is not asked for again.
	first := h.state().Current
	h.dl.mu.Lock()
	h.dl.status["playing0001"] = media.Status{State: "failed", Error: "403"}
	h.dl.mu.Unlock()
	if ans := h.songKey(first.QID); ans.Pending || ans.Error == "" {
		t.Errorf("a failed download: %+v", ans)
	}
}

func TestSongKeyNeedsFFmpeg(t *testing.T) {
	h := newHarness(t, true)
	cur := h.add("playing0001", "An")
	if resp, _ := h.do("GET", "/api/songkey?qid="+cur.Entry.QID, ""); resp.StatusCode != 404 {
		t.Errorf("without a key finder: %d", resp.StatusCode)
	}
	if h.state().Features.Keys {
		t.Error("features.keys is on without a key finder")
	}
}
