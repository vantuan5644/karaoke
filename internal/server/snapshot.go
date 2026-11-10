package server

import "github.com/vantuan5644/karaoke/internal/room"

// Snapshot is what every page renders from: the room plus how the TV should play each entry.
type Snapshot struct {
	Version  int64         `json:"version"`
	Current  *EntryView    `json:"current"`
	Queue    []EntryView   `json:"queue"`
	History  []room.Entry  `json:"history"`
	Settings room.Settings `json:"settings"`
	Features Features      `json:"features"`
	Player   *PlayerStatus `json:"player,omitempty"`
}

// EntryView is a queue entry and the way to play it.
type EntryView struct {
	room.Entry
	Play Play `json:"play"`
}

// Play tells the TV what to do with an entry.
type Play struct {
	Mode     string  `json:"mode"` // "embed" (IFrame player), "file" (our own copy), "wait" or "error"
	URL      string  `json:"url,omitempty"`
	CDG      string  `json:"cdg,omitempty"` // the graphics, for an MP3+G
	Progress float64 `json:"progress,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// Features says which optional parts this server has.
type Features struct {
	Download bool `json:"download"` // yt-dlp is there, so videos that refuse to embed still play
	Lyrics   bool `json:"lyrics"`   // lyrics can be looked up
	Library  int  `json:"library"`  // songs in folders on this computer
}

const historyShown = 30

func (s *Server) snapshot() Snapshot {
	st := s.cfg.Room.State()
	snap := Snapshot{
		Version:  st.Version,
		Settings: st.Settings,
		Features: s.features(),
		Queue:    make([]EntryView, len(st.Queue)),
		History:  st.History,
	}
	if len(snap.History) > historyShown {
		snap.History = snap.History[:historyShown]
	}
	if st.Current != nil {
		snap.Current = &EntryView{Entry: *st.Current, Play: s.playFor(*st.Current, st.Settings)}
	}
	for i, e := range st.Queue {
		snap.Queue[i] = EntryView{Entry: e, Play: s.playFor(e, st.Settings)}
	}
	if p, ok := s.player(); ok {
		snap.Player = &p
	}
	return snap
}

func (s *Server) features() Features {
	f := Features{Download: s.cfg.Downloader != nil, Lyrics: s.cfg.Lyrics != nil}
	if s.cfg.Library != nil {
		f.Library = s.cfg.Library.Len()
	}
	return f
}

// playFor picks how a song plays. A downloaded copy always wins (it starts at once and has
// no ads); otherwise YouTube's own player, unless the video refuses to embed or the room
// asked for downloads, in which case the TV waits for the copy.
func (s *Server) playFor(e room.Entry, settings room.Settings) Play {
	song := e.Song
	if song.Source == "library" {
		return s.playFromLibrary(song)
	}
	if song.VideoID == "" {
		return Play{Mode: "error", Error: "nothing to play"}
	}
	d := s.cfg.Downloader
	if d == nil {
		if song.Embeddable {
			return Play{Mode: "embed"}
		}
		return Play{Mode: "error", Error: "This video can't play embedded, and yt-dlp isn't installed to download it."}
	}
	st := d.Status(song.VideoID)
	switch st.State {
	case "ready":
		return Play{Mode: "file", URL: "/media/yt/" + song.VideoID}
	case "failed":
		if song.Embeddable {
			return Play{Mode: "embed"}
		}
		return Play{Mode: "error", Error: "Download failed: " + st.Error}
	}
	// A song in another key needs its download: YouTube's player keeps its sound to itself.
	if song.Embeddable && !settings.AlwaysDownload && e.Key == 0 {
		return Play{Mode: "embed"}
	}
	return Play{Mode: "wait", Progress: st.Progress}
}

// wanted lists, in queue order, the videos to download (those that won't embed, or all of
// them when the room asks for downloads), and the videos to keep on disk: every one still to
// play, since a song already playing from its copy must not lose it to the cache trim.
func wanted(st room.State) (download, keep []string) {
	add := func(e room.Entry) {
		if e.Song.VideoID == "" {
			return
		}
		keep = append(keep, e.Song.VideoID)
		if !e.Song.Embeddable || st.Settings.AlwaysDownload || e.Key != 0 {
			download = append(download, e.Song.VideoID)
		}
	}
	if st.Current != nil {
		add(*st.Current)
	}
	for _, e := range st.Queue {
		add(e)
	}
	return download, keep
}

// playFromLibrary plays a file from the library's folders, which needs nothing fetched.
func (s *Server) playFromLibrary(song room.Song) Play {
	if s.cfg.Library == nil {
		return Play{Mode: "error", Error: "This server's library is switched off."}
	}
	it, ok := s.cfg.Library.Get(song.LibraryID)
	if !ok {
		return Play{Mode: "error", Error: "That file is no longer in the library."}
	}
	p := Play{Mode: "file", URL: "/media/lib/" + it.ID}
	if it.Format == "cdg" {
		p.CDG = "/media/lib/" + it.ID + "/cdg"
	}
	return p
}
