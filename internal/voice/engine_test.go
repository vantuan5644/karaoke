package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The engine's tests run this test binary as a stand-in for pw-record and pw-play, so they
// need no PipeWire: the record side writes a tone in real time, the play side counts what
// reaches it.
func TestMain(m *testing.M) {
	if os.Getenv("VOICE_FAKE_PW") != "" {
		os.Exit(fakePipeWire(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakePipeWire(args []string) int {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--channels=1") {
		hz, _ := strconv.ParseFloat(os.Getenv("VOICE_FAKE_TONE"), 64)
		amp, _ := strconv.ParseFloat(os.Getenv("VOICE_FAKE_AMP"), 64)
		dieAfter, _ := strconv.Atoi(os.Getenv("VOICE_FAKE_DIE_MS"))
		start := time.Now()
		chunk := make([]byte, 4*DefaultBuffer)
		for n := 0; ; n++ {
			if dieAfter > 0 && time.Since(start) > time.Duration(dieAfter)*time.Millisecond {
				fmt.Fprintln(os.Stderr, "stream error: no node found for target")
				return 1
			}
			for i := 0; i < DefaultBuffer; i++ {
				t := float64(n*DefaultBuffer+i) / Rate
				binary.LittleEndian.PutUint32(chunk[4*i:], math.Float32bits(float32(amp*math.Sin(2*math.Pi*hz*t))))
			}
			if _, err := os.Stdout.Write(chunk); err != nil {
				return 0
			}
			// Real time, roughly: a cycle's worth of audio every cycle.
			time.Sleep(time.Until(start.Add(time.Duration(n+1) * DefaultBuffer * time.Second / Rate)))
		}
	}
	got, peak := 0, 0.0
	buf := make([]byte, 8192)
	for {
		n, err := os.Stdin.Read(buf)
		for i := 0; i+4 <= n; i += 4 {
			peak = math.Max(peak, math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[i:])))))
		}
		got += n
		if err != nil {
			break
		}
	}
	os.WriteFile(os.Getenv("VOICE_FAKE_OUT"), []byte(fmt.Sprintf("%d %f %s", got, peak, joined)), 0o600)
	return 0
}

func fakeEngine(t *testing.T, tone, amp float64, dieMs int) (*Engine, string) {
	t.Helper()
	out := t.TempDir() + "/play"
	t.Setenv("VOICE_FAKE_PW", "1")
	t.Setenv("VOICE_FAKE_TONE", fmt.Sprint(tone))
	t.Setenv("VOICE_FAKE_AMP", fmt.Sprint(amp))
	t.Setenv("VOICE_FAKE_DIE_MS", fmt.Sprint(dieMs))
	t.Setenv("VOICE_FAKE_OUT", out)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{Record: self, Play: self, Dump: self, Output: "test_sink"})
	t.Cleanup(e.Close)
	return e, out
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestEngineRunsTheVoiceThrough(t *testing.T) {
	e, out := fakeEngine(t, 220, 0.2, 0)
	st := e.Set(Settings{On: true, Volume: 100, DeviceID: "alsa_input.usb-mic.mono-fallback"})
	if !st.Running || st.Error != "" {
		t.Fatalf("after turning on: %+v", st)
	}
	if st.LatencyMs < 5 || st.LatencyMs > 6 {
		t.Errorf("latency estimate %v ms, want two 128-sample cycles (5.3)", st.LatencyMs)
	}
	until(t, "a level on the meter", func() bool { return e.Status().Level > 0.05 })
	if st := e.Set(Settings{On: true, Volume: 100, Tune: 50, DeviceID: "alsa_input.usb-mic.mono-fallback"}); st.LatencyMs < 13 {
		t.Errorf("with tune on the estimate should add %v ms, got %v", DelayMs, st.LatencyMs)
	}
	time.Sleep(200 * time.Millisecond)
	if st := e.Set(Settings{On: false}); st.Running {
		t.Fatalf("still running after turning off: %+v", st)
	}
	until(t, "pw-play to finish", func() bool { _, err := os.Stat(out); return err == nil })
	data, _ := os.ReadFile(out)
	var got int
	var peak float64
	var args string
	fmt.Sscanf(string(data), "%d %f", &got, &peak)
	args = string(data)
	if got < 8*Rate/10 {
		t.Errorf("pw-play got %d bytes, want at least 0.1 s of stereo", got)
	}
	if peak < 0.05 || peak > 1 {
		t.Errorf("pw-play's peak %v", peak)
	}
	if !strings.Contains(args, "--target=test_sink") || !strings.Contains(args, "--latency=128") {
		t.Errorf("pw-play args: %s", args)
	}
}

func TestEngineReportsADeadMic(t *testing.T) {
	e, _ := fakeEngine(t, 220, 0.2, 150)
	e.Set(Settings{On: true, Volume: 100})
	until(t, "the engine to notice", func() bool { return !e.Status().Running })
	st := e.Status()
	if st.On || !strings.Contains(st.Error, "no node found") {
		t.Errorf("after pw-record died: %+v", st)
	}
	// Turning it on again clears the error.
	if st := e.Set(Settings{On: true, Volume: 100}); st.Error != "" || !st.Running {
		t.Errorf("after turning on again: %+v", st)
	}
}

func TestEngineTurnsDownAHowl(t *testing.T) {
	e, _ := fakeEngine(t, 2400, 0.9, 0)
	e.Set(Settings{On: true, Volume: 150})
	until(t, "the howl guard", func() bool { return e.Status().Howls > 0 })
	if st := e.Status(); st.Volume != 90 || !st.On {
		t.Errorf("after a howl: %+v, want volume 150 * 0.6 = 90", st)
	}
	// A knob turned before the page heard about the cut carries the old volume: keep the cut.
	none := 0
	if st := e.Set(Settings{On: true, Volume: 150, Echo: 40, SeenHowls: &none}); st.Volume != 90 || st.Echo != 40 {
		t.Errorf("a change sent before the cut was seen: volume %d echo %d, want 90 and 40", st.Volume, st.Echo)
	}
	// Once the page knows, raising it again is on purpose.
	seen := e.Status().Howls
	if st := e.Set(Settings{On: true, Volume: 120, SeenHowls: &seen}); st.Volume != 120 {
		t.Errorf("raised after the cut was seen: volume %d, want 120", st.Volume)
	}
	if st := e.Status(); st.SeenHowls != nil {
		t.Error("the sender's count is not part of the settings")
	}
}

func TestMeasureOneAtATime(t *testing.T) {
	e, _ := fakeEngine(t, 220, 0.2, 0)
	e.Set(Settings{On: true, Volume: 100})
	done := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := e.Measure(ctx)
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Errorf("measure: %v", err)
		}
	}
}

func TestEngineRefusesOddDeviceNames(t *testing.T) {
	e, _ := fakeEngine(t, 220, 0.2, 0)
	if st := e.Set(Settings{On: true, DeviceID: "--help"}); st.DeviceID != "" {
		t.Errorf("a device id that is not a node name got through: %q", st.DeviceID)
	}
}

func TestDevicesFromADump(t *testing.T) {
	dumpJSON := `[
	 {"type":"PipeWire:Interface:Metadata","props":{"metadata.name":"default"},
	  "metadata":[{"key":"default.audio.source","value":{"name":"alsa_input.usb-mic"}}]},
	 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source","node.name":"alsa_input.usb-mic","node.description":"USB Mic"}}},
	 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source/Virtual","node.name":"virtual-mic"}}},
	 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink","node.name":"speakers"}}},
	 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source","node.name":"bad name; rm"}}}
	]`
	var objects []dumpObject
	if err := jsonUnmarshal(dumpJSON, &objects); err != nil {
		t.Fatal(err)
	}
	got := devices(objects)
	want := []Device{{ID: "alsa_input.usb-mic", Name: "USB Mic", Default: true}, {ID: "virtual-mic", Name: "virtual-mic"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("devices = %v, want %v", got, want)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
