package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func fixture(t *testing.T) any {
	t.Helper()
	data, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParsePageReadsARealResponse(t *testing.T) {
	page := parsePage(fixture(t))
	if len(page.Videos) != 4 {
		t.Fatalf("got %d videos, want 4 (live stream, short and duplicate left out): %+v", len(page.Videos), page.Videos)
	}
	first := page.Videos[0]
	want := Video{
		ID: "9Lxm0iSnKNc", Title: "Queen - Bohemian Rhapsody (Karaoke Version)", Channel: "Sing King",
		Duration: 375, Thumb: "https://i.ytimg.com/vi/9Lxm0iSnKNc/mqdefault.jpg",
	}
	if first.ID != want.ID || first.Title != want.Title || first.Channel != want.Channel ||
		first.Duration != want.Duration || first.Thumb != want.Thumb {
		t.Errorf("first video = %+v, want %+v", first, want)
	}
	if first.Views == "" {
		t.Error("first video has no view count")
	}
	if !strings.HasPrefix(page.More, "Eq4DEhlib2hl") {
		t.Errorf("continuation = %q", page.More)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]float64{"4:05": 245, "0:59": 59, "1:02:03": 3723} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "5", "a:b", "1:2:3:4", "-1:00"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) accepted", bad)
		}
	}
}

func TestWithKaraoke(t *testing.T) {
	cases := map[string]string{
		"bohemian rhapsody":        "bohemian rhapsody karaoke",
		"Beat It":                  "Beat It karaoke",
		"Nắng ấm xa dần karaoke":   "Nắng ấm xa dần karaoke",
		"my way KARAOKE tone nam":  "my way KARAOKE tone nam",
		"let it go instrumental":   "let it go instrumental",
		"  hello  ":                "hello karaoke",
		"di nhu con gio minus one": "di nhu con gio minus one",
	}
	for in, want := range cases {
		if got := WithKaraoke(in); got != want {
			t.Errorf("WithKaraoke(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidID(t *testing.T) {
	if !ValidID("9Lxm0iSnKNc") || !ValidID("a-b_c-d_e-f") {
		t.Error("a real ID was refused")
	}
	for _, bad := range []string{"", "short", "9Lxm0iSnKNc1", "../../etc/p", "9Lxm0iSnKN!"} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
	}
}

func TestSearchSendsQueryThenContinuation(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/search" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		data, _ := os.ReadFile("testdata/search.json")
		w.Write(data)
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL
	c.Region = "VN"
	page, err := c.Search(context.Background(), "bohemian rhapsody karaoke", "")
	if err != nil || len(page.Videos) != 4 {
		t.Fatalf("Search = %d videos, %v", len(page.Videos), err)
	}
	if _, err := c.Search(context.Background(), "", page.More); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["query"] != "bohemian rhapsody karaoke" || bodies[0]["params"] != onlyVideos {
		t.Errorf("first request = %v", bodies[0])
	}
	client := bodies[0]["context"].(map[string]any)["client"].(map[string]any)
	if client["gl"] != "VN" || client["clientName"] != clientName {
		t.Errorf("client context = %v", client)
	}
	if bodies[1]["continuation"] != page.More || bodies[1]["query"] != nil {
		t.Errorf("second request = %v", bodies[1])
	}
}

func TestSearchReportsHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL
	if _, err := c.Search(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the 429", err)
	}
}

func TestEmbeddableAsksOnceAndTrustsThePlayerWhenUnsure(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		io.Copy(io.Discard, r.Body)
		switch {
		case strings.Contains(r.URL.RawQuery, "blockedVid1"):
			w.WriteHeader(http.StatusUnauthorized)
		case strings.Contains(r.URL.RawQuery, "flakyVideo1"):
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Write([]byte(`{"title":"ok"}`))
		}
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL
	ids := []string{"okVideo0001", "blockedVid1", "flakyVideo1"}
	got := c.Embeddable(context.Background(), ids)
	if !got["okVideo0001"] || got["blockedVid1"] || !got["flakyVideo1"] {
		t.Fatalf("Embeddable = %v", got)
	}
	c.Embeddable(context.Background(), ids)
	if n := asked.Load(); n != 4 { // 3, then only the flaky one again
		t.Errorf("asked YouTube %d times, want 4", n)
	}
	c.MarkNotEmbeddable("okVideo0001")
	if c.Embeddable(context.Background(), []string{"okVideo0001"})["okVideo0001"] {
		t.Error("MarkNotEmbeddable did not stick")
	}
}
