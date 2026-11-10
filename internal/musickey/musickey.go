// Package musickey works out a song's key from its sound, so pitch correction can pull a
// singer to the song's own notes rather than to any semitone. It adds up how much of each of
// the twelve notes the song plays (its chroma) and matches that against the profiles of the
// 24 major and minor keys (Krumhansl and Kessler's).
package musickey

import (
	"math"
	"math/cmplx"
)

// Rate is the sample rate Analyze expects: plenty for notes up to a few kHz.
const Rate = 11025

// Key is a song's key, and the notes a singer is pulled to in it.
type Key struct {
	Tonic int    `json:"tonic"` // 0..11, C first
	Minor bool   `json:"minor"`
	Name  string `json:"name"` // "A minor"
	// Notes is the key's scale, and any other note the song leans on as much (the raised
	// seventh of a minor key, most often), as 12 bits, C first.
	Notes int `json:"notes"`
	// Fit is how well the song's notes match the key's profile: a correlation, -1..1.
	// Above about 0.6 the key is clear; music that changes key or barely has one scores less.
	Fit float64 `json:"fit"`
}

const (
	frameSize = 8192 // 0.74 s: fine enough to tell semitones apart at 65 Hz
	hop       = 4096
	lowNote   = 33  // A1, 55 Hz: the lowest note counted
	highNote  = 93  // A6, 1760 Hz: the highest note counted
	topNote   = 105 // A7: notes up to here are heard, so their harmonics' share can come off
	// extraShare is how much of an outside note, against the scale's average (both above the
	// song's floor), makes it one of the song's notes too.
	extraShare = 0.5
	quiet      = 1e-3 // a frame's RMS below this is silence
)

// The key profiles: how strongly each note of the octave, counted up from the tonic,
// belongs to a major or a minor key, as listeners rated them.
var (
	majorProfile = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	minorProfile = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
	majorScale   = []int{0, 2, 4, 5, 7, 9, 11}
	minorScale   = []int{0, 2, 3, 5, 7, 8, 10} // natural minor: the same notes as its relative major
	majorNames   = [12]string{"C", "D♭", "D", "E♭", "E", "F", "F♯", "G", "A♭", "A", "B♭", "B"}
	minorNames   = [12]string{"C", "C♯", "D", "E♭", "E", "F", "F♯", "G", "G♯", "A", "B♭", "B"}
)

// Name is a key's name, as the TV shows it.
func Name(tonic int, minor bool) string {
	if minor {
		return minorNames[tonic] + " minor"
	}
	return majorNames[tonic] + " major"
}

// Analyze finds the key of mono audio at Rate; false when there is no music in it.
func Analyze(x []float32) (Key, bool) {
	chroma, ok := Chroma(x)
	if !ok {
		return Key{}, false
	}
	return keyOf(chroma), true
}

// Chroma is how much of each note (C first) the audio has, each tonal frame counting the same
// however loud it is; false when no frame has sound.
//
// Each frame's spectrum is gathered into semitones first, and each note's third, fifth and
// sixth harmonics are taken back out of the notes they land on: an instrument's third
// harmonic sounds a fifth above it, so without this every song leans toward the key a fifth
// up, its dominant.
func Chroma(x []float32) ([12]float64, bool) {
	var total [12]float64
	window := make([]float64, frameSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/frameSize)
	}
	// Which semitone each FFT bin belongs to, and how near its centre it is.
	type binNote struct {
		note   int // index into the semitone spectrum
		weight float64
	}
	var bins []binNote
	var binIndex []int
	for k := 1; k < frameSize/2; k++ {
		hz := float64(k) * Rate / frameSize
		midi := 69 + 12*math.Log2(hz/440)
		near := math.Round(midi)
		if near < lowNote || near > topNote {
			continue
		}
		// Full weight at a note's centre, none half way to the next.
		bins = append(bins, binNote{note: int(near) - lowNote, weight: math.Cos(math.Pi * (midi - near))})
		binIndex = append(binIndex, k)
	}
	frame := make([]complex128, frameSize)
	var semis [topNote - lowNote + 1]float64
	frames := 0
	for start := 0; start+frameSize <= len(x); start += hop {
		energy := 0.0
		for i := range frame {
			v := float64(x[start+i])
			energy += v * v
			frame[i] = complex(v*window[i], 0)
		}
		if math.Sqrt(energy/frameSize) < quiet {
			continue
		}
		fft(frame)
		clear(semis[:])
		for i, b := range bins {
			semis[b.note] += cmplx.Abs(frame[binIndex[i]]) * b.weight
		}
		for m := range semis { // low notes first: a note's own harmonics are gone before it lends its own
			for _, h := range harmonics {
				if up := m + h.semitones; up < len(semis) {
					semis[up] = math.Max(0, semis[up]-h.share*semis[m])
				}
			}
		}
		var c [12]float64
		sum := 0.0
		for m := 0; m <= highNote-lowNote; m++ {
			c[(m+lowNote)%12] += semis[m]
			sum += semis[m]
		}
		if sum == 0 {
			continue
		}
		for pc := range c {
			total[pc] += c[pc] / sum
		}
		frames++
	}
	return total, frames > 0
}

// harmonics are the overtones that sound as another note: how far up, and how strong
// against the note itself. (The 2nd and 4th are octaves, the same note.)
var harmonics = []struct {
	semitones int
	share     float64
}{
	{19, 0.5},  // 3rd: an octave and a fifth
	{28, 0.3},  // 5th: two octaves and a major third
	{31, 0.25}, // 6th: two octaves and a fifth
}

// keyOf picks the key whose profile the chroma matches best.
func keyOf(chroma [12]float64) Key {
	best := Key{Fit: math.Inf(-1)}
	for tonic := 0; tonic < 12; tonic++ {
		for _, minor := range []bool{false, true} {
			profile := majorProfile
			if minor {
				profile = minorProfile
			}
			var rotated [12]float64
			for i := range rotated {
				rotated[(tonic+i)%12] = profile[i]
			}
			if r := correlation(chroma, rotated); r > best.Fit {
				best = Key{Tonic: tonic, Minor: minor, Fit: r}
			}
		}
	}
	best.Name = Name(best.Tonic, best.Minor)
	best.Notes = notesOf(best, chroma)
	return best
}

// notesOf is the key's scale plus any other note the song uses about as much as the scale's
// own notes. Both are measured above the song's quietest note, its floor: drums and noise
// lift every note alike.
func notesOf(k Key, chroma [12]float64) int {
	scale := majorScale
	if k.Minor {
		scale = minorScale
	}
	floor := math.Inf(1)
	for _, v := range chroma {
		floor = math.Min(floor, v)
	}
	notes, sum := 0, 0.0
	for _, step := range scale {
		pc := (k.Tonic + step) % 12
		notes |= 1 << pc
		sum += chroma[pc] - floor
	}
	mean := sum / float64(len(scale))
	for pc, v := range chroma {
		if notes&(1<<pc) == 0 && v-floor >= extraShare*mean {
			notes |= 1 << pc
		}
	}
	return notes
}

func correlation(a, b [12]float64) float64 {
	var ma, mb float64
	for i := range a {
		ma += a[i] / 12
		mb += b[i] / 12
	}
	var num, da, db float64
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// fft is an in-place radix-2 FFT; len(a) must be a power of two.
func fft(a []complex128) {
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
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u := a[start+k]
				v := a[start+k+size/2] * w
				a[start+k] = u + v
				a[start+k+size/2] = u - v
				w *= step
			}
		}
	}
}
