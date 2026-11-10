package room

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRoom(t *testing.T) (*Room, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	r, err := OpenRoom(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return r, path
}

func song(title string) Song {
	return Song{Source: "youtube", Title: title, VideoID: "abcdefghij" + title[:1], Embeddable: true}
}

func titles(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Song.Title
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAddPlaysAtOnceWhenIdleThenQueues(t *testing.T) {
	r, _ := testRoom(t)
	first, _ := r.Add(song("A"), "an", false)
	r.Add(song("B"), "bo", false)
	r.Add(song("C"), "cy", true)
	s := r.State()
	if s.Current == nil || s.Current.QID != first.QID {
		t.Fatalf("current = %+v, want the first song", s.Current)
	}
	if s.Current.PlayedAt.IsZero() {
		t.Error("the song playing has no PlayedAt")
	}
	if got := titles(s.Queue); !equalStrings(got, []string{"C", "B"}) {
		t.Errorf("queue = %v, want [C B] (C was added as next)", got)
	}
}

func TestAdvanceOnlyFromTheEntryPlaying(t *testing.T) {
	r, _ := testRoom(t)
	a, _ := r.Add(song("A"), "", false)
	r.Add(song("B"), "", false)
	if ok, _ := r.Advance("not-playing"); ok {
		t.Fatal("advanced from an entry that is not playing")
	}
	if ok, _ := r.Advance(a.QID); !ok {
		t.Fatal("did not advance from the entry playing")
	}
	// A second TV reporting the same end must not skip B.
	if ok, _ := r.Advance(a.QID); ok {
		t.Fatal("advanced twice from the same entry")
	}
	s := r.State()
	if s.Current == nil || s.Current.Song.Title != "B" {
		t.Fatalf("current = %+v, want B", s.Current)
	}
	if got := titles(s.History); !equalStrings(got, []string{"A"}) {
		t.Errorf("history = %v, want [A]", got)
	}
}

func TestAdvancePastTheLastSongLeavesTheRoomIdle(t *testing.T) {
	r, _ := testRoom(t)
	a, _ := r.Add(song("A"), "", false)
	r.Advance(a.QID)
	if s := r.State(); s.Current != nil || len(s.Queue) != 0 {
		t.Fatalf("state = %+v, want idle", s)
	}
}

func TestRemoveUpcomingAndPlaying(t *testing.T) {
	r, _ := testRoom(t)
	a, _ := r.Add(song("A"), "", false)
	b, _ := r.Add(song("B"), "", false)
	r.Add(song("C"), "", false)
	if err := r.Remove(b.QID); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(a.QID); err != nil { // the one playing: skips it
		t.Fatal(err)
	}
	s := r.State()
	if s.Current == nil || s.Current.Song.Title != "C" || len(s.Queue) != 0 {
		t.Fatalf("state = current %+v queue %v, want C playing and nothing after", s.Current, titles(s.Queue))
	}
	if err := r.Remove("missing"); err != ErrNotFound {
		t.Errorf("Remove(missing) = %v, want ErrNotFound", err)
	}
}

func TestMove(t *testing.T) {
	r, _ := testRoom(t)
	r.Add(song("A"), "", false)
	r.Add(song("B"), "", false)
	r.Add(song("C"), "", false)
	d, _ := r.Add(song("D"), "", false)
	if err := r.Move(d.QID, 0); err != nil {
		t.Fatal(err)
	}
	if got := titles(r.State().Queue); !equalStrings(got, []string{"D", "B", "C"}) {
		t.Fatalf("queue = %v, want [D B C]", got)
	}
	if err := r.Move(d.QID, 2); err != nil {
		t.Fatal(err)
	}
	if got := titles(r.State().Queue); !equalStrings(got, []string{"B", "C", "D"}) {
		t.Fatalf("queue = %v, want [B C D]", got)
	}
	if err := r.Move(d.QID, 3); err != ErrBadMove {
		t.Errorf("Move past the end = %v, want ErrBadMove", err)
	}
}

func TestStateIsACopy(t *testing.T) {
	r, _ := testRoom(t)
	r.Add(song("A"), "", false)
	r.Add(song("B"), "", false)
	s := r.State()
	s.Queue[0].Singer = "changed"
	s.Current.Singer = "changed"
	if again := r.State(); again.Queue[0].Singer == "changed" || again.Current.Singer == "changed" {
		t.Fatal("changing a State() copy changed the room")
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	r, path := testRoom(t)
	r.Add(song("A"), "an", false)
	r.Add(song("B"), "bo", false)
	r.SetSettings(Settings{AlwaysDownload: true, UpNextSeconds: 99})
	again, err := OpenRoom(path)
	if err != nil {
		t.Fatal(err)
	}
	s := again.State()
	if s.Current == nil || s.Current.Song.Title != "A" || s.Current.Singer != "an" {
		t.Fatalf("current after reopen = %+v", s.Current)
	}
	if got := titles(s.Queue); !equalStrings(got, []string{"B"}) {
		t.Errorf("queue after reopen = %v", got)
	}
	if !s.Settings.AlwaysDownload || s.Settings.UpNextSeconds != maxUpNextSecond {
		t.Errorf("settings after reopen = %+v, want alwaysDownload and the up-next cap", s.Settings)
	}
}

func TestOnChangeAndVersion(t *testing.T) {
	r, _ := testRoom(t)
	calls := 0
	r.OnChange(func() { calls++ })
	a, _ := r.Add(song("A"), "", false)
	r.Advance("stale") // changes nothing
	r.Clear()          // nothing to clear
	r.Advance(a.QID)
	if calls != 2 {
		t.Errorf("onChange ran %d times, want 2", calls)
	}
	if v := r.State().Version; v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
}

func TestMarkNotEmbeddable(t *testing.T) {
	r, _ := testRoom(t)
	a := song("A")
	r.Add(a, "", false)
	r.Add(a, "", false)
	r.Add(song("B"), "", false)
	if err := r.MarkNotEmbeddable(a.VideoID); err != nil {
		t.Fatal(err)
	}
	s := r.State()
	if s.Current.Song.Embeddable || s.Queue[0].Song.Embeddable {
		t.Error("entries with the video still say embeddable")
	}
	if !s.Queue[1].Song.Embeddable {
		t.Error("an entry with another video lost embeddable")
	}
	v := s.Version
	r.MarkNotEmbeddable(a.VideoID)
	if r.State().Version != v {
		t.Error("marking again changed the version")
	}
}

func TestAFailedSaveKeepsTheChangeAndReportsNoError(t *testing.T) {
	r, path := testRoom(t)
	notified := 0
	r.OnChange(func() { notified++ })
	os.RemoveAll(filepath.Dir(path)) // nowhere left to save
	if _, err := r.Add(song("A"), "an", false); err != nil {
		t.Fatalf("Add = %v; a failed save must not fail the request (the phone would send it again)", err)
	}
	if s := r.State(); s.Current == nil || s.Current.Song.Title != "A" || notified != 1 {
		t.Errorf("current = %+v, notified %d times; want the change kept and announced", s.Current, notified)
	}
}
