package server

import (
	"net/http"
	"sync"
	"time"
)

// hub fans server-sent events out to every open page.
type hub struct {
	mu      sync.Mutex
	clients map[chan event]struct{}
}

type event struct {
	name string
	data []byte
}

const (
	clientBuffer = 32
	pingEvery    = 20 * time.Second
)

func newHub() *hub { return &hub{clients: map[chan event]struct{}{}} }

func (h *hub) subscribe() chan event {
	ch := make(chan event, clientBuffer)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan event) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// publish never blocks. A page too slow to keep up is dropped; its EventSource reconnects
// and starts again from a fresh state, which beats it showing a stale one.
func (h *hub) publish(name string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- event{name, data}:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// serveEvents streams events to one page, starting with first (the current state).
func (h *hub) serveEvents(w http.ResponseWriter, r *http.Request, first []event) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	ch := h.subscribe()
	defer h.unsubscribe(ch)
	w.WriteHeader(http.StatusOK)
	for _, e := range first {
		writeEvent(w, e)
	}
	flusher.Flush()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			writeEvent(w, e)
			flusher.Flush()
		case <-ping.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, e event) {
	w.Write([]byte("event: " + e.name + "\ndata: "))
	w.Write(e.data) // JSON from encoding/json holds no raw newline
	w.Write([]byte("\n\n"))
}
