package voice

import "math"

// Settings are the singer's knobs, the same ones the browser's mic has (mic-core.js).
type Settings struct {
	On       bool   `json:"on"`
	Volume   int    `json:"volume"` // 0..200 (%)
	Echo     int    `json:"echo"`   // 0..100
	Reverb   int    `json:"reverb"` // 0..100
	Warmth   int    `json:"warmth"` // 0..100: body in the low mids
	Bright   int    `json:"bright"` // 0..100: presence and air
	Double   int    `json:"double"` // 0..100: a second voice beside the first
	Tune     int    `json:"tune"`   // 0..100: how far pitch correction pulls a note, 0 is off
	Speed    int    `json:"speed"`  // 0..100: how fast it pulls; 100 snaps at once
	Notes    int    `json:"notes"`  // the notes it pulls to: 12 bits, C first; 0 is every semitone
	Boost    int    `json:"boost"`  // 0..24 dB: the mic's level going in, before anything else
	DeviceID string `json:"deviceId"`

	// SeenHowls is how many feedback cuts the sender knew of when it sent these. A sender
	// that missed the latest one is sending the volume from before it, which Set keeps from
	// undoing the cut. Nil: the sender doesn't know (a page just opened).
	SeenHowls *int `json:"seenHowls,omitempty"`
}

// Defaults are what a new mic starts with.
var Defaults = Settings{Volume: 100, Echo: 35, Reverb: 25, Speed: 50}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

// Normalize keeps settings inside what the chain accepts.
func Normalize(s Settings) Settings {
	s.Volume = clampInt(s.Volume, 0, 200)
	s.Echo = clampInt(s.Echo, 0, 100)
	s.Reverb = clampInt(s.Reverb, 0, 100)
	s.Warmth = clampInt(s.Warmth, 0, 100)
	s.Bright = clampInt(s.Bright, 0, 100)
	s.Double = clampInt(s.Double, 0, 100)
	s.Tune = clampInt(s.Tune, 0, 100)
	s.Speed = clampInt(s.Speed, 0, 100)
	s.Notes = clampInt(s.Notes, 0, allNotes)
	s.Boost = clampInt(s.Boost, 0, MaxBoostDb)
	if len(s.DeviceID) > 200 {
		s.DeviceID = ""
	}
	return s
}

// The fixed part of the chain, as in the browser's CHAIN.
const (
	highpassHz     = 90.0
	warmthHz       = 220.0 // low shelf
	warmthMaxDb    = 6.0
	mudHz          = 300.0
	mudGainDb      = -3.0
	presenceHz     = 4000.0
	presenceGainDb = 3.0
	brightHz       = 5000.0 // high shelf
	brightMaxDb    = 8.0
	echoSeconds    = 0.16
	doubleMaxMix   = 0.7
	MaxBoostDb     = 24
)

// Params are the knobs turned into the chain's numbers.
type Params struct {
	On                     bool
	BoostGain              float64 // into the chain
	MicGain                float64 // out of it
	EchoSend, EchoFeedback float64
	ReverbSend             float64
	WarmthDb, BrightDb     float64
	DoubleMix              float64
	Tune, Speed, Notes     int
}

// ParamsFor is the same arithmetic as the browser's params().
func ParamsFor(s Settings) Params {
	n := Normalize(s)
	return Params{
		On:           n.On,
		BoostGain:    math.Pow(10, float64(n.Boost)/20),
		MicGain:      float64(n.Volume) / 100,
		EchoSend:     float64(n.Echo) / 100 * 0.7,
		EchoFeedback: 0.12 + float64(n.Echo)/100*0.33,
		ReverbSend:   float64(n.Reverb) / 100 * 0.85,
		WarmthDb:     float64(n.Warmth) / 100 * warmthMaxDb,
		BrightDb:     float64(n.Bright) / 100 * brightMaxDb,
		DoubleMix:    float64(n.Double) / 100 * doubleMaxMix,
		Tune:         n.Tune,
		Speed:        n.Speed,
		Notes:        n.Notes,
	}
}

// Chain is the whole vocal chain for one mono mic, giving stereo out.
type Chain struct {
	highpass, warmth, mud, presence, bright biquad
	comp                                    compressor
	tune                                    *tuner
	double                                  doubler
	echo                                    echo
	reverb                                  *reverb
	limit                                   limiter

	p      Params
	boost  float64 // smoothed boost
	gain   float64 // smoothed mic gain
	sends  [4]float64
	peak   float64 // the loudest sample since the last Peak
	spec   *spectrum
	guard  howlGuard
	clock  float64 // seconds of audio so far
	howled bool
}

// NewChain makes a chain set up for p.
func NewChain(p Params) *Chain {
	c := &Chain{tune: newTuner(), double: newDoubler(), echo: newEcho(), reverb: newReverb(0.84, 0.25), limit: newLimiter(),
		spec: newSpectrum(), guard: newHowlGuard()}
	c.highpass.highpass(highpassHz, 0.7)
	c.mud.peaking(mudHz, 1, mudGainDb)
	c.presence.peaking(presenceHz, 0.8, presenceGainDb)
	c.comp = newCompressor(-24, 6, 3, 0.003, 0.15, 0)
	c.comp.makeup = c.comp.autoMakeup()
	c.echo.delay = echoSeconds * Rate
	c.Set(p)
	c.boost = p.BoostGain
	return c
}

// Set changes the knobs; gains glide there rather than jump, so a knob never clicks.
func (c *Chain) Set(p Params) {
	c.p = p
	c.warmth.shelf(warmthHz, p.WarmthDb, true)
	c.bright.shelf(brightHz, p.BrightDb, false)
	c.echo.feedback = p.EchoFeedback
	c.tune.setTune(float64(p.Tune), float64(p.Speed), p.Notes)
}

// Peak returns the loudest level since it was last asked, for the meter.
func (c *Chain) Peak() float64 {
	p := c.peak
	c.peak = 0
	return p
}

// Howled says whether the voice has started to feed back since it was last asked.
func (c *Chain) Howled() bool {
	h := c.howled
	c.howled = false
	return h
}

// glide is a one-pole step toward a target, about 20 ms.
const glide = 0.001

// Process runs mono in through the chain into interleaved stereo out (len(out) == 2*len(in)).
func (c *Chain) Process(in []float32, out []float32) {
	target := 0.0
	if c.p.On {
		target = c.p.MicGain
	}
	sendTargets := [4]float64{c.p.EchoSend, c.p.ReverbSend, c.p.DoubleMix, 0}
	for i, s := range in {
		c.boost += (c.p.BoostGain - c.boost) * glide
		x := c.highpass.process(float64(s) * c.boost)
		x = c.tune.process(x)
		x = c.warmth.process(x)
		x = c.mud.process(x)
		x = c.presence.process(x)
		x = c.bright.process(x)
		// The meter and the feedback guard listen before the compressor, which would
		// otherwise hold a howl under the guard's threshold.
		if a := math.Abs(x); a > c.peak {
			c.peak = a
		}
		c.clock += 1.0 / Rate
		if c.spec.push(x) && c.p.On && c.guard.update(c.spec.db[:], c.spec.peak, c.clock) {
			c.howled = true
		}
		x = c.comp.process(x)
		c.gain += (target - c.gain) * glide
		for k := range c.sends {
			c.sends[k] += (sendTargets[k] - c.sends[k]) * glide
		}
		dry := x * c.gain

		l, r := dry, dry
		if c.sends[2] > 1e-4 {
			dl, dr := c.double.process(dry)
			l += dl * c.sends[2]
			r += dr * c.sends[2]
		} else {
			c.double.line.write(dry)
		}
		e := c.echo.process(dry * c.sends[0])
		rl, rr := c.reverb.process(dry * c.sends[1])
		l, r = c.limit.process(l+e+rl, r+e+rr)
		out[2*i] = float32(l)
		out[2*i+1] = float32(r)
	}
}
