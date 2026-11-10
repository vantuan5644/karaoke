package musickey

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/cmplx"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// notes is a set of pitch classes as Key.Notes has them.
func notes(pcs ...int) int {
	n := 0
	for _, pc := range pcs {
		n |= 1 << (pc % 12)
	}
	return n
}

// chords plays each chord (MIDI notes) for a second and a half, with its root an octave down
// in the bass, each note a few harmonics strong, as a backing track would.
func chords(progression [][]int, times int) []float32 {
	const each = Rate * 3 / 2
	var x []float32
	r := rand.New(rand.NewSource(1))
	for t := 0; t < times; t++ {
		for _, chord := range progression {
			voices := append([]int{chord[0] - 12}, chord...)
			for i := 0; i < each; i++ {
				v := 0.0
				for _, m := range voices {
					hz := 440 * math.Pow(2, float64(m-69)/12)
					for h := 1; h <= 5 && hz*float64(h) < Rate/2; h++ {
						v += math.Sin(2*math.Pi*hz*float64(h)*float64(i)/Rate) / float64(h)
					}
				}
				x = append(x, float32(0.05*v+0.005*(r.Float64()*2-1)))
			}
		}
	}
	return x
}

func TestAMajorSongIsHeardInItsKey(t *testing.T) {
	// D major: D, G, A, Bm (I IV V vi).
	k, ok := Analyze(chords([][]int{{62, 66, 69}, {67, 71, 74}, {69, 73, 76}, {71, 74, 78}}, 4))
	if !ok {
		t.Fatal("no key heard")
	}
	if k.Name != "D major" || k.Notes != notes(2, 4, 6, 7, 9, 11, 1) {
		t.Errorf("got %s, notes %012b (fit %.2f); want D major, D E F# G A B C#", k.Name, k.Notes, k.Fit)
	}
	if k.Fit < 0.6 {
		t.Errorf("a plain I IV V vi should fit its key clearly, fit %.2f", k.Fit)
	}
}

func TestAMinorSongKeepsItsRaisedSeventh(t *testing.T) {
	// A minor with its dominant E major (G#): Am, Dm, E, Am.
	k, ok := Analyze(chords([][]int{{57, 60, 64}, {62, 65, 69}, {64, 68, 71}, {57, 60, 64}}, 4))
	if !ok {
		t.Fatal("no key heard")
	}
	if k.Name != "A minor" {
		t.Errorf("got %s (fit %.2f), want A minor", k.Name, k.Fit)
	}
	scale := notes(9, 11, 0, 2, 4, 5, 7)
	if k.Notes&scale != scale || k.Notes&notes(8) == 0 {
		t.Errorf("notes %012b: want A minor's scale and the G# of its E chord", k.Notes)
	}
}

func TestTransposedSongMovesItsKey(t *testing.T) {
	up := func(p [][]int, n int) [][]int {
		out := make([][]int, len(p))
		for i, c := range p {
			for _, m := range c {
				out[i] = append(out[i], m+n)
			}
		}
		return out
	}
	base := [][]int{{60, 64, 67}, {65, 69, 72}, {67, 71, 74}, {60, 64, 67}} // C F G C
	for shift := 0; shift < 12; shift++ {
		k, _ := Analyze(chords(up(base, shift), 2))
		if k.Tonic != shift || k.Minor {
			t.Errorf("C major up %d: heard %s", shift, k.Name)
		}
	}
}

func TestSilenceHasNoKey(t *testing.T) {
	if _, ok := Analyze(make([]float32, Rate*5)); ok {
		t.Error("heard a key in silence")
	}
	if _, ok := Analyze(nil); ok {
		t.Error("heard a key in nothing")
	}
}

func TestFFTMatchesTheDefinition(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	a := make([]complex128, 64)
	for i := range a {
		a[i] = complex(r.Float64()-0.5, 0)
	}
	want := make([]complex128, len(a))
	for k := range want {
		for n, v := range a {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*n)/float64(len(a))))
		}
	}
	fft(a)
	for k := range a {
		if cmplx.Abs(a[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v, want %v", k, a[k], want[k])
		}
	}
}

func TestFinderHearsAFileOnce(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	raw := filepath.Join(dir, "song.f32")
	x := chords([][]int{{62, 66, 69}, {67, 71, 74}, {69, 73, 76}, {71, 74, 78}}, 3)
	b := make([]byte, 4*len(x))
	for i, v := range x {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	if err := os.WriteFile(raw, b, 0o644); err != nil {
		t.Fatal(err)
	}
	song := filepath.Join(dir, "song.mp3")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "f32le", "-ar", "11025", "-ac", "1", "-i", raw, "-ar", "44100", "-ac", "2", song).CombinedOutput(); err != nil {
		t.Fatalf("making the test song: %v %s", err, out)
	}
	f := &Finder{FFmpeg: ffmpeg}
	k, err := f.Key(context.Background(), song)
	if err != nil || k.Name != "D major" {
		t.Fatalf("Key = %+v, %v; want D major", k, err)
	}
	f.FFmpeg = "/nonexistent" // a second ask must not run ffmpeg again,
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(song, later, later); err != nil { // even after the cache touched the file
		t.Fatal(err)
	}
	if again, err := f.Key(context.Background(), song); err != nil || again != k {
		t.Errorf("asked again: %+v, %v", again, err)
	}
	if _, err := f.Key(context.Background(), filepath.Join(dir, "missing.mp3")); err == nil {
		t.Error("a missing file had a key")
	}
	silent := filepath.Join(dir, "silent.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "3", silent).CombinedOutput(); err != nil {
		t.Fatalf("making a silent file: %v %s", err, out)
	}
	f.FFmpeg = ffmpeg
	if _, err := f.Key(context.Background(), silent); err != ErrNoMusic {
		t.Errorf("silence: %v, want ErrNoMusic", err)
	}

	// Waiting for another file to be heard gives up when the caller does.
	f.busy <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	other := filepath.Join(dir, "other.mp3")
	os.WriteFile(other, []byte("not heard yet"), 0o644)
	if _, err := f.Key(ctx, other); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiting past the deadline: %v", err)
	}
	<-f.busy
}
