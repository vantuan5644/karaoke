// Package voice runs the singer's mic through a vocal chain on this computer, with a few
// milliseconds of delay instead of the browser's seventy: high-pass, tone, compressor,
// pitch correction, a doubler, the karaoke echo, reverb and a limiter.
package voice

import "math"

// Rate is the sample rate the whole chain runs at.
const Rate = 48000

// biquad is one second-order filter (RBJ cookbook), direct form I.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) set(b0, b1, b2, a0, a1, a2 float64) {
	f.b0, f.b1, f.b2, f.a1, f.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

func omega(hz float64) (cos, sin float64) {
	w := 2 * math.Pi * hz / Rate
	return math.Cos(w), math.Sin(w)
}

func (f *biquad) highpass(hz, q float64) {
	c, s := omega(hz)
	alpha := s / (2 * q)
	f.set((1+c)/2, -(1 + c), (1+c)/2, 1+alpha, -2*c, 1-alpha)
}

func (f *biquad) lowpass(hz, q float64) {
	c, s := omega(hz)
	alpha := s / (2 * q)
	f.set((1-c)/2, 1-c, (1-c)/2, 1+alpha, -2*c, 1-alpha)
}

func (f *biquad) peaking(hz, q, db float64) {
	c, s := omega(hz)
	alpha := s / (2 * q)
	a := math.Pow(10, db/40)
	f.set(1+alpha*a, -2*c, 1-alpha*a, 1+alpha/a, -2*c, 1-alpha/a)
}

// shelf is a low (low=true) or high shelf with slope 1.
func (f *biquad) shelf(hz, db float64, low bool) {
	c, s := omega(hz)
	a := math.Pow(10, db/40)
	alpha := s / 2 * math.Sqrt2
	sq := 2 * math.Sqrt(a) * alpha
	if low {
		f.set(a*((a+1)-(a-1)*c+sq), 2*a*((a-1)-(a+1)*c), a*((a+1)-(a-1)*c-sq),
			(a+1)+(a-1)*c+sq, -2*((a-1)+(a+1)*c), (a+1)+(a-1)*c-sq)
		return
	}
	f.set(a*((a+1)+(a-1)*c+sq), -2*a*((a-1)+(a+1)*c), a*((a+1)+(a-1)*c-sq),
		(a+1)-(a-1)*c+sq, 2*((a-1)-(a+1)*c), (a+1)-(a-1)*c-sq)
}

func (f *biquad) process(x float64) float64 {
	y := flush(f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2)
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// flush rounds a value too small to hear to zero. A feedback loop fed silence decays into
// subnormal numbers, which a CPU handles many times slower than normal ones, and they never
// quite reach zero on their own: a mic left on in a quiet room would get slower and slower.
func flush(v float64) float64 {
	if v > -1e-30 && v < 1e-30 {
		return 0
	}
	return v
}

// coefficient is the per-sample smoothing factor for a time constant.
func coefficient(seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return math.Exp(-1 / (seconds * Rate))
}

// compressor is a feed-forward compressor with a soft knee and makeup gain.
type compressor struct {
	threshold, knee, ratio, makeup float64 // dB, dB, :1, dB
	attack, release                float64 // smoothing coefficients
	env                            float64 // the gain reduction, in dB (<= 0)
}

func newCompressor(threshold, knee, ratio, attack, release, makeup float64) compressor {
	return compressor{threshold: threshold, knee: knee, ratio: ratio, makeup: makeup,
		attack: coefficient(attack), release: coefficient(release)}
}

// autoMakeup is the gain a browser's DynamicsCompressorNode adds on its own: 0.6 of what
// the curve takes off a full-scale signal. Matching it keeps both chains equally loud.
func (c *compressor) autoMakeup() float64 { return -0.6 * c.reduction(0) }

// reduction is the static curve: how many dB to take off a level.
func (c *compressor) reduction(db float64) float64 {
	over := db - c.threshold
	switch {
	case 2*over < -c.knee:
		return 0
	case 2*math.Abs(over) <= c.knee && c.knee > 0:
		x := over + c.knee/2
		return (1/c.ratio - 1) * x * x / (2 * c.knee)
	default:
		return (1/c.ratio - 1) * over
	}
}

func (c *compressor) process(x float64) float64 {
	level := math.Abs(x)
	db := -120.0
	if level > 1e-6 {
		db = 20 * math.Log10(level)
	}
	target := c.reduction(db)
	k := c.release
	if target < c.env {
		k = c.attack
	}
	c.env = target + k*(c.env-target)
	return x * math.Pow(10, (c.env+c.makeup)/20)
}

// ring is a delay line; read takes a fractional delay in samples.
type ring struct {
	buf []float64
	pos int
}

func newRing(samples int) ring { return ring{buf: make([]float64, samples)} }

func (r *ring) write(x float64) {
	r.buf[r.pos] = x
	r.pos++
	if r.pos == len(r.buf) {
		r.pos = 0
	}
}

// read returns the sample written delay samples ago (1 is the last one written).
func (r *ring) read(delay float64) float64 {
	n := len(r.buf)
	if delay < 1 {
		delay = 1
	}
	if delay > float64(n-2) {
		delay = float64(n - 2)
	}
	p := float64(r.pos) - delay
	for p < 0 {
		p += float64(n)
	}
	i := int(p)
	frac := p - float64(i)
	a := r.buf[i%n]
	b := r.buf[(i+1)%n]
	return a + (b-a)*frac
}

// doubler thickens a voice the way a second take does: two copies 10 to 30 ms late, each
// drifting slowly in time (which bends its pitch a few cents), one to each side.
type doubler struct {
	line           ring
	phaseL, phaseR float64
}

func newDoubler() doubler { return doubler{line: newRing(Rate / 10), phaseR: 0.37} }

func (d *doubler) process(x float64) (l, r float64) {
	d.line.write(x)
	d.phaseL += 0.31 / Rate
	d.phaseR += 0.47 / Rate
	if d.phaseL >= 1 {
		d.phaseL--
	}
	if d.phaseR >= 1 {
		d.phaseR--
	}
	ms := Rate / 1000.0
	l = d.line.read((14 + 2.5*math.Sin(2*math.Pi*d.phaseL)) * ms)
	r = d.line.read((23 + 3*math.Sin(2*math.Pi*d.phaseR)) * ms)
	return l, r
}

// echo is the karaoke machine's: one delay feeding back through filters, so each repeat is
// darker and quieter. Feedback stays well under 1.
type echo struct {
	line     ring
	hp, lp   biquad
	feedback float64
	delay    float64 // samples
}

func newEcho() echo {
	e := echo{line: newRing(Rate)}
	e.hp.highpass(250, 0.7)
	e.lp.lowpass(4500, 0.7)
	return e
}

func (e *echo) process(x float64) float64 {
	out := e.lp.process(e.hp.process(e.line.read(e.delay)))
	e.line.write(flush(x + out*e.feedback))
	return out
}

// reverb is Freeverb (Jezar's): eight damped combs and four all-passes per side, the right
// side's lines a little longer so the room sounds wide.
type reverb struct {
	pre       ring
	preDelay  float64
	hp        biquad
	combs     [2][8]comb
	allpasses [2][4]allpass
}

type comb struct {
	buf             []float64
	pos             int
	store, feedback float64
	damp            float64
}

type allpass struct {
	buf []float64
	pos int
}

var (
	combTuning    = [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617}
	allpassTuning = [4]int{556, 441, 341, 225}
)

const stereoSpread = 23

func newReverb(room, damp float64) *reverb {
	r := &reverb{pre: newRing(Rate / 5), preDelay: 0.025 * Rate}
	r.hp.highpass(200, 0.7)
	scale := float64(Rate) / 44100
	for side := 0; side < 2; side++ {
		for i, n := range combTuning {
			r.combs[side][i] = comb{buf: make([]float64, int(float64(n+side*stereoSpread)*scale)), feedback: room, damp: damp}
		}
		for i, n := range allpassTuning {
			r.allpasses[side][i] = allpass{buf: make([]float64, int(float64(n+side*stereoSpread)*scale))}
		}
	}
	return r
}

func (c *comb) process(x float64) float64 {
	out := c.buf[c.pos]
	c.store = flush(out*(1-c.damp) + c.store*c.damp)
	c.buf[c.pos] = flush(x + c.store*c.feedback)
	c.pos++
	if c.pos == len(c.buf) {
		c.pos = 0
	}
	return out
}

func (a *allpass) process(x float64) float64 {
	b := a.buf[a.pos]
	a.buf[a.pos] = flush(x + b*0.5)
	a.pos++
	if a.pos == len(a.buf) {
		a.pos = 0
	}
	return b - x
}

func (r *reverb) process(x float64) (l, rr float64) {
	r.pre.write(r.hp.process(x))
	in := r.pre.read(r.preDelay) * 0.015
	var out [2]float64
	for side := 0; side < 2; side++ {
		sum := 0.0
		for i := range r.combs[side] {
			sum += r.combs[side][i].process(in)
		}
		for i := range r.allpasses[side] {
			sum = r.allpasses[side][i].process(sum)
		}
		out[side] = sum
	}
	return out[0], out[1]
}

// limiter keeps the sum under full scale: a fast compressor, then a soft clip for anything
// that still gets through.
type limiter struct{ c compressor }

func newLimiter() limiter { return limiter{newCompressor(-3, 0, 20, 0.001, 0.05, 0)} }

// process limits a stereo pair by the louder side, so the image doesn't shift.
func (lm *limiter) process(l, r float64) (float64, float64) {
	peak := math.Max(math.Abs(l), math.Abs(r))
	gain := 1.0
	if peak > 1e-9 {
		gain = lm.c.process(peak) / peak
	}
	return softClip(l * gain), softClip(r * gain)
}

// softClip passes anything under the knee untouched and bends what's above it smoothly
// toward full scale, never past it: louder in is always louder out, with no step at the knee.
func softClip(x float64) float64 {
	const knee = 0.9
	switch {
	case x > knee:
		return knee + (1-knee)*math.Tanh((x-knee)/(1-knee))
	case x < -knee:
		return -knee - (1-knee)*math.Tanh((-x-knee)/(1-knee))
	}
	return x
}
