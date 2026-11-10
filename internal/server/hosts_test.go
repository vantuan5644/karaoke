package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKnownHosts(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := knownHosts([]string{"karaoke.example.com:443", " Party.Example.org "}, ok)
	for host, want := range map[string]int{
		"192.168.1.20:8765":            204,
		"[fe80::1]:8765":               204,
		"localhost:8765":               204,
		"omarchy:8765":                 204,
		"omarchy.local":                204,
		"minipc.lan:8765":              204,
		"box.home.arpa":                204,
		"tv.tail1234.ts.net":           204,
		"karaoke.example.com":          204,
		"party.example.org:8765":       204,
		"rebind.attacker.example:8765": 421,
		"karaoke.example.com.evil.io":  421,
		"":                             421,
	} {
		r := httptest.NewRequest("GET", "/api/state", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
	}
}
