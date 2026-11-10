// Package server is the karaoke HTTP server: the phone remote and the TV player pages, the
// JSON API behind them, and the live event stream that keeps every open page in step.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vantuan5644/karaoke/internal/media"
	"github.com/vantuan5644/karaoke/internal/room"
	"github.com/vantuan5644/karaoke/internal/youtube"
)

// Downloader is the part of media.Downloader the server uses.
type Downloader interface {
	Status(id string) media.Status
	Path(id string) (string, bool)
	Want(ids []string)
}

// Searcher is the part of youtube.Client the server uses.
type Searcher interface {
	Search(ctx context.Context, query, more string) (youtube.Page, error)
	Embeddable(ctx context.Context, ids []string) map[string]bool
	MarkNotEmbeddable(id string)
}

// Config wires a Server. Downloader is nil when yt-dlp is not available.
type Config struct {
	Room       *room.Room
	YouTube    Searcher
	Downloader Downloader
	Web        fs.FS    // index.html, tv.html and static/
	URLs       []string // addresses phones can reach this server at, best first
	Version    string
}

// Server serves one room.
type Server struct {
	cfg     Config
	hub     *hub
	limiter *limiter

	mu       sync.Mutex
	status   PlayerStatus
	statusAt time.Time
}

// New returns a Server and hooks it to the room's changes.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, hub: newHub(), limiter: newLimiter(4, 12)}
	cfg.Room.OnChange(s.roomChanged)
	s.roomChanged()
	return s
}

// DownloadChanged is for the downloader's OnChange: progress reaches every page.
func (s *Server) DownloadChanged() { s.publishState() }

func (s *Server) roomChanged() {
	if s.cfg.Downloader != nil {
		s.cfg.Downloader.Want(wanted(s.cfg.Room.State()))
	}
	s.publishState()
}

func (s *Server) publishState() {
	data, err := json.Marshal(s.snapshot())
	if err != nil {
		log.Printf("state: %v", err)
		return
	}
	s.hub.publish("state", data)
}

// Handler returns the server's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page("index.html"))
	mux.HandleFunc("GET /tv", s.page("tv.html"))
	mux.Handle("GET /static/", s.static())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })

	mux.HandleFunc("GET /api/state", s.getState)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/qr", s.qr)
	mux.HandleFunc("GET /api/search", s.search)

	mux.HandleFunc("POST /api/queue", s.enqueue)
	mux.HandleFunc("DELETE /api/queue/{qid}", s.dequeue)
	mux.HandleFunc("POST /api/queue/{qid}/move", s.move)
	mux.HandleFunc("POST /api/queue/clear", s.clearQueue)
	mux.HandleFunc("POST /api/player/next", s.next)
	mux.HandleFunc("POST /api/player/command", s.command)
	mux.HandleFunc("POST /api/player/status", s.playerStatus)
	mux.HandleFunc("POST /api/player/embed-failed", s.embedFailed)
	mux.HandleFunc("POST /api/settings", s.setSettings)

	mux.HandleFunc("GET /media/yt/{id}", s.youtubeFile)
	return securityHeaders(sameOriginWrites(mux))
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(s.cfg.Web, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	}
}

func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.cfg.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// sameOriginWrites refuses changes sent from another site's page. Phones on the party's
// network carry no credentials a forged request could borrow, but a page elsewhere could
// still skip or clear the queue through the visitor's browser.
func sameOriginWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "send JSON")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval' https://www.youtube.com https://s.ytimg.com",
		"frame-src https://www.youtube.com https://www.youtube-nocookie.com",
		"img-src 'self' data: https://i.ytimg.com",
		"media-src 'self' blob:",
		"connect-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"worker-src 'self' blob:",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		// YouTube's embedded player refuses to start without knowing the embedding origin.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "microphone=(self), autoplay=(self \"https://www.youtube.com\")")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		} else {
			writeError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		}
		return false
	}
	return true
}
