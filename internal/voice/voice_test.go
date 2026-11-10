package voice

import (
	"math"
	"math/cmplx"
	"math/rand"
	"testing"
)

// saw is a band-limited sawtooth: a voice-like tone with a full set of harmonics.
func saw(hz float64, n int) []float64 {
	out := make([]float64, n)
	for k := 1; hz*float64(k) < 8000; k++ {
		for i := range out {
			out[i] += 0.3 * math.Sin(2*math.Pi*hz*float64(k)*float64(i)/Rate) / float64(k)
		}
	}
	return out
}

func sine(hz float64, n int, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*hz*float64(i)/Rate)
	}
	return out
}

// pitchOf runs a fresh detector over x and returns the median frequency it heard in the
// second half, or 0 when it heard no note.
func pitchOf(x []float64) float64 {
	d := newDetector()
	var heard []float64
	for i, v := range x {
		d.push(v)
		if d.updated && i > len(x)/2 && d.period > 0 {
			heard = append(heard, Rate/d.period)
		}
	}
	if len(heard) == 0 {
		return 0
	}
	for i := 1; i < len(heard); i++ { // insertion sort: a few hundred values
		for j := i; j > 0 && heard[j] < heard[j-1]; j-- {
			heard[j], heard[j-1] = heard[j-1], heard[j]
		}
	}
	return heard[len(heard)/2]
}

func cents(a, b float64) float64 { return 1200 * math.Log2(a/b) }

func rms(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

func TestDetectorFindsThePitch(t *testing.T) {
	for _, hz := range []float64{82, 110, 196, 330, 440, 660} {
		for name, x := range map[string][]float64{"sine": sine(hz, Rate/4, 0.3), "saw": saw(hz, Rate/4)} {
			got := pitchOf(x)
			if math.Abs(cents(got, hz)) > 5 {
				t.Errorf("%s at %v Hz: heard %.1f Hz (%.1f cents off)", name, hz, got, cents(got, hz))
			}
		}
	}
}

func TestDetectorHearsNoNoteInSilenceOrNoise(t *testing.T) {
	if got := pitchOf(make([]float64, Rate/4)); got != 0 {
		t.Errorf("silence: heard %v Hz", got)
	}
	r := rand.New(rand.NewSource(1))
	noise := make([]float64, Rate/4)
	for i := range noise {
		noise[i] = 0.3 * (r.Float64()*2 - 1)
	}
	d := newDetector()
	voiced, total := 0, 0
	for _, v := range noise {
		d.push(v)
		if d.updated {
			total++
			if d.period > 0 {
				voiced++
			}
		}
	}
	if voiced*10 > total {
		t.Errorf("noise: heard a note in %d of %d looks", voiced, total)
	}
}

func process(tu *tuner, x []float64) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = tu.process(v)
	}
	return out
}

func TestTuneOffChangesNothing(t *testing.T) {
	tu := newTuner()
	x := saw(452, Rate/10)
	for i, y := range process(tu, x) {
		if y != x[i] {
			t.Fatalf("sample %d: %v in, %v out", i, x[i], y)
		}
	}
}

func TestTuneMovesANoteToTheNearestSemitone(t *testing.T) {
	for _, c := range []struct {
		in, want float64
		tune     float64
		within   float64 // cents
	}{
		{in: 452.1, want: 440, tune: 100, within: 4},                                   // A4 + 47 cents, snapped
		{in: 430, want: 440, tune: 100, within: 4},                                     // A4 - 40 cents
		{in: 200, want: 196, tune: 100, within: 4},                                     // G3 + 35 cents
		{in: 452.1, want: 440 * math.Pow(2, 0.47*(1-0.5/0.6)/12), tune: 50, within: 4}, // most of the way
	} {
		tu := newTuner()
		tu.setTune(c.tune)
		out := process(tu, saw(c.in, Rate/2))
		got := pitchOf(out)
		if off := cents(got, c.want); math.Abs(off) > c.within {
			t.Errorf("%v Hz at tune %v: came out %.1f Hz, %.1f cents from %.1f", c.in, c.tune, got, off, c.want)
		}
	}
}

func TestTuneKeepsTheLevelAndTheDelay(t *testing.T) {
	tu := newTuner()
	tu.setTune(100)
	x := saw(452.1, Rate/2)
	out := process(tu, x)
	half := len(x) / 2
	ratio := 20 * math.Log10(rms(out[half:])/rms(x[half:]))
	if math.Abs(ratio) > 1 {
		t.Errorf("level changed by %.2f dB", ratio)
	}
	// The grains are read DelayMs behind the voice, give or take half a period: that is the
	// delay a singer hears while it is on.
	for _, hz := range []float64{110, 452.1} {
		tu = newTuner()
		tu.setTune(100)
		lo, hi := math.Inf(1), 0.0
		for i, v := range saw(hz, Rate/2) {
			tu.process(v)
			if i > Rate/10 {
				lo, hi = math.Min(lo, tu.last), math.Max(hi, tu.last)
			}
		}
		half := Rate / hz / 2
		if lo < tu.centre-half-1 || hi > tu.centre+half+1 {
			t.Errorf("%v Hz: grains read %.1f..%.1f samples back, want %.0f ± %.0f", hz, lo, hi, tu.centre, half)
		}
	}
	// Noise (no note) comes through exactly DelayMs late.
	tu = newTuner()
	tu.setTune(100)
	r := rand.New(rand.NewSource(2))
	noise := make([]float64, Rate/4)
	for i := range noise {
		noise[i] = 0.2 * (r.Float64()*2 - 1)
	}
	out = process(tu, noise)
	lag := int(tu.centre) - 1 // the sample is written before it is read
	// (After the 30 ms fade from the dry voice has finished.)
	if e := rms(sub(out[Rate/5:], noise[Rate/5-lag:len(noise)-lag])); e > 1e-3 {
		t.Errorf("noise is not just delayed by %d samples (error %v)", lag, e)
	}
}

func TestTuneNoteChoiceHolds(t *testing.T) {
	if got := noteFor(69.6, 69); got != 69 {
		t.Errorf("60 cents above a held A should stay on A, got %v", got)
	}
	if got := noteFor(69.7, 69); got != 70 {
		t.Errorf("70 cents above should move to B flat, got %v", got)
	}
	if got := noteFor(69.4, 0); got != 69 {
		t.Errorf("a fresh note takes the nearest semitone, got %v", got)
	}
}

func run(c *Chain, x []float64) (l, r []float64) {
	in := make([]float32, len(x))
	for i, v := range x {
		in[i] = float32(v)
	}
	out := make([]float32, 2*len(x))
	c.Process(in, out)
	l, r = make([]float64, len(x)), make([]float64, len(x))
	for i := range x {
		l[i], r[i] = float64(out[2*i]), float64(out[2*i+1])
	}
	return l, r
}

func TestChainIsSilentWhenOff(t *testing.T) {
	c := NewChain(ParamsFor(Settings{On: false, Volume: 100, Echo: 50, Reverb: 50}))
	l, r := run(c, saw(220, Rate/2))
	if rms(l)+rms(r) > 1e-6 {
		t.Errorf("mic off still made sound: %v %v", rms(l), rms(r))
	}
}

func TestChainNeverClips(t *testing.T) {
	c := NewChain(ParamsFor(Settings{On: true, Volume: 200, Echo: 100, Reverb: 100, Warmth: 100, Bright: 100, Double: 100, Tune: 100}))
	l, r := run(c, sine(300, Rate, 1))
	for i := range l {
		if math.Abs(l[i]) > 1 || math.Abs(r[i]) > 1 {
			t.Fatalf("sample %d over full scale: %v %v", i, l[i], r[i])
		}
	}
}

func TestSoftClipBendsWithoutAStep(t *testing.T) {
	prev := softClip(-3)
	for x := -3.0; x <= 3; x += 0.0005 {
		y := softClip(x)
		if y < prev || math.Abs(y) > 1 {
			t.Fatalf("softClip(%v) = %v after %v: louder in must be louder out, under full scale", x, y, prev)
		}
		if math.Abs(y-prev) > 0.001 {
			t.Fatalf("softClip jumps by %v at %v", y-prev, x)
		}
		prev = y
	}
	if softClip(0.5) != 0.5 || softClip(-0.89) != -0.89 {
		t.Error("under the knee the sound must pass untouched")
	}
}

// subnormals counts the numbers too small to be normal in a chain's feedback paths.
func subnormals(c *Chain) int {
	n := 0
	count := func(vs ...float64) {
		for _, v := range vs {
			if v != 0 && math.Abs(v) < 0x1p-1022 {
				n++
			}
		}
	}
	for side := range c.reverb.combs {
		for i := range c.reverb.combs[side] {
			count(c.reverb.combs[side][i].buf...)
			count(c.reverb.combs[side][i].store)
		}
		for i := range c.reverb.allpasses[side] {
			count(c.reverb.allpasses[side][i].buf...)
		}
	}
	count(c.echo.line.buf...)
	count(c.reverb.pre.buf...)
	for _, f := range []*biquad{&c.highpass, &c.warmth, &c.mud, &c.presence, &c.bright, &c.echo.hp, &c.echo.lp, &c.reverb.hp} {
		count(f.x1, f.x2, f.y1, f.y2)
	}
	return n
}

func TestSilenceDoesNotLeaveSubnormals(t *testing.T) {
	c := NewChain(ParamsFor(Settings{On: true, Volume: 100, Echo: 100, Reverb: 100, Warmth: 50, Bright: 50, Double: 50}))
	run(c, saw(220, Rate/2))
	run(c, make([]float64, 60*Rate)) // a muted mic for a minute
	if n := subnormals(c); n > 0 {
		t.Errorf("%d subnormal numbers left in the feedback paths after a minute of silence", n)
	}
}

// gainAt is how loud a tone of hz comes out of a dry chain (no echo or reverb), in dB.
func gainAt(s Settings, hz float64) float64 {
	s.On, s.Volume, s.Echo, s.Reverb = true, 100, 0, 0
	c := NewChain(ParamsFor(s))
	c.comp = newCompressor(0, 0, 1, 0.003, 0.15, 0) // no compression, to read the EQ alone
	x := sine(hz, Rate/2, 0.05)
	l, _ := run(c, x)
	return 20 * math.Log10(rms(l[Rate/4:])/rms(x[Rate/4:]))
}

func TestToneKnobs(t *testing.T) {
	flat := Settings{}
	if lift := gainAt(Settings{Warmth: 100}, 150) - gainAt(flat, 150); lift < 4 || lift > 6.5 {
		t.Errorf("warmth 100 lifts 150 Hz by %.1f dB, want about 5", lift)
	}
	if lift := gainAt(Settings{Warmth: 100}, 3000) - gainAt(flat, 3000); math.Abs(lift) > 0.5 {
		t.Errorf("warmth moved 3 kHz by %.1f dB", lift)
	}
	if lift := gainAt(Settings{Bright: 100}, 9000) - gainAt(flat, 9000); lift < 6 || lift > 8.5 {
		t.Errorf("bright 100 lifts 9 kHz by %.1f dB, want about 7", lift)
	}
	if lift := gainAt(Settings{Bright: 100}, 300) - gainAt(flat, 300); math.Abs(lift) > 0.5 {
		t.Errorf("bright moved 300 Hz by %.1f dB", lift)
	}
}

func TestDoubleWidensTheVoice(t *testing.T) {
	dry := NewChain(ParamsFor(Settings{On: true, Volume: 100}))
	l, r := run(dry, saw(220, Rate/2))
	if d := rms(sub(l, r)); d > 1e-9 {
		t.Errorf("without double the voice should sit in the middle, L-R = %v", d)
	}
	wide := NewChain(ParamsFor(Settings{On: true, Volume: 100, Double: 100}))
	l, r = run(wide, saw(220, Rate/2))
	if d := rms(sub(l[Rate/4:], r[Rate/4:])); d < 0.01 {
		t.Errorf("double should make the sides differ, L-R = %v", d)
	}
}

func sub(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out
}

func TestReverbTailLength(t *testing.T) {
	r := newReverb(0.84, 0.25)
	energy := make([]float64, 3*Rate)
	for i := range energy {
		x := 0.0
		if i == 0 {
			x = 1
		}
		l, rr := r.process(x)
		energy[i] = l*l + rr*rr
	}
	// RT60 from the energy decay (Schroeder integration).
	total := 0.0
	for _, e := range energy {
		total += e
	}
	rest, t60 := total, 0.0
	for i, e := range energy {
		rest -= e
		if 10*math.Log10(rest/total) < -60 {
			t60 = float64(i) / Rate
			break
		}
	}
	if t60 < 0.9 || t60 > 2.2 {
		t.Errorf("reverb RT60 %.2f s, want a small hall (about 1.2-1.8 s)", t60)
	}
	t.Logf("reverb RT60 %.2f s, impulse energy %.3f", t60, total)
}

func TestParamsMatchTheBrowser(t *testing.T) {
	// The same numbers mic-core.test.mjs checks for params().
	p := ParamsFor(Settings{On: true, Volume: 150, Echo: 50, Reverb: 40, Warmth: 50, Bright: 25, Double: 50, Tune: 30})
	want := Params{On: true, MicGain: 1.5, EchoSend: 0.35, EchoFeedback: 0.285, ReverbSend: 0.34, WarmthDb: 3, BrightDb: 2, DoubleMix: 0.35, Tune: 30}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if p.On != want.On || p.Tune != want.Tune || !near(p.MicGain, want.MicGain) || !near(p.EchoSend, want.EchoSend) ||
		!near(p.EchoFeedback, want.EchoFeedback) || !near(p.ReverbSend, want.ReverbSend) || !near(p.WarmthDb, want.WarmthDb) ||
		!near(p.BrightDb, want.BrightDb) || !near(p.DoubleMix, want.DoubleMix) {
		t.Errorf("ParamsFor = %+v, want %+v", p, want)
	}
	if got := tuneParams(30); !near(got.amount, 0.5) || !near(got.seconds, 0.3*0.49+0.005) {
		t.Errorf("tuneParams(30) = %+v", got)
	}
	if got := Normalize(Settings{Volume: 900, Tune: -4, Double: 101}); got.Volume != 200 || got.Tune != 0 || got.Double != 100 {
		t.Errorf("Normalize = %+v", got)
	}
}

func BenchmarkChainOneSecond(b *testing.B) {
	c := NewChain(ParamsFor(Settings{On: true, Volume: 100, Echo: 50, Reverb: 50, Warmth: 50, Bright: 50, Double: 50, Tune: 60}))
	x := saw(220, Rate)
	in := make([]float32, 256)
	out := make([]float32, 512)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i+256 <= len(x); i += 256 {
			for j := range in {
				in[j] = float32(x[i+j])
			}
			c.Process(in, out)
		}
	}
}

func TestFFTMatchesTheDefinition(t *testing.T) {
	const n = 16
	a := make([]complex128, n)
	for i := range a {
		a[i] = complex(math.Sin(float64(i)*0.7)+0.3*float64(i%3), 0)
	}
	want := make([]complex128, n)
	for k := range want {
		for i, v := range a {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*i)/n))
		}
	}
	tw := make([]complex128, n/2)
	for i := range tw {
		tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/n))
	}
	fft(a, tw)
	for k := range a {
		if cmplx.Abs(a[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v, want %v", k, a[k], want[k])
		}
	}
}

// A loud steady tone held for over a second is feedback; a sung note that moves is not.
func TestHowlGuard(t *testing.T) {
	feed := func(x []float64) bool {
		c := NewChain(ParamsFor(Settings{On: true, Volume: 100}))
		in := make([]float32, len(x))
		for i, v := range x {
			in[i] = float32(v)
		}
		c.Process(in, make([]float32, 2*len(x)))
		return c.Howled()
	}
	if !feed(sine(2400, 2*Rate, 0.9)) {
		t.Error("a loud 2.4 kHz tone held for 2 s was not called feedback")
	}
	if feed(sine(2400, Rate/2, 0.9)) {
		t.Error("half a second is too short to be sure")
	}
	var moving []float64
	for _, hz := range []float64{220, 247, 262, 294, 330, 349, 392, 440} {
		moving = append(moving, saw(hz, Rate/4)...)
	}
	if feed(moving) {
		t.Error("a melody was called feedback")
	}
	if feed(sine(2400, 2*Rate, 0.05)) {
		t.Error("a quiet tone was called feedback")
	}
}

// The probe against a simulated room: what it plays comes back some samples later, quieter,
// over a little noise.
func TestProbeTimesTheRoundTrip(t *testing.T) {
	for _, delay := range []int{240, 1000, 4800} {
		r := rand.New(rand.NewSource(int64(delay)))
		var played []float64
		var p probe
		result := make(chan float64, 1)
		p.start(0, result)
		var got float64
		for frame := int64(0); frame < 2*Rate; frame++ {
			x := 0.004 * (r.Float64()*2 - 1)
			if i := int(frame) - delay; i >= 0 {
				x += 0.3 * played[i]
			}
			played = append(played, p.step(frame, x))
			if p.state == probeIdle {
				got = <-result
				break
			}
		}
		want := float64(delay) / Rate
		if math.Abs(got-want) > 0.0005 {
			t.Errorf("round trip of %d samples: measured %.2f ms, want %.2f", delay, got*1000, want*1000)
		}
	}
	// Nothing comes back: no answer rather than a wrong one.
	var p probe
	result := make(chan float64, 1)
	p.start(0, result)
	for frame := int64(0); p.state != probeIdle; frame++ {
		p.step(frame, 0)
	}
	if got := <-result; got != 0 {
		t.Errorf("silence: %v", got)
	}
}
