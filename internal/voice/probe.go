package voice

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"
)

// The delay probe, as the browser's (mic-worklet.js): listen to the room, play a short click
// through the speakers, and count samples until the mic hears it. The figure covers
// everything between singing and hearing it back: PipeWire, the devices, the air.

const (
	clickSeconds = 0.003
	clickHz      = 2500
)

const (
	probeIdle = iota
	probeQuiet
	probeListen
)

type probe struct {
	state                          int
	noiseFrom, quietUntil, clickAt int64
	noise, threshold               float64
	result                         chan float64 // seconds; 0 when nothing came back
}

// start begins a measurement at frame; the answer goes to result.
func (p *probe) start(frame int64, result chan float64) {
	p.state = probeQuiet
	p.noiseFrom = frame + int64(0.25*Rate) // let the last click's tail die away
	p.quietUntil = p.noiseFrom + int64(0.15*Rate)
	p.noise = 0
	p.result = result
}

// step takes the mic's sample at frame and returns what to add to the speakers.
func (p *probe) step(frame int64, x float64) float64 {
	switch p.state {
	case probeIdle:
		return 0
	case probeQuiet:
		if frame >= p.noiseFrom {
			p.noise = math.Max(p.noise, math.Abs(x))
		}
		if frame < p.quietUntil {
			return 0
		}
		// Loud enough to stand out from the room, never below a floor noise could reach.
		p.threshold = math.Min(0.6, math.Max(0.03, p.noise*4))
		p.clickAt = frame
		p.state = probeListen
	}
	t := float64(frame-p.clickAt) / Rate
	y := 0.0
	if t < clickSeconds {
		y = 0.8 * math.Sin(math.Pi*t/clickSeconds) * math.Sin(2*math.Pi*clickHz*t)
	}
	// Anything before 1 ms is the click leaking straight through, not the room.
	switch {
	case t > 0.001 && math.Abs(x) > p.threshold:
		p.finish(t)
	case t > 0.8:
		p.finish(0)
	}
	return y
}

func (p *probe) finish(seconds float64) {
	select {
	case p.result <- seconds:
	default:
	}
	p.state = probeIdle
}

// ErrNotRunning is Measure on a mic that is off.
var ErrNotRunning = errors.New("the mic is off")

// Measure plays three clicks and returns the median round trip in milliseconds, or 0 when
// no click came back (speakers too quiet, or headphones).
func (e *Engine) Measure(ctx context.Context) (float64, error) {
	e.measuring.Lock()
	defer e.measuring.Unlock()
	var heard []float64
	for i := 0; i < 3; i++ {
		e.mu.Lock()
		p := e.cur
		e.mu.Unlock()
		if p == nil {
			return 0, ErrNotRunning
		}
		result := make(chan float64, 1)
		p.measure.Store(&result)
		select {
		case s := <-result:
			if s > 0 {
				heard = append(heard, s*1000)
			}
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if len(heard) == 0 {
		return 0, nil
	}
	sort.Float64s(heard)
	mid := len(heard) / 2
	ms := heard[mid]
	if len(heard)%2 == 0 {
		ms = (heard[mid-1] + heard[mid]) / 2
	}
	return math.Round(ms), nil
}
