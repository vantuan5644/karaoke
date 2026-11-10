package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vantuan5644/karaoke/internal/voice"
)

// Voice is the low-latency mic on this computer (voice.Engine): the singer's voice goes
// from the mic through PipeWire and back out in a few milliseconds, where a browser takes
// sixty or seventy. Only a TV page on this same computer can use it, since that is where
// the mic is plugged in.
type Voice interface {
	Set(voice.Settings) voice.Status
	Status() voice.Status
	Devices(ctx context.Context) ([]voice.Device, error)
	Measure(ctx context.Context) (float64, error)
	Off() // off, keeping the settings
}

// micIdle is how long the mic stays on after the TV page stops asking about it: a TV page
// closed with the mic on must not leave it live.
const micIdle = 20 * time.Second

// MicInfo is GET /api/mic.
type MicInfo struct {
	Native bool          `json:"native"` // this server has the low-latency mic
	Here   bool          `json:"here"`   // and the page asking is on this computer, so it can use it
	Status *voice.Status `json:"status,omitempty"`
}

func (s *Server) micInfo(w http.ResponseWriter, r *http.Request) {
	info := MicInfo{Native: s.cfg.Voice != nil, Here: fromThisComputer(r)}
	if info.Native && info.Here {
		st := s.cfg.Voice.Status()
		info.Status = &st
		// The TV page's poll keeps the mic on. Another site's page can make this request
		// (an image, say), but not keep somebody's mic alive with it.
		if site := r.Header.Get("Sec-Fetch-Site"); site == "" || site == "same-origin" || site == "none" {
			s.micAlive()
		}
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) setMic(w http.ResponseWriter, r *http.Request) {
	if !s.micAllowed(w, r) {
		return
	}
	settings := voice.Defaults // what a page leaves out (an older page has no Speed) is the default
	if !readJSON(w, r, &settings) {
		return
	}
	s.micMu.Lock()
	s.micAlive()
	st := s.cfg.Voice.Set(settings)
	s.micMu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) micDevices(w http.ResponseWriter, r *http.Request) {
	if !s.micAllowed(w, r) {
		return
	}
	list, err := s.cfg.Voice.Devices(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if list == nil {
		list = []voice.Device{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

// measureMic times a click from the speakers back into the mic; latencyMs is 0 when none
// came back.
func (s *Server) measureMic(w http.ResponseWriter, r *http.Request) {
	if !s.micAllowed(w, r) {
		return
	}
	s.micAlive()
	ms, err := s.cfg.Voice.Measure(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.micAlive()
	writeJSON(w, http.StatusOK, map[string]float64{"latencyMs": ms})
}

func (s *Server) micAllowed(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case s.cfg.Voice == nil:
		writeError(w, http.StatusNotFound, "this server has no low-latency mic (it needs PipeWire)")
		return false
	case !fromThisComputer(r):
		// Phones change the mic through the TV page, which owns it.
		writeError(w, http.StatusForbidden, "only a TV page on the server's own computer can use its mic")
		return false
	}
	return true
}

// micAlive pushes back the moment an unattended mic turns itself off.
func (s *Server) micAlive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.micLast = time.Now()
	if s.micTimer == nil {
		s.micTimer = time.AfterFunc(s.micIdleAfter(), s.micUnattended)
		return
	}
	s.micTimer.Reset(s.micIdleAfter())
}

func (s *Server) micIdleAfter() time.Duration {
	if s.cfg.MicIdle > 0 {
		return s.cfg.MicIdle
	}
	return micIdle
}

func (s *Server) micUnattended() {
	s.micMu.Lock()
	defer s.micMu.Unlock()
	s.mu.Lock()
	wait := s.micIdleAfter() - time.Since(s.micLast)
	if wait > 0 { // asked again while this timer was firing
		s.micTimer.Reset(wait)
	}
	s.mu.Unlock()
	if wait <= 0 {
		s.cfg.Voice.Off()
	}
}

// fromThisComputer says whether a request was made on the server's own computer: it came
// from one of this computer's addresses, and the page asked for this computer by a name of
// its own (localhost, an address of its own, or its host name). A request a proxy here
// passed on (tailscale serve, a reverse proxy) is not from here: it came from elsewhere.
// That is what a proxy's headers say, and what a forwarder that adds none (ssh -R, socat)
// still gives away in the host name the far browser asked for. A forwarder that also
// rewrites that name to this computer's cannot be told apart, but even then no sound
// leaves this computer: the mic only plays through its own speakers.
func fromThisComputer(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host", "Tailscale-User-Login"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ownAddress(ip) && namesThisComputer(r.Host)
}

func ownAddress(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// namesThisComputer says whether a Host header is a name for this computer.
func namesThisComputer(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ownAddress(ip)
	}
	name, err := os.Hostname()
	name = strings.ToLower(name)
	return err == nil && name != "" && (host == name || host == name+".local")
}
