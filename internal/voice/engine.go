package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The engine runs the chain between PipeWire's own pw-record and pw-play, so the voice never
// goes near the browser: about 5 ms from mic to speaker instead of the browser's 60 to 70.
// It needs PipeWire (most Linux desktops); Find says whether it is there.

// Options say how to reach PipeWire.
type Options struct {
	Record, Play, Dump string // pw-record, pw-play, pw-dump
	Stdbuf             string // coreutils stdbuf, so pw-cat does not hold audio in stdio buffers
	Buffer             int    // samples per PipeWire cycle the streams ask for
	Output             string // the sink to play on; "" is the system default
}

// DefaultBuffer is 128 samples, 2.7 ms a cycle at 48 kHz.
const DefaultBuffer = 128

// Find looks for the PipeWire tools and checks the daemon answers.
func Find(ctx context.Context) (Options, error) {
	var o Options
	for _, t := range []struct {
		name string
		dst  *string
	}{{"pw-record", &o.Record}, {"pw-play", &o.Play}, {"pw-dump", &o.Dump}} {
		path, err := exec.LookPath(t.name)
		if err != nil {
			return Options{}, fmt.Errorf("%s not found: the low-latency mic needs PipeWire", t.name)
		}
		*t.dst = path
	}
	o.Stdbuf, _ = exec.LookPath("stdbuf")
	o.Buffer = DefaultBuffer
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, o.Dump, "--version").Run(); err != nil {
		return Options{}, fmt.Errorf("pw-dump: %w", err)
	}
	if _, err := dump(ctx, o.Dump); err != nil {
		return Options{}, err
	}
	return o, nil
}

// Device is one mic PipeWire knows.
type Device struct {
	ID      string `json:"id"` // the node name, which --target takes
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

type dumpObject struct {
	Type string `json:"type"`
	Info struct {
		Props map[string]any `json:"props"`
	} `json:"info"`
	Props    map[string]any `json:"props"`
	Metadata []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	} `json:"metadata"`
}

func dump(ctx context.Context, path string) ([]dumpObject, error) {
	out, err := exec.CommandContext(ctx, path).Output()
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w (is PipeWire running for this user?)", err)
	}
	var objects []dumpObject
	if err := json.Unmarshal(out, &objects); err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}
	return objects, nil
}

func prop(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// devices picks the mics out of a pw-dump.
func devices(objects []dumpObject) []Device {
	def := ""
	for _, o := range objects {
		if o.Type != "PipeWire:Interface:Metadata" || prop(o.Props, "metadata.name") != "default" {
			continue
		}
		for _, m := range o.Metadata {
			if m.Key == "default.audio.source" {
				var v struct{ Name string }
				if json.Unmarshal(m.Value, &v) == nil {
					def = v.Name
				}
			}
		}
	}
	var list []Device
	for _, o := range objects {
		p := o.Info.Props
		if o.Type != "PipeWire:Interface:Node" || !strings.HasPrefix(prop(p, "media.class"), "Audio/Source") {
			continue
		}
		id := prop(p, "node.name")
		if !validNode.MatchString(id) {
			continue
		}
		name := prop(p, "node.description")
		if name == "" {
			name = id
		}
		list = append(list, Device{ID: id, Name: name, Default: id == def})
	}
	return list
}

// validNode is what a PipeWire node name looks like; anything else is not passed on.
var validNode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+-]{0,199}$`)

// Status is what the engine is doing, for the TV.
type Status struct {
	Settings
	Running   bool    `json:"running"`
	Device    string  `json:"device,omitempty"` // the mic's name
	LatencyMs float64 `json:"latencyMs"`        // what the engine adds, before the devices' own
	Level     float64 `json:"level"`            // the loudest sample lately, 0..1
	Howls     int     `json:"howls"`            // times feedback turned the mic down
	Error     string  `json:"error,omitempty"`
}

// Howl handling, as in the browser: down 40%, at most every 3 s, never below 25%.
const (
	howlCut      = 0.6
	howlFloor    = 25
	howlCooldown = 3 * time.Second
)

// Engine owns the mic pipeline.
type Engine struct {
	opt Options

	measuring sync.Mutex // one Measure at a time: two would steal each other's clicks

	mu       sync.Mutex
	s        Settings
	cur      *pipeline
	err      string
	howls    int
	howledAt time.Time
	names    map[string]string // node name -> description
}

// New makes an engine; nothing runs until Set turns the mic on.
func New(opt Options) *Engine {
	if opt.Buffer <= 0 {
		opt.Buffer = DefaultBuffer
	}
	return &Engine{opt: opt, s: Defaults, names: map[string]string{}}
}

// Devices lists the mics.
func (e *Engine) Devices(ctx context.Context) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	objects, err := dump(ctx, e.opt.Dump)
	if err != nil {
		return nil, err
	}
	list := devices(objects)
	e.mu.Lock()
	for _, d := range list {
		e.names[d.ID] = d.Name
	}
	e.mu.Unlock()
	return list, nil
}

// Set applies the singer's settings, starting, restarting or stopping the pipeline.
func (e *Engine) Set(s Settings) Status {
	s = Normalize(s)
	if s.DeviceID != "" && !validNode.MatchString(s.DeviceID) {
		s.DeviceID = ""
	}
	seen := s.SeenHowls
	s.SeenHowls = nil
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.s
	if seen != nil && *seen < e.howls && before.On && s.Volume > before.Volume {
		s.Volume = before.Volume // sent before the sender saw the mic turned down for feedback
	}
	e.s = s
	switch {
	case !s.On:
		e.stopLocked()
	case e.cur == nil || s.DeviceID != before.DeviceID:
		e.stopLocked()
		e.err = ""
		p, err := e.start(s)
		if err != nil {
			e.err = err.Error()
			e.s.On = false
		} else {
			e.cur = p
		}
	default:
		params := ParamsFor(s)
		e.cur.params.Store(&params)
	}
	return e.statusLocked()
}

// Off turns the mic off and keeps its settings, in one step: nothing sent in between is lost.
func (e *Engine) Off() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.s.On = false
	e.stopLocked()
}

// Status reports without changing anything.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked()
}

func (e *Engine) statusLocked() Status {
	st := Status{Settings: e.s, Howls: e.howls, Error: e.err}
	if p := e.cur; p != nil {
		st.Running = true
		st.Device = e.names[e.s.DeviceID]
		switch {
		case st.Device != "":
		case e.s.DeviceID != "":
			st.Device = e.s.DeviceID
		default:
			st.Device = "Default mic"
		}
		st.Level = math.Float64frombits(p.level.Swap(0)) // the loudest since the last look
		quantum := float64(p.quantum.Load())
		if quantum == 0 {
			quantum = float64(e.opt.Buffer)
		}
		ms := 2*quantum/Rate*1000 + float64(p.backlog.Load())/8/Rate*1000
		if e.s.Tune > 0 {
			ms += DelayMs
		}
		st.LatencyMs = math.Round(ms*10) / 10
	}
	return st
}

// Close stops the mic.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
}

func (e *Engine) stopLocked() {
	if e.cur != nil {
		e.cur.stop()
		e.cur = nil
	}
}

// howl is the pump saying the voice is feeding back.
func (e *Engine) howl(p *pipeline) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != p || time.Since(e.howledAt) < howlCooldown {
		return
	}
	e.howledAt = time.Now()
	volume := max(howlFloor, int(math.Round(float64(e.s.Volume)*howlCut)))
	if volume >= e.s.Volume {
		return
	}
	e.s.Volume = volume
	e.howls++
	params := ParamsFor(e.s)
	p.params.Store(&params)
}

// exited is the pump saying the pipeline died on its own: the mic was unplugged, or
// PipeWire went away.
func (e *Engine) exited(p *pipeline, why string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != p {
		return
	}
	e.cur = nil
	e.s.On = false
	e.err = "The mic stopped: " + why
	go p.stop()
}

// --- The pipeline ----------------------------------------------------------------------

type pipeline struct {
	rec, play *exec.Cmd
	in, out   *os.File // pw-record's stdout, pw-play's stdin
	recLog    *tail
	playLog   *tail
	params    atomic.Pointer[Params]
	level     atomic.Uint64                // float64 bits: the loudest sample since Status last read it
	quantum   atomic.Int64                 // samples per cycle, as seen in pw-record's writes
	backlog   atomic.Int64                 // bytes waiting in pw-play's stdin
	buffer    int                          // the samples per cycle asked for
	measure   atomic.Pointer[chan float64] // a delay measurement to start
	done      chan struct{}
	stopOnce  sync.Once
}

func (e *Engine) command(stdbuf, path string, args ...string) *exec.Cmd {
	if e.opt.Stdbuf != "" {
		return exec.Command(e.opt.Stdbuf, append([]string{stdbuf, path}, args...)...)
	}
	return exec.Command(path, args...)
}

func streamArgs(buffer, channels int, name string) []string {
	return []string{
		"--raw", "--format=f32", "--rate=48000", fmt.Sprintf("--channels=%d", channels),
		fmt.Sprintf("--latency=%d", buffer),
		"-P", fmt.Sprintf(`{ node.name = "%s" node.group = "karaoke-mic" node.description = "Karaoke mic" media.name = "Karaoke mic" application.name = "Karaoke" }`, name),
	}
}

func (e *Engine) start(s Settings) (*pipeline, error) {
	recArgs := streamArgs(e.opt.Buffer, 1, "karaoke-mic-in")
	if s.DeviceID != "" {
		recArgs = append(recArgs, "--target="+s.DeviceID)
	}
	playArgs := streamArgs(e.opt.Buffer, 2, "karaoke-mic-out")
	if e.opt.Output != "" {
		playArgs = append(playArgs, "--target="+e.opt.Output)
	}
	p := &pipeline{
		rec:     e.command("-o0", e.opt.Record, append(recArgs, "-")...),
		play:    e.command("-i0", e.opt.Play, append(playArgs, "-")...),
		recLog:  &tail{},
		playLog: &tail{},
		buffer:  e.opt.Buffer,
		done:    make(chan struct{}),
	}
	params := ParamsFor(s)
	p.params.Store(&params)

	playIn, out, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	in, recOut, err := os.Pipe()
	if err != nil {
		playIn.Close()
		out.Close()
		return nil, err
	}
	p.in, p.out = in, out
	p.play.Stdin, p.play.Stderr = playIn, p.playLog
	p.rec.Stdout, p.rec.Stderr = recOut, p.recLog
	for _, c := range []*exec.Cmd{p.play, p.rec} {
		c.SysProcAttr = childAttr()
	}
	// Play first, so the first recorded audio has somewhere to go at once.
	if err := p.play.Start(); err != nil {
		closeAll(playIn, out, in, recOut)
		return nil, fmt.Errorf("pw-play: %w", err)
	}
	if err := p.rec.Start(); err != nil {
		closeAll(playIn, recOut)
		p.stop()
		return nil, fmt.Errorf("pw-record: %w", err)
	}
	closeAll(playIn, recOut) // the children hold their own ends
	go func() {
		why := p.pump(func() { go e.howl(p) })
		e.exited(p, why)
	}()
	return p, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// Each cycle the pump writes one cycle of audio and pw-play takes it, so before a write
// pw-play's stdin should be all but empty. When it has held more than a cycle and a half
// for several writes running (a stall at startup, or the pump fell behind), the pump drops
// a chunk to catch up: otherwise the voice would stay that much late for good.
const (
	backlogCycles = 1.5
	backlogWrites = 4
)

// pump moves audio from pw-record through the chain to pw-play until either side ends, and
// says why it ended.
func (p *pipeline) pump(onHowl func()) string {
	defer close(p.done)
	params := p.params.Load()
	chain := NewChain(*params)
	raw := make([]byte, 16384)
	carry := 0
	mono := make([]float32, len(raw)/4)
	stereo := make([]float32, 2*len(mono))
	outBytes := make([]byte, 8*len(mono))
	var frame int64
	var click probe
	smallest, since := 0, time.Now()
	limit, over := int(backlogCycles*float64(p.buffer*8)), 0
	for {
		n, err := p.in.Read(raw[carry:])
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return "stopped"
			}
			return p.reason("pw-record")
		}
		if smallest == 0 || n < smallest {
			smallest = n
		}
		if time.Since(since) > time.Second {
			p.quantum.Store(int64(smallest / 4))
			limit = int(backlogCycles * float64(max(p.buffer, smallest/4)*8))
			smallest, since = 0, time.Now()
		}
		total := carry + n
		frames := total / 4
		for i := 0; i < frames; i++ {
			mono[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		}
		carry = copy(raw, raw[4*frames:total])
		if q := p.params.Load(); q != params {
			params = q
			chain.Set(*params)
		}
		chain.Process(mono[:frames], stereo[:2*frames])
		if chain.Howled() {
			onHowl()
		}
		if result := p.measure.Swap(nil); result != nil {
			click.start(frame, *result)
		}
		if click.state != probeIdle {
			for i := 0; i < frames; i++ {
				y := float32(click.step(frame+int64(i), float64(mono[i])))
				stereo[2*i] += y
				stereo[2*i+1] += y
			}
		}
		frame += int64(frames)
		p.raiseLevel(chain.Peak())
		queued := queuedBytes(p.out)
		p.backlog.Store(int64(queued))
		if queued > limit {
			over++
		} else {
			over = 0
		}
		if over >= backlogWrites {
			over = 0
			continue // behind: drop this chunk rather than stay late
		}
		for i, v := range stereo[:2*frames] {
			binary.LittleEndian.PutUint32(outBytes[4*i:], math.Float32bits(v))
		}
		if _, err := p.out.Write(outBytes[:8*frames]); err != nil {
			if errors.Is(err, os.ErrClosed) {
				return "stopped"
			}
			return p.reason("pw-play")
		}
	}
}

// reason is the last thing a dead side printed, or that it ended.
func (p *pipeline) reason(side string) string {
	log := p.recLog
	if side == "pw-play" {
		log = p.playLog
	}
	if msg := log.last(); msg != "" {
		return side + ": " + msg
	}
	return side + " ended"
}

func (p *pipeline) stop() {
	p.stopOnce.Do(func() {
		p.out.Close() // pw-play reads EOF and finishes
		p.in.Close()  // unblocks the pump
		for _, c := range []*exec.Cmd{p.rec, p.play} {
			if c.Process == nil {
				continue
			}
			c.Process.Signal(os.Interrupt)
			exited := make(chan struct{})
			go func() { c.Wait(); close(exited) }()
			select {
			case <-exited:
			case <-time.After(time.Second):
				c.Process.Kill()
				<-exited
			}
		}
	})
}

// tail keeps the last line a child wrote to stderr.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	return len(b), nil
}

func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(t.buf), []byte("\n"))
	msg := strings.TrimSpace(string(lines[len(lines)-1]))
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return msg
}

var _ io.Writer = (*tail)(nil)

// raiseLevel keeps the loudest sample until Status reads it, so a meter polled every quarter
// second sees the peaks between its looks, not just the last few milliseconds.
func (p *pipeline) raiseLevel(peak float64) {
	for {
		old := p.level.Load()
		if peak <= math.Float64frombits(old) || p.level.CompareAndSwap(old, math.Float64bits(peak)) {
			return
		}
	}
}
