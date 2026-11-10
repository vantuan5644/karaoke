package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseName(t *testing.T) {
	for _, tc := range []struct{ in, artist, title string }{
		{"Queen - Bohemian Rhapsody", "Queen", "Bohemian Rhapsody"},
		{"SC8125-01 - Queen - Bohemian Rhapsody", "Queen", "Bohemian Rhapsody"},
		{"PHM0705-03 - Adele - Hello", "Adele", "Hello"},
		{"Prince - 1999", "Prince", "1999"},
		{"99 Luftballons", "", "99 Luftballons"},
		{"01. Abba - Dancing Queen", "Abba", "Dancing Queen"},
		{"07_Toto_-_Africa", "Toto", "Africa"},
		{"Sơn Tùng M-TP - Nắng Ấm Xa Dần", "Sơn Tùng M-TP", "Nắng Ấm Xa Dần"},
		{"Bon Jovi – Livin' on a Prayer", "Bon Jovi", "Livin' on a Prayer"},
		{"Artist - Song - Live", "Artist", "Song - Live"},
		{"Hello", "", "Hello"},
	} {
		artist, title := parseName(tc.in)
		if artist != tc.artist || title != tc.title {
			t.Errorf("parseName(%q) = %q, %q; want %q, %q", tc.in, artist, title, tc.artist, tc.title)
		}
	}
}

// folder makes a library folder holding these files (empty, apart from their names).
func folder(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scanned(t *testing.T, root string) *Library {
	t.Helper()
	l, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Scan(); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestScanFindsEachKindOfSong(t *testing.T) {
	root := folder(t,
		"SC8125/SC8125-01 - Queen - Bohemian Rhapsody.mp3",
		"SC8125/SC8125-01 - Queen - Bohemian Rhapsody.CDG", // the case of an extension varies
		"Abba - Dancing Queen.mp4",
		"Việt/Sơn Tùng M-TP - Nắng Ấm Xa Dần.mkv",
		"Prince - 1999.mp3",
		"lonely graphics.cdg", // no audio: not a song
		"notes.txt",
		".hidden/Secret - Song.mp4",
		"@eaDir/Abba - Dancing Queen.mp4",       // a NAS thumbnail folder
		"Both - Kinds.mp4", "Both - Kinds.webm", // one song, not two
	)
	l := scanned(t, root)
	if l.Len() != 5 {
		t.Fatalf("found %d songs, want 5: %+v", l.Len(), l.sorted)
	}
	byTitle := map[string]Item{}
	for _, it := range l.sorted {
		byTitle[it.Title] = it
	}
	cdg := byTitle["Bohemian Rhapsody"]
	if cdg.Format != "cdg" || cdg.Artist != "Queen" || cdg.Where != "SC8125" || filepath.Ext(cdg.CDG) != ".CDG" {
		t.Errorf("MP3+G = %+v", cdg)
	}
	if it := byTitle["Dancing Queen"]; it.Format != "video" || it.Where != "" {
		t.Errorf("video = %+v", it)
	}
	if it := byTitle["1999"]; it.Format != "audio" || it.Artist != "Prince" {
		t.Errorf("audio = %+v", it)
	}
	if it := byTitle["Kinds"]; filepath.Ext(it.Media) != ".mp4" {
		t.Errorf("mp4 should win over webm: %s", it.Media)
	}

	// IDs stay the same from one scan to the next, so a saved queue still finds its songs.
	again := scanned(t, root)
	if _, ok := again.Get(cdg.ID); !ok {
		t.Error("the ID changed between scans")
	}
}

func TestSearch(t *testing.T) {
	l := scanned(t, folder(t,
		"SC8125-01 - Queen - Bohemian Rhapsody.mp3", "SC8125-01 - Queen - Bohemian Rhapsody.cdg",
		"Abba - Dancing Queen.mp4",
		"Việt/Sơn Tùng M-TP - Nắng Ấm Xa Dần.mkv",
		"Queen - Bohemian Rhapsody (Live).mp4",
	))
	titles := func(q string) []string {
		var out []string
		for _, it := range l.Search(q, 10) {
			out = append(out, it.Title)
		}
		return out
	}
	if got := titles("boh rhap"); len(got) != 2 {
		t.Errorf("boh rhap = %v, want both Bohemian Rhapsodys", got)
	}
	if got := titles("nang am"); len(got) != 1 || got[0] != "Nắng Ấm Xa Dần" {
		t.Errorf("nang am = %v", got)
	}
	if got := titles("Nắng ấm"); len(got) != 1 {
		t.Errorf("accented query = %v", got)
	}
	if got := titles("viet"); len(got) != 1 {
		t.Errorf("the folder name should count: %v", got)
	}
	if got := titles("dancing queen"); len(got) == 0 || got[0] != "Dancing Queen" {
		t.Errorf("dancing queen = %v, want Dancing Queen first", got)
	}
	if got := titles("queen hello"); len(got) != 0 {
		t.Errorf("every word must match: %v", got)
	}
	if got := titles("  "); got != nil {
		t.Errorf("empty query = %v", got)
	}
	if got := l.Search("queen", 1); len(got) != 1 {
		t.Errorf("limit: %d results", len(got))
	}
}

func TestDurationOutlivesARescan(t *testing.T) {
	root := folder(t, "Abba - Dancing Queen.mp4")
	l := scanned(t, root)
	id := l.sorted[0].ID
	l.SetDuration(id, 231)
	l.Scan()
	if it, _ := l.Get(id); it.Duration != 231 {
		t.Errorf("duration after a rescan = %v", it.Duration)
	}
	if got := l.Search("abba", 1); len(got) != 1 || got[0].Duration != 231 {
		t.Errorf("search shows %+v", got)
	}
}

func TestNewRefusesWhatIsNotAFolder(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("no folders: no error")
	}
	if _, err := New([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Error("missing folder: no error")
	}
	f := filepath.Join(folder(t, "a.mp4"), "a.mp4")
	if _, err := New([]string{f}); err == nil {
		t.Error("a file: no error")
	}
}
