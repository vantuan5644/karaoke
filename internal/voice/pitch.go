package voice

import "math"

// Pitch correction, the "auto-tune" kind: find the note being sung (YIN), work out how far
// it is from the nearest semitone, and move it there with TD-PSOLA (overlapping grains, each
// two periods long, laid down closer together or further apart than they were sung). Grains
// are read at normal speed, so the voice keeps its own formants rather than sounding sped up.
//
// It costs DelayMs of extra delay while it is on, and nothing when it is off.

const (
	// DelayMs is how far behind the live voice the grains are read: room to pick a grain a
	// whole period earlier or later, which is how PSOLA repeats or skips a cycle.
	DelayMs = 8.0

	decimate    = 3 // the detector runs at 16 kHz
	detectRate  = Rate / decimate
	minHz       = 70.0
	maxHz       = 800.0
	yinWindow   = 512 // 32 ms at 16 kHz
	yinHop      = 80  // a fresh estimate every 5 ms
	yinCutoff   = 0.15
	quietRMS    = 0.003 // about -50 dBFS: anything quieter is not a note
	unvoicedHop = Rate / 100
)

// detector is YIN (de Cheveigné and Kawahara, 2002) on a 16 kHz copy of the input.
type detector struct {
	aa1, aa2 biquad // anti-alias low-pass before decimating
	phase    int
	buf      []float64 // the decimated input, a ring
	span     []float64 // the stretch YIN looks at, copied out of buf
	pos      int
	count    int
	diff     []float64
	minLag   int
	maxLag   int

	period  float64 // the last estimate, in Rate samples; 0 when there is no note
	updated bool    // a new estimate came in with the last push
}

func newDetector() *detector {
	d := &detector{
		minLag: int(detectRate / maxHz),
		maxLag: int(math.Ceil(detectRate / minHz)),
	}
	d.buf = make([]float64, 1024)
	d.span = make([]float64, yinWindow+d.maxLag+1)
	d.diff = make([]float64, d.maxLag+2)
	d.aa1.lowpass(5000, 0.54)
	d.aa2.lowpass(5000, 1.31)
	return d
}

func (d *detector) push(x float64) {
	d.updated = false
	y := d.aa2.process(d.aa1.process(x))
	d.phase++
	if d.phase < decimate {
		return
	}
	d.phase = 0
	d.buf[d.pos] = y
	d.pos = (d.pos + 1) % len(d.buf)
	d.count++
	if d.count >= yinHop {
		d.count = 0
		d.period = d.estimate() * decimate
		d.updated = true
	}
}

// estimate returns the period in decimated samples, or 0 for silence, noise or a chord.
func (d *detector) estimate() float64 {
	// The analysis span, oldest first, out of the ring.
	n, span := len(d.buf), len(d.span)
	start := (d.pos - span + n) % n
	copy(d.span, d.buf[start:])
	if start+span > n {
		copy(d.span[n-start:], d.buf[:start+span-n])
	}
	x := d.span
	energy := 0.0
	for _, v := range x[:yinWindow] {
		energy += v * v
	}
	if math.Sqrt(energy/yinWindow) < quietRMS {
		return 0
	}
	// The difference function, then its cumulative mean normalised form, in place.
	d.diff[0] = 1
	running := 0.0
	for tau := 1; tau <= d.maxLag; tau++ {
		sum := 0.0
		shifted := x[tau : tau+yinWindow]
		for j, v := range x[:yinWindow] {
			delta := v - shifted[j]
			sum += delta * delta
		}
		running += sum
		if running > 0 {
			d.diff[tau] = sum * float64(tau) / running
		} else {
			d.diff[tau] = 1
		}
	}
	tau := 0
	for t := d.minLag; t < d.maxLag; t++ {
		if d.diff[t] < yinCutoff {
			for t+1 < d.maxLag && d.diff[t+1] < d.diff[t] {
				t++
			}
			tau = t
			break
		}
	}
	if tau == 0 {
		return 0
	}
	// Parabolic interpolation for a period between samples.
	a, b, c := d.diff[tau-1], d.diff[tau], d.diff[tau+1]
	shift := 0.0
	if den := a - 2*b + c; den > 0 {
		shift = 0.5 * (a - c) / den
	}
	return float64(tau) + math.Max(-0.5, math.Min(0.5, shift))
}

type grain struct {
	active bool
	pos    int
	length int
	delay  float64
}

// tuner corrects the pitch of one voice.
type tuner struct {
	det     *detector
	line    ring
	grains  [4]grain
	next    float64 // samples until the next grain starts
	last    float64 // the delay the last grain started at
	ratio   float64 // the pitch change being applied now
	target  float64 // where ratio is heading
	note    float64 // the note being corrected to (MIDI), 0 for none
	silent  int     // samples since the last note was heard
	amount  float64 // 0..1 of the way to the note
	smooth  float64 // per-sample smoothing toward target
	centre  float64 // DelayMs, in samples
	mix     float64 // 0 is the dry voice, 1 the corrected one
	enabled bool
}

func newTuner() *tuner {
	t := &tuner{det: newDetector(), line: newRing(4096), ratio: 1, target: 1, centre: DelayMs * Rate / 1000}
	t.last = t.centre
	t.setTune(0)
	return t
}

// setTune takes the singer's knob (0 to 100). Low values pull a little and slowly, so the
// voice keeps its slides and vibrato; 100 snaps to the note at once, the robotic effect.
func (t *tuner) setTune(tune float64) {
	p := tuneParams(tune)
	t.amount = p.amount
	t.smooth = 1 - coefficient(p.seconds)
	t.enabled = p.amount > 0
}

type tunePlan struct{ amount, seconds float64 }

// tuneParams is the knob's arithmetic, shared with the browser's copy (mic-core.js).
func tuneParams(tune float64) tunePlan {
	s := math.Max(0, math.Min(100, tune)) / 100
	if s == 0 {
		return tunePlan{}
	}
	return tunePlan{amount: math.Min(1, s/0.6), seconds: 0.3*(1-s)*(1-s) + 0.005}
}

// noteFor picks the semitone to correct a pitch to, holding the current one until the
// singer is clearly nearer another: without that, a note sung half way between two would
// flip back and forth.
func noteFor(midi, current float64) float64 {
	if current > 0 && math.Abs(midi-current) < 0.65 {
		return current
	}
	return math.Round(midi)
}

func (t *tuner) process(x float64) float64 {
	if !t.enabled && t.mix == 0 {
		return x
	}
	want := 0.0
	if t.enabled {
		want = 1
	}
	t.mix += (want - t.mix) * 0.0007 // about 30 ms
	if !t.enabled && t.mix < 1e-4 {
		t.mix = 0
		t.reset()
		return x
	}
	t.det.push(x)
	t.line.write(x)
	if t.det.updated {
		t.retarget()
	}
	t.ratio += (t.target - t.ratio) * t.smooth

	t.next--
	if t.next <= 0 {
		t.startGrain()
	}
	acc, weights := 0.0, 0.0
	for i := range t.grains {
		g := &t.grains[i]
		if !g.active {
			continue
		}
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(g.pos)/float64(g.length))
		acc += w * t.line.read(g.delay)
		weights += w
		g.pos++
		if g.pos >= g.length {
			g.active = false
		}
	}
	out := 0.0
	if weights > 1e-6 {
		out = acc / weights
	}
	return x + (out-x)*t.mix
}

// retarget turns a fresh pitch estimate into the ratio to correct by.
func (t *tuner) retarget() {
	period := t.det.period
	if period <= 0 {
		t.silent += yinHop * decimate
		if t.silent > Rate/10 {
			t.note = 0
		}
		t.target = 1
		return
	}
	t.silent = 0
	midi := 69 + 12*math.Log2(Rate/period/440)
	t.note = noteFor(midi, t.note)
	t.target = math.Pow(2, t.amount*(t.note-midi)/12)
}

// startGrain lays down the next grain. Its read position is lined up with the last grain's,
// a whole number of periods apart, so the two add up in phase.
func (t *tuner) startGrain() {
	period := t.det.period
	if period <= 0 {
		period = unvoicedHop
	}
	hop := period / t.ratio
	delay := t.last + hop
	cycles := math.Round((delay - t.centre) / period)
	delay -= cycles * period
	delay = math.Max(2, math.Min(float64(len(t.line.buf)-4), delay))
	slot, progress := 0, -1.0
	for i, g := range t.grains {
		if !g.active {
			slot = i
			break
		}
		if p := float64(g.pos) / float64(g.length); p > progress {
			slot, progress = i, p // all busy: take over the one nearest its end
		}
	}
	t.grains[slot] = grain{active: true, length: int(math.Max(32, math.Round(2*period))), delay: delay}
	t.last = delay
	t.next += hop
}

// reset forgets the voice, so turning the correction on again starts clean.
func (t *tuner) reset() {
	for i := range t.grains {
		t.grains[i].active = false
	}
	clear(t.line.buf)
	t.next, t.last, t.ratio, t.target, t.note = 0, t.centre, 1, 1, 0
}
