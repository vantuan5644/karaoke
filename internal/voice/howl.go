package voice

import (
	"math"
	"math/cmplx"
)

// Feedback detection, the same test as the browser's HowlGuard (mic-core.js), on the same
// kind of spectrum an AnalyserNode gives: 2048 points, Blackman window, smoothed 0.8.

const (
	fftSize   = 2048
	fftHop    = 1024
	smoothing = 0.8
)

// spectrum is a running analyser over the chain's compressed voice.
type spectrum struct {
	ring    [fftSize]float64
	pos     int
	fresh   int
	window  [fftSize]float64
	work    []complex128
	twiddle []complex128
	mag     [fftSize / 2]float64 // smoothed magnitude
	db      [fftSize / 2]float64
	peak    float64 // the loudest sample in the last fftSize
}

func newSpectrum() *spectrum {
	s := &spectrum{work: make([]complex128, fftSize), twiddle: make([]complex128, fftSize/2)}
	for i := range s.window {
		x := float64(i) / fftSize
		s.window[i] = 0.42 - 0.5*math.Cos(2*math.Pi*x) + 0.08*math.Cos(4*math.Pi*x)
	}
	for i := range s.twiddle {
		s.twiddle[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/fftSize))
	}
	return s
}

// push adds a sample and says when a new spectrum is ready in db.
func (s *spectrum) push(x float64) bool {
	s.ring[s.pos] = x
	s.pos = (s.pos + 1) % fftSize
	s.fresh++
	if s.fresh < fftHop {
		return false
	}
	s.fresh = 0
	s.analyse()
	return true
}

func (s *spectrum) analyse() {
	s.peak = 0
	for i := range s.work {
		x := s.ring[(s.pos+i)%fftSize]
		s.peak = math.Max(s.peak, math.Abs(x))
		s.work[i] = complex(x*s.window[i], 0)
	}
	fft(s.work, s.twiddle)
	for k := range s.mag {
		m := cmplx.Abs(s.work[k]) / fftSize
		s.mag[k] = smoothing*s.mag[k] + (1-smoothing)*m
		if s.mag[k] > 0 {
			s.db[k] = 20 * math.Log10(s.mag[k])
		} else {
			s.db[k] = -200
		}
	}
}

// fft is an in-place iterative radix-2 transform; len(a) is a power of two and twiddle
// holds its first len(a)/2 roots of unity.
func fft(a []complex128, twiddle []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < size/2; k++ {
				t := twiddle[k*step] * a[start+k+size/2]
				a[start+k+size/2] = a[start+k] - t
				a[start+k] += t
			}
		}
	}
}

// howlScore is the share of a spectrum's energy in one narrow peak, and that peak's bin.
func howlScore(db []float64) (float64, int) {
	peak, bin := math.Inf(-1), 0
	for i := 2; i < len(db); i++ {
		if db[i] > peak {
			peak, bin = db[i], i
		}
	}
	if peak < -25 {
		return 0, bin // too quiet to be feedback
	}
	total, near := 0.0, 0.0
	for i := 2; i < len(db); i++ {
		power := math.Pow(10, db[i]/10)
		total += power
		if i >= bin-2 && i <= bin+2 {
			near += power
		}
	}
	if total == 0 {
		return 0, bin
	}
	return near / total, bin
}

// howlGuard waits for a loud, narrow peak that stays on one frequency for a full second:
// feedback does that, a held note does not.
type howlGuard struct {
	since float64 // seconds
	bin   int
}

func newHowlGuard() howlGuard { return howlGuard{bin: -1} }

func (g *howlGuard) update(db []float64, level, now float64) bool {
	score, bin := howlScore(db)
	if score < 0.7 || level < 0.5 {
		g.bin = -1
		return false
	}
	if g.bin < 0 || bin-g.bin > 1 || g.bin-bin > 1 {
		g.since, g.bin = now, bin
		return false
	}
	if now-g.since < 1 {
		return false
	}
	g.bin = -1
	return true
}
