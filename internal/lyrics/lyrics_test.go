package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Nắng Ấm Xa Dần":            "nang am xa dan",
		"ĐỪNG LÀM TRÁI TIM ANH ĐAU": "dung lam trai tim anh dau",
		"Céline Dion":               "celine dion",
		"Beyoncé":                   "beyonce",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParts(t *testing.T) {
	for title, want := range map[string][]string{
		"Queen - Bohemian Rhapsody (Karaoke Version)":                                              {"queen", "bohemian rhapsody"},
		"Take Me Home, Country Roads - John Denver | Karaoke Version | KaraFun":                    {"take me home country roads", "john denver"},
		"Take Me Home, Country Roads -John Denver [Acoustic Karaoke | Femaler Key]":                {"take me home country roads", "john denver"},
		"The Beatles - Yesterday - Karaoke Version from Zoom Karaoke":                              {"the beatles", "yesterday"},
		"Ed Sheeran   Shape Of You Karaoke Version":                                                {"ed sheeran shape of you"},
		"Karaoke | Em Của Ngày Hôm Qua (New Version) (Beat Chuẩn Có Bè)":                           {"em cua ngay hom qua"},
		"Nắng Ấm Xa Dần Karaoke ( Có Bè ) Sơn Tùng M-TP":                                           {"nang am xa dan son tung m tp"},
		"ĐỪNG LÀM TRÁI TIM ANH ĐAU KARAOKE (BEAT CHUẨN) TONE NAM":                                  {"dung lam trai tim anh dau"},
		"Karaoke   Full HD Nắng ấm xa dần   Sơn Tùng MTP Beat chuẩn Newtitan www tubeprojects net": {"nang am xa dan son tung mtp newtitan"},
		"(Karaoke) Tone Nữ - Em Của Ngày Hôm Qua - Sơn Tùng MTP - ecnhqkanu #ecnhqkanu":            {"em cua ngay hom qua", "son tung mtp", "ecnhqkanu"},
		"Let It Go (Karaoke) from 'Frozen'":                                                        {"let it go from frozen"},
		"Karaoke Karaoke (Official Video)":                                                         nil,
	} {
		if got := Parts(title); !reflect.DeepEqual(got, want) {
			t.Errorf("Parts(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestQueriesTryTheWholeTitleThenItsPartsThenShorter(t *testing.T) {
	got := queries([]string{"nang am xa dan son tung mtp newtitan"})
	want := []string{"nang am xa dan son tung mtp newtitan", "nang am xa dan son tung mtp", "nang am xa dan son tung", "nang am xa dan son", "nang am xa dan"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("queries = %q", got)
	}
	if got := queries([]string{"queen", "bohemian rhapsody"}); got[0] != "queen bohemian rhapsody" || got[1] != "queen" || got[2] != "bohemian rhapsody" {
		t.Errorf("queries = %q", got)
	}
}

func TestScore(t *testing.T) {
	title := tokens("queen bohemian rhapsody")
	song := Track{Track: "Bohemian Rhapsody", Artist: "Queen", Duration: 355, Lyrics: "Is this the real life?"}
	queen := Track{Track: "Queen", Artist: "Someone", Duration: 200, Lyrics: "la la"}
	remix := Track{Track: "Bohemian Rhapsody (DJ Remix Edit)", Artist: "Queen", Duration: 355, Lyrics: "Is this the real life?"}
	if s := score(song, title, 375, false); s < goodEnough {
		t.Errorf("the song scored %.2f", s)
	}
	if score(queen, title, 375, false) >= score(song, title, 375, false) {
		t.Error("a song named Queen beat Bohemian Rhapsody")
	}
	if score(remix, title, 375, false) >= score(song, title, 375, false) {
		t.Error("the remix beat the song")
	}
	instrumental := song
	instrumental.Instrumental = true
	empty := song
	empty.Lyrics = " "
	if score(instrumental, title, 375, false) != 0 || score(empty, title, 375, false) != 0 {
		t.Error("a track with no words scored")
	}
	vn := tokens("em cua ngay hom qua")
	typed := Track{Track: "Em Cua Ngay Hom Qua", Artist: "Son Tung M-TP", Lyrics: "Lieu con mai ben nhau"}
	accented := Track{Track: "Em Của Ngày Hôm Qua", Artist: "Sơn Tùng M-TP", Lyrics: "Liệu rằng chia tay"}
	if score(typed, vn, 0, true) >= score(accented, vn, 0, true) {
		t.Error("lyrics without accents beat lyrics with them, for an accented title")
	}
}

// fakeLRCLIB answers searches from a map of query to records.
type fakeLRCLIB struct {
	mu       sync.Mutex
	queries  []string
	answers  map[string][]record
	busyOnce bool
}

func (f *fakeLRCLIB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("User-Agent") != "karaoke-test" {
		http.Error(w, "say who you are", http.StatusBadRequest)
		return
	}
	if f.busyOnce {
		f.busyOnce = false
		w.Header().Set("Retry-After", "1")
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	switch {
	case r.URL.Path == "/api/search":
		q := r.URL.Query().Get("q")
		f.queries = append(f.queries, q)
		json.NewEncoder(w).Encode(append([]record{}, f.answers[q]...))
	case r.URL.Path == "/api/get/42":
		json.NewEncoder(w).Encode(record{ID: 42, TrackName: "Yesterday", ArtistName: "The Beatles", SyncedLyrics: "[00:01.50] Yesterday\n[00:05.00][00:30.00] All my troubles"})
	default:
		http.NotFound(w, r)
	}
}

func newFake(t *testing.T, answers map[string][]record) (*Client, *fakeLRCLIB) {
	f := &fakeLRCLIB{answers: answers}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New("karaoke-test")
	c.Base, c.Gap = srv.URL, time.Millisecond
	return c, f
}

func TestMatchFallsBackToShorterQueriesAndCaches(t *testing.T) {
	c, f := newFake(t, map[string][]record{
		"nang am xa dan son tung": {
			{ID: 1, TrackName: "Nắng Ấm Xa Dần (Remix)", ArtistName: "DJ", Duration: 250, PlainLyrics: "Rồi nắng"},
			{ID: 2, TrackName: "Nắng Ấm Xa Dần", ArtistName: "Sơn Tùng M-TP", Duration: 191, PlainLyrics: "Rồi nắng ấm xa dần"},
		},
	})
	title := "Karaoke Nắng Ấm Xa Dần - Sơn Tùng (Beat Chuẩn) Newtitan"
	got, ok, err := c.Match(context.Background(), title, 195)
	if err != nil || !ok || got.ID != 2 {
		t.Fatalf("Match = %+v, %v, %v", got, ok, err)
	}
	asked := len(f.queries)
	if asked < 2 || f.queries[0] != "nang am xa dan son tung newtitan" {
		t.Errorf("queries = %q", f.queries)
	}
	if _, _, err := c.Match(context.Background(), title, 195); err != nil || len(f.queries) != asked {
		t.Errorf("a second Match asked LRCLIB again (%d queries)", len(f.queries))
	}
}

func TestMatchSaysSoWhenNothingFits(t *testing.T) {
	c, _ := newFake(t, map[string][]record{
		"queen bohemian rhapsody": {{ID: 3, TrackName: "Something Else", ArtistName: "Queen", PlainLyrics: "x"}},
	})
	if _, ok, err := c.Match(context.Background(), "Queen - Bohemian Rhapsody (Karaoke)", 360); ok || err != nil {
		t.Errorf("Match = %v, %v; want no match and no error", ok, err)
	}
}

func TestGetStripsTimestampsAndWaitsOutA429(t *testing.T) {
	c, f := newFake(t, nil)
	f.busyOnce = true
	start := time.Now()
	got, err := c.Get(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lyrics != "Yesterday\nAll my troubles" {
		t.Errorf("lyrics = %q", got.Lyrics)
	}
	if time.Since(start) < time.Second {
		t.Error("did not wait out Retry-After")
	}
	if _, err := c.Get(context.Background(), 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing record: %v", err)
	}
}

func TestRequestsAreSpacedOut(t *testing.T) {
	c, _ := newFake(t, nil)
	c.Gap = 100 * time.Millisecond
	start := time.Now()
	for _, q := range []string{"a", "b", "c"} {
		c.Search(context.Background(), q)
	}
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("three requests took %v, want at least two gaps", took)
	}
	if got, _ := c.Search(context.Background(), strings.Repeat(" ", 3)); got != nil {
		t.Error("an empty query reached LRCLIB")
	}
}
