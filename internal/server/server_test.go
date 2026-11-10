package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vantuan5644/karaoke/internal/lyrics"
	"github.com/vantuan5644/karaoke/internal/media"
	"github.com/vantuan5644/karaoke/internal/room"
	"github.com/vantuan5644/karaoke/internal/youtube"
)

const (
	embedOK = "embedOK0001"
	blocked = "blocked0001"
)

type fakeYouTube struct {
	mu      sync.Mutex
	queries []string
	marked  []string
}

func (f *fakeYouTube) Search(ctx context.Context, query, more string) (youtube.Page, error) {
	f.mu.Lock()
	f.queries = append(f.queries, query+"|"+more)
	f.mu.Unlock()
	return youtube.Page{More: "next-page", Videos: []youtube.Video{
		{ID: embedOK, Title: "Song A (Karaoke)", Channel: "Channel", Duration: 200, Thumb: "https://i.ytimg.com/vi/" + embedOK + "/mqdefault.jpg"},
		{ID: blocked, Title: "Song B (Karaoke)", Channel: "Sing King", Duration: 180, Thumb: "https://i.ytimg.com/vi/" + blocked + "/mqdefault.jpg"},
	}}, nil
}

func (f *fakeYouTube) Embeddable(ctx context.Context, ids []string) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = id != blocked
		for _, m := range f.marked {
			if m == id {
				out[id] = false
			}
		}
	}
	return out
}

func (f *fakeYouTube) MarkNotEmbeddable(id string) {
	f.mu.Lock()
	f.marked = append(f.marked, id)
	f.mu.Unlock()
}

type fakeDownloader struct {
	mu     sync.Mutex
	wanted []string
	status map[string]media.Status
	files  map[string]string
}

func (f *fakeDownloader) Status(id string) media.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id]
}

func (f *fakeDownloader) Path(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.files[id]
	return p, ok
}

func (f *fakeDownloader) Want(ids, keep []string) {
	f.mu.Lock()
	f.wanted = append([]string(nil), ids...)
	f.mu.Unlock()
}

func (f *fakeDownloader) lastWanted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wanted
}

type harness struct {
	t   *testing.T
	srv *Server
	web *httptest.Server
	yt  *fakeYouTube
	dl  *fakeDownloader
}

func newHarness(t *testing.T, withDownloads bool) *harness {
	t.Helper()
	rm, err := room.OpenRoom(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, yt: &fakeYouTube{}}
	cfg := Config{
		Room: rm, YouTube: h.yt, Lyrics: fakeLyrics{}, Version: "test", URLs: []string{"http://192.168.1.5:8765"},
		Web: fstest.MapFS{
			"index.html":       {Data: []byte("<h1>remote</h1>")},
			"tv.html":          {Data: []byte("<h1>tv</h1>")},
			"static/remote.js": {Data: []byte("console.log(1)")},
		},
	}
	if withDownloads {
		h.dl = &fakeDownloader{status: map[string]media.Status{}, files: map[string]string{}}
		cfg.Downloader = h.dl
	}
	h.srv = New(cfg)
	h.web = httptest.NewServer(h.srv.Handler())
	t.Cleanup(h.web.Close)
	return h
}

func (h *harness) do(method, path, body string, headers ...string) (*http.Response, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.web.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (h *harness) state() Snapshot {
	h.t.Helper()
	_, data := h.do("GET", "/api/state", "")
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		h.t.Fatalf("state: %v: %s", err, data)
	}
	return s
}

func (h *harness) add(id string, singer string) enqueueResponse {
	h.t.Helper()
	resp, data := h.do("POST", "/api/queue", `{"song":{"source":"youtube","videoId":"`+id+`","title":"T `+id+`","embeddable":true,"thumb":"https://evil.example/x.png"},"singer":"`+singer+`"}`)
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("add %s: %d %s", id, resp.StatusCode, data)
	}
	var out enqueueResponse
	json.Unmarshal(data, &out)
	return out
}

func TestPagesAndHealth(t *testing.T) {
	h := newHarness(t, false)
	for path, want := range map[string]string{"/": "remote", "/tv": "tv", "/static/remote.js": "console.log", "/healthz": "ok"} {
		resp, body := h.do("GET", path, "")
		if resp.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Errorf("GET %s = %d %q", path, resp.StatusCode, body)
		}
	}
	resp, _ := h.do("GET", "/", "")
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src https://www.youtube.com") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if resp, _ := h.do("GET", "/nope", ""); resp.StatusCode != 404 {
		t.Errorf("GET /nope = %d", resp.StatusCode)
	}
}

func TestSearchAddsKaraokeAndMarksEmbeds(t *testing.T) {
	h := newHarness(t, false)
	_, data := h.do("GET", "/api/search?source=youtube&q=my+way&karaoke=1", "")
	var res searchResults
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || !res.Results[0].Embeddable || res.Results[1].Embeddable || res.More != "next-page" {
		t.Fatalf("results = %+v", res)
	}
	h.do("GET", "/api/search?more=next-page", "")
	if got := h.yt.queries; len(got) != 2 || got[0] != "my way karaoke|" || got[1] != "|next-page" {
		t.Errorf("queries = %v", got)
	}
	if resp, _ := h.do("GET", "/api/search?q=", ""); resp.StatusCode != 200 {
		t.Errorf("empty query = %d", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/api/search?source=spotify&q=x", ""); resp.StatusCode != 400 {
		t.Errorf("unknown source = %d", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/api/search?q="+strings.Repeat("a", 201), ""); resp.StatusCode != 400 {
		t.Errorf("long query = %d", resp.StatusCode)
	}
}

func TestEnqueueTrustsOnlyWhatItChecks(t *testing.T) {
	h := newHarness(t, false)
	first := h.add(embedOK, `  An\u0007  `)
	if first.Ahead != 0 || first.Entry.Singer != "An" {
		t.Errorf("first = %+v", first)
	}
	if first.Entry.Song.Thumb != "https://i.ytimg.com/vi/"+embedOK+"/mqdefault.jpg" {
		t.Errorf("thumb = %q, want it rebuilt from the ID", first.Entry.Song.Thumb)
	}
	if second := h.add(embedOK, "Bo"); second.Ahead != 1 {
		t.Errorf("second ahead = %d, want 1", second.Ahead)
	}
	// It claims to embed, but YouTube says no, and there is no yt-dlp: refused.
	resp, body := h.do("POST", "/api/queue", `{"song":{"source":"youtube","videoId":"`+blocked+`","embeddable":true}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("blocked without yt-dlp = %d %s", resp.StatusCode, body)
	}
	for _, bad := range []string{
		`{"song":{"source":"youtube","videoId":"../../../x"}}`,
		`{"song":{"source":"files","videoId":"` + embedOK + `"}}`,
		`not json`,
	} {
		if resp, _ := h.do("POST", "/api/queue", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestWritesFromOtherSitesAndNonJSONAreRefused(t *testing.T) {
	h := newHarness(t, false)
	if resp, _ := h.do("POST", "/api/queue/clear", "{}", "Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site = %d", resp.StatusCode)
	}
	if resp, _ := h.do("POST", "/api/queue/clear", "{}", "Sec-Fetch-Site", "same-origin"); resp.StatusCode != http.StatusNoContent {
		t.Errorf("same-origin = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", h.web.URL+"/api/queue/clear", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d", resp.StatusCode)
	}
	big := `{"singer":"` + strings.Repeat("x", 70<<10) + `"}`
	if resp, _ := h.do("POST", "/api/queue", big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("70 KB body = %d", resp.StatusCode)
	}
}

func TestQueueRoutes(t *testing.T) {
	h := newHarness(t, false)
	a := h.add(embedOK, "a").Entry
	b := h.add(embedOK, "b").Entry
	c := h.add(embedOK, "c").Entry
	if resp, _ := h.do("POST", "/api/queue/"+c.QID+"/move", `{"to":0}`); resp.StatusCode != 204 {
		t.Fatalf("move = %d", resp.StatusCode)
	}
	if s := h.state(); s.Queue[0].QID != c.QID {
		t.Errorf("after move, queue[0] = %s", s.Queue[0].Singer)
	}
	if resp, _ := h.do("POST", "/api/queue/"+c.QID+"/move", `{"to":5}`); resp.StatusCode != 400 {
		t.Errorf("move out of range = %d", resp.StatusCode)
	}
	if resp, _ := h.do("DELETE", "/api/queue/"+b.QID, ""); resp.StatusCode != 204 {
		t.Errorf("delete = %d", resp.StatusCode)
	}
	if resp, _ := h.do("DELETE", "/api/queue/nope", ""); resp.StatusCode != 404 {
		t.Errorf("delete missing = %d", resp.StatusCode)
	}
	_, body := h.do("POST", "/api/player/next", `{"qid":"`+a.QID+`"}`)
	_, again := h.do("POST", "/api/player/next", `{"qid":"`+a.QID+`"}`)
	if !strings.Contains(string(body), "true") || !strings.Contains(string(again), "false") {
		t.Errorf("next = %s then %s, want true then false", body, again)
	}
	s := h.state()
	if s.Current == nil || s.Current.QID != c.QID || len(s.History) != 1 {
		t.Errorf("after next: current %+v, history %d", s.Current, len(s.History))
	}
	if s.Current.Play.Mode != "embed" {
		t.Errorf("play = %+v, want embed", s.Current.Play)
	}
}

func TestDownloadsAreWantedAndPlayedFromTheCopy(t *testing.T) {
	h := newHarness(t, true)
	h.add(embedOK, "a")
	resp, body := h.do("POST", "/api/queue", `{"song":{"source":"youtube","videoId":"`+blocked+`"},"singer":"b"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("blocked with yt-dlp = %d %s", resp.StatusCode, body)
	}
	if got := h.dl.lastWanted(); len(got) != 1 || got[0] != blocked {
		t.Errorf("wanted = %v, want only the blocked video", got)
	}
	h.dl.mu.Lock()
	h.dl.status[blocked] = media.Status{State: "downloading", Progress: 0.4}
	h.dl.mu.Unlock()
	if p := h.state().Queue[0].Play; p.Mode != "wait" || p.Progress != 0.4 {
		t.Errorf("downloading: play = %+v", p)
	}
	file := filepath.Join(t.TempDir(), blocked+".mp4")
	os.WriteFile(file, []byte("video bytes"), 0o644)
	h.dl.mu.Lock()
	h.dl.status[blocked] = media.Status{State: "ready"}
	h.dl.files[blocked] = file
	h.dl.mu.Unlock()
	if p := h.state().Queue[0].Play; p.Mode != "file" || p.URL != "/media/yt/"+blocked {
		t.Errorf("ready: play = %+v", p)
	}
	resp, data := h.do("GET", "/media/yt/"+blocked, "", "Range", "bytes=0-4")
	if resp.StatusCode != http.StatusPartialContent || string(data) != "video" {
		t.Errorf("range request = %d %q", resp.StatusCode, data)
	}
	if resp, _ := h.do("GET", "/media/yt/"+embedOK, ""); resp.StatusCode != 404 {
		t.Errorf("a video not downloaded = %d", resp.StatusCode)
	}
	// Always download: now the embeddable one is wanted too.
	h.do("POST", "/api/settings", `{"alwaysDownload":true,"upNextSeconds":4}`)
	if got := h.dl.lastWanted(); len(got) != 2 || got[0] != embedOK {
		t.Errorf("wanted with alwaysDownload = %v", got)
	}
	if s := h.state(); s.Current.Play.Mode != "wait" || s.Settings.UpNextSeconds != 4 {
		t.Errorf("current play = %+v, settings %+v", s.Current.Play, s.Settings)
	}
}

func TestEmbedFailureSwitchesToADownload(t *testing.T) {
	h := newHarness(t, true)
	a := h.add(embedOK, "a").Entry
	if resp, _ := h.do("POST", "/api/player/embed-failed", `{"qid":"someone-else","code":150}`); resp.StatusCode != 404 {
		t.Errorf("embed-failed for an entry not playing = %d", resp.StatusCode)
	}
	if resp, _ := h.do("POST", "/api/player/embed-failed", `{"qid":"`+a.QID+`","code":150}`); resp.StatusCode != 204 {
		t.Fatalf("embed-failed = %d", resp.StatusCode)
	}
	if got := h.dl.lastWanted(); len(got) != 1 || got[0] != embedOK {
		t.Errorf("wanted = %v", got)
	}
	if p := h.state().Current.Play; p.Mode != "wait" {
		t.Errorf("play = %+v, want wait", p)
	}
	if h.yt.Embeddable(context.Background(), []string{embedOK})[embedOK] {
		t.Error("the search side still thinks it embeds")
	}
}

func TestWithoutYtdlpABlockedSongIsAnError(t *testing.T) {
	h := newHarness(t, false)
	a := h.add(embedOK, "a").Entry
	h.do("POST", "/api/player/embed-failed", `{"qid":"`+a.QID+`","code":150}`)
	if p := h.state().Current.Play; p.Mode != "error" {
		t.Errorf("play = %+v, want error", p)
	}
	if resp, _ := h.do("POST", "/api/settings", `{"alwaysDownload":true}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("alwaysDownload without yt-dlp = %d", resp.StatusCode)
	}
}

func TestCommandsAndStatusReachTheEventStream(t *testing.T) {
	h := newHarness(t, false)
	h.add(embedOK, "a")
	req, _ := http.NewRequest("GET", h.web.URL+"/api/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		name := ""
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				events <- name + " " + strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	expect := func(prefix string) string {
		t.Helper()
		select {
		case e := <-events:
			if !strings.HasPrefix(e, prefix) {
				t.Fatalf("event %q, want %s", e, prefix)
			}
			return e
		case <-ctx.Done():
			t.Fatalf("no %s event", prefix)
		}
		return ""
	}
	expect("state ")
	if resp, _ := h.do("POST", "/api/player/command", `{"action":"volume","value":9999}`); resp.StatusCode != 200 {
		t.Fatalf("command = %d", resp.StatusCode)
	}
	if e := expect("command "); !strings.Contains(e, `"value":600`) {
		t.Errorf("command event %q, want the value clamped", e)
	}
	if resp, _ := h.do("POST", "/api/player/command", `{"action":"self-destruct"}`); resp.StatusCode != 400 {
		t.Errorf("unknown action = %d", resp.StatusCode)
	}
	if resp, _ := h.do("POST", "/api/player/status", `{"qid":"x","state":"playing","position":12,"duration":200,"volume":180,"mic":{"on":true,"volume":900,"echo":-5,"reverb":40,"latencyMs":21}}`); resp.StatusCode != 204 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if e := expect("player "); !strings.Contains(e, `"volume":100`) || !strings.Contains(e, `"mic":{"on":true,"volume":200,"echo":0,"reverb":40`) {
		t.Errorf("player event %q, want volume and mic clamped", e)
	}
	if resp, _ := h.do("POST", "/api/player/command", `{"action":"mic-echo","value":10}`); resp.StatusCode != 200 {
		t.Errorf("mic command = %d", resp.StatusCode)
	}
	expect("command ")
	if s := h.state(); s.Player == nil || s.Player.Position != 12 {
		t.Errorf("snapshot player = %+v", s.Player)
	}
	h.add(embedOK, "b")
	expect("state ")
}

func TestQRCodeAndInfo(t *testing.T) {
	h := newHarness(t, false)
	resp, body := h.do("GET", "/api/qr?text=http%3A%2F%2F192.168.1.5%3A8765", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(string(body), "<svg") {
		t.Errorf("qr = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := h.do("GET", "/api/qr", ""); resp.StatusCode != 400 {
		t.Errorf("qr without text = %d", resp.StatusCode)
	}
	_, body = h.do("GET", "/api/info", "")
	if !strings.Contains(string(body), "192.168.1.5") || !strings.Contains(string(body), `"version":"test"`) {
		t.Errorf("info = %s", body)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(1, 3)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow() {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d at once, want the burst of 3", allowed)
	}
}

func TestClean(t *testing.T) {
	if got := clean("  héllo\x00 wörld\n ", 7); got != "héllo w" {
		t.Errorf("clean = %q", got)
	}
}

func TestAddressRankPrefersTheLAN(t *testing.T) {
	order := []string{"192.168.1.2", "10.0.0.4", "172.17.0.1", "100.69.74.46", "8.8.8.8"}
	for i := 1; i < len(order); i++ {
		a, b := addressRank(parseIP(order[i-1])), addressRank(parseIP(order[i]))
		if a >= b {
			t.Errorf("%s (rank %d) should come before %s (rank %d)", order[i-1], a, order[i], b)
		}
	}
}

func parseIP(s string) []byte { return net.ParseIP(s).To4() }

func TestWantedDownloadsWhatWontEmbedAndKeepsEverythingStillToPlay(t *testing.T) {
	entry := func(id string, embeds bool) room.Entry {
		return room.Entry{Song: room.Song{VideoID: id, Embeddable: embeds}}
	}
	cur := entry("playing0001", true)
	st := room.State{Current: &cur, Queue: []room.Entry{entry("blocked0001", false), entry("embeds00001", true), {}}}
	download, keep := wanted(st)
	if fmt.Sprint(download) != "[blocked0001]" {
		t.Errorf("download = %v", download)
	}
	// The song playing embeds, but it may be playing from a copy made earlier: keep that copy.
	if fmt.Sprint(keep) != "[playing0001 blocked0001 embeds00001]" {
		t.Errorf("keep = %v", keep)
	}
	st.Settings.AlwaysDownload = true
	if download, _ := wanted(st); fmt.Sprint(download) != "[playing0001 blocked0001 embeds00001]" {
		t.Errorf("download with alwaysDownload = %v", download)
	}
}

type fakeLyrics struct{}

func (fakeLyrics) Match(ctx context.Context, title string, duration float64) (lyrics.Track, bool, error) {
	if strings.Contains(title, "Bohemian") {
		return lyrics.Track{ID: 7, Track: "Bohemian Rhapsody", Artist: "Queen", Lyrics: "Is this the real life?\nIs this just fantasy?"}, true, nil
	}
	return lyrics.Track{}, false, nil
}

func (fakeLyrics) Get(ctx context.Context, id int) (lyrics.Track, error) {
	if id == 42 {
		return lyrics.Track{ID: 42, Track: "Bohemian Rhapsody (Live)", Artist: "Queen", Lyrics: "Mama"}, nil
	}
	return lyrics.Track{}, lyrics.ErrNotFound
}

func (fakeLyrics) Search(ctx context.Context, q string) ([]lyrics.Track, error) {
	return []lyrics.Track{
		{ID: 42, Track: "Bohemian Rhapsody (Live)", Artist: "Queen", Lyrics: "\nMama\njust killed a man\nput a gun"},
		{ID: 43, Track: "Bohemian Rhapsody", Artist: "Queen", Instrumental: true},
	}, nil
}

func TestLyrics(t *testing.T) {
	h := newHarness(t, false)
	resp, data := h.do("POST", "/api/queue", `{"song":{"source":"youtube","videoId":"embedOK0001","title":"Queen - Bohemian Rhapsody (Karaoke)","embeddable":true},"singer":"an"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("enqueue = %d %s", resp.StatusCode, data)
	}
	var added enqueueResponse
	json.Unmarshal(data, &added)
	qid := added.Entry.QID
	get := func() lyricsAnswer {
		t.Helper()
		resp, data := h.do("GET", "/api/lyrics?qid="+qid, "")
		if resp.StatusCode != 200 {
			t.Fatalf("lyrics = %d %s", resp.StatusCode, data)
		}
		var a lyricsAnswer
		json.Unmarshal(data, &a)
		return a
	}
	if a := get(); a.Track == nil || a.Track.ID != 7 || a.Chosen || a.Query != "queen bohemian rhapsody" {
		t.Errorf("automatic match = %+v", a)
	}
	if resp, _ := h.do("POST", "/api/queue/"+qid+"/lyrics", `{"id":42}`); resp.StatusCode != 204 {
		t.Errorf("choose = %d", resp.StatusCode)
	}
	if a := get(); a.Track == nil || a.Track.ID != 42 || !a.Chosen {
		t.Errorf("chosen = %+v", a)
	}
	if h.state().Current.Song.LyricsID != 42 {
		t.Error("the choice is not in the room's state, so the TV would not hear of it")
	}
	h.do("POST", "/api/queue/"+qid+"/lyrics", `{"id":-1}`)
	if a := get(); !a.Off || a.Track != nil {
		t.Errorf("turned off = %+v", a)
	}
	for body, want := range map[string]int{`{}`: 400, `{"id":-5}`: 400} {
		if resp, _ := h.do("POST", "/api/queue/"+qid+"/lyrics", body); resp.StatusCode != want {
			t.Errorf("POST %s = %d, want %d", body, resp.StatusCode, want)
		}
	}
	if resp, _ := h.do("POST", "/api/queue/nope/lyrics", `{"id":1}`); resp.StatusCode != 404 {
		t.Errorf("unknown entry = %d", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/api/lyrics?qid=nope", ""); resp.StatusCode != 404 {
		t.Errorf("lyrics for an unknown entry = %d", resp.StatusCode)
	}
	resp, data = h.do("GET", "/api/lyrics/search?q=bohemian", "")
	var found struct{ Results []lyricsCandidate }
	json.Unmarshal(data, &found)
	if resp.StatusCode != 200 || len(found.Results) != 1 || found.Results[0].Preview != "Mama / just killed a man" || found.Results[0].Lines != 4 {
		t.Errorf("search = %d %+v", resp.StatusCode, found.Results)
	}
	if !h.state().Features.Lyrics {
		t.Error("features.lyrics is off")
	}
}

func TestAKeyChangeNeedsTheDownload(t *testing.T) {
	h := newHarness(t, true)
	cur := h.add(embedOK, "an").Entry
	if got := h.state().Current.Play.Mode; got != "embed" {
		t.Fatalf("mode = %s", got)
	}
	if resp, _ := h.do("POST", "/api/queue/"+cur.QID+"/key", `{"key":2}`); resp.StatusCode != 204 {
		t.Fatalf("set key = %d", resp.StatusCode)
	}
	st := h.state()
	if st.Current.Key != 2 || st.Current.Play.Mode != "wait" {
		t.Errorf("after a key change: key %d, mode %s; want 2 and wait for the copy", st.Current.Key, st.Current.Play.Mode)
	}
	if got := h.dl.lastWanted(); len(got) != 1 || got[0] != embedOK {
		t.Errorf("wanted = %v, want the song in its new key downloaded", got)
	}
	h.dl.setReady(embedOK)
	h.srv.DownloadChanged()
	if got := h.state().Current.Play; got.Mode != "file" {
		t.Errorf("with the copy: %+v", got)
	}
	if resp, _ := h.do("POST", "/api/queue/"+cur.QID+"/key", `{}`); resp.StatusCode != 400 {
		t.Errorf("no key = %d", resp.StatusCode)
	}
}

func (f *fakeDownloader) setReady(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = media.Status{State: "ready"}
	f.files[id] = "/nonexistent/" + id + ".mp4"
}
