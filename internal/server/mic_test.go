package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vantuan5644/karaoke/internal/voice"
)

type fakeVoice struct {
	mu sync.Mutex
	s  voice.Settings
}

func (f *fakeVoice) Set(s voice.Settings) voice.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s = voice.Normalize(s)
	return voice.Status{Settings: f.s, Running: f.s.On}
}

func (f *fakeVoice) Status() voice.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return voice.Status{Settings: f.s, Running: f.s.On}
}

func (f *fakeVoice) Devices(context.Context) ([]voice.Device, error) {
	return []voice.Device{{ID: "alsa_input.usb-mic", Name: "USB Mic", Default: true}}, nil
}

func (f *fakeVoice) Measure(context.Context) (float64, error) {
	if !f.on() {
		return 0, voice.ErrNotRunning
	}
	return 9, nil
}

func (f *fakeVoice) Off() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s.On = false
}

func (f *fakeVoice) on() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s.On
}

func TestNativeMicFromThisComputer(t *testing.T) {
	fv := &fakeVoice{}
	h := newHarnessWith(t, false, func(c *Config) { c.Voice = fv })

	// The test server is on loopback, so this request is "here".
	resp, body := h.do("GET", "/api/mic", "")
	var info MicInfo
	json.Unmarshal(body, &info)
	if resp.StatusCode != 200 || !info.Native || !info.Here || info.Status == nil {
		t.Fatalf("GET /api/mic: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do("POST", "/api/mic", `{"on":true,"volume":120,"tune":40,"warmth":300,"deviceId":"alsa_input.usb-mic"}`)
	var st voice.Status
	json.Unmarshal(body, &st)
	if resp.StatusCode != 200 || !st.Running || st.Volume != 120 || st.Tune != 40 || st.Warmth != 100 {
		t.Fatalf("POST /api/mic: %d %s", resp.StatusCode, body)
	}
	if st.Speed != voice.Defaults.Speed || st.Echo != voice.Defaults.Echo {
		t.Errorf("what a page leaves out should be the default, not 0: speed %d, echo %d", st.Speed, st.Echo)
	}
	resp, body = h.do("GET", "/api/mic/devices", "")
	if resp.StatusCode != 200 || !json.Valid(body) || len(body) < 20 {
		t.Fatalf("GET /api/mic/devices: %d %s", resp.StatusCode, body)
	}

	if resp, body := h.do("POST", "/api/mic/measure", "{}"); resp.StatusCode != 200 || string(body) != "{\"latencyMs\":9}\n" {
		t.Errorf("measure: %d %s", resp.StatusCode, body)
	}

	// Through a proxy on this computer, it came from somewhere else.
	resp, body = h.do("POST", "/api/mic", `{"on":false}`, "X-Forwarded-For", "100.64.0.9")
	if resp.StatusCode != http.StatusForbidden || !fv.on() {
		t.Errorf("a proxied request changed the mic: %d %s", resp.StatusCode, body)
	}
	_, body = h.do("GET", "/api/mic", "", "Tailscale-User-Login", "someone@example.com")
	var proxied MicInfo
	json.Unmarshal(body, &proxied)
	if proxied.Here || proxied.Status != nil {
		t.Errorf("a proxied request was told it is here: %s", body)
	}
}

func TestNoNativeMic(t *testing.T) {
	h := newHarness(t, false)
	_, body := h.do("GET", "/api/mic", "")
	var info MicInfo
	json.Unmarshal(body, &info)
	if info.Native {
		t.Errorf("no engine, but native: %s", body)
	}
	if resp, _ := h.do("POST", "/api/mic", `{"on":true}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST without an engine: %d", resp.StatusCode)
	}
}

func TestUnattendedMicTurnsOff(t *testing.T) {
	fv := &fakeVoice{}
	h := newHarnessWith(t, false, func(c *Config) { c.Voice = fv; c.MicIdle = 150 * time.Millisecond })
	h.do("POST", "/api/mic", `{"on":true,"volume":100}`)
	// Asking keeps it on...
	for i := 0; i < 4; i++ {
		time.Sleep(80 * time.Millisecond)
		h.do("GET", "/api/mic", "")
	}
	if !fv.on() {
		t.Fatal("the mic went off while the TV kept asking")
	}
	// ...and silence turns it off.
	time.Sleep(400 * time.Millisecond)
	if fv.on() {
		t.Error("the mic stayed on with nobody asking")
	}
}

func TestFromThisComputer(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:5000": true, "[::1]:5000": true, "203.0.113.7:5000": false, "garbage": false} {
		r := httptest.NewRequest("GET", "/api/mic", nil)
		r.RemoteAddr, r.Host = addr, "localhost:8765"
		if got := fromThisComputer(r); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
	name, _ := os.Hostname()
	// From loopback, but the page asked for another computer: a forwarder (ssh -R, socat)
	// brought it here from wherever that name is.
	for host, want := range map[string]bool{
		"localhost:8765": true, "127.0.0.1:8765": true, "[::1]:8765": true, "LocalHost": true,
		name + ":8765": name != "", "karaoke.example.ts.net": false, "203.0.113.7:8765": false, "": false,
	} {
		r := httptest.NewRequest("GET", "/api/mic", nil)
		r.RemoteAddr, r.Host = "127.0.0.1:5000", host
		if got := fromThisComputer(r); got != want {
			t.Errorf("host %q: %v, want %v", host, got, want)
		}
	}
}

func TestOtherSitesCannotKeepTheMicOn(t *testing.T) {
	fv := &fakeVoice{}
	h := newHarnessWith(t, false, func(c *Config) { c.Voice = fv; c.MicIdle = 150 * time.Millisecond })
	h.do("POST", "/api/mic", `{"on":true,"volume":100}`)
	for i := 0; i < 5; i++ {
		time.Sleep(80 * time.Millisecond)
		h.do("GET", "/api/mic", "", "Sec-Fetch-Site", "cross-site")
	}
	if fv.on() {
		t.Error("another site's requests kept the mic on")
	}
}

func TestMicStatusCarriesTheToneKnobs(t *testing.T) {
	h := newHarness(t, false)
	h.do("POST", "/api/player/status", `{"state":"idle","mic":{"on":true,"volume":100,"warmth":40,"bright":250,"double":10,"tune":30,"engine":"rocket"}}`)
	m := h.state().Player.Mic
	if m.Warmth != 40 || m.Bright != 100 || m.Double != 10 || m.Tune != 30 || m.Engine != "browser" {
		t.Errorf("mic status = %+v", m)
	}
	if resp, _ := h.do("POST", "/api/player/command", `{"action":"mic-tune","value":10}`); resp.StatusCode != 200 {
		t.Errorf("mic-tune command: %d", resp.StatusCode)
	}
	h.do("POST", "/api/player/status", `{"state":"idle","mic":{"on":true,"boost":40,"speed":-3,"scale":"key-11","songKey":"A minor"}}`)
	m = h.state().Player.Mic
	if m.Boost != 24 || m.Speed != 0 || m.Scale != "key-11" || m.SongKey != "A minor" {
		t.Errorf("mic status = %+v", m)
	}
	for _, bad := range []string{"key-12", "lydian", "<b>"} {
		h.do("POST", "/api/player/status", `{"state":"idle","mic":{"on":true,"scale":"`+bad+`"}}`)
		if m := h.state().Player.Mic; m.Scale != "chromatic" {
			t.Errorf("scale %q was let through as %q", bad, m.Scale)
		}
	}
	for _, action := range []string{"mic-speed", "mic-boost", "mic-scale", "mic-tune-preset"} {
		if resp, _ := h.do("POST", "/api/player/command", `{"action":"`+action+`","value":1}`); resp.StatusCode != 200 {
			t.Errorf("%s command: %d", action, resp.StatusCode)
		}
	}
}
