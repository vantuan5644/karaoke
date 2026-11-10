// Pitch correction for the browser's mic, the same method as the Go engine's
// (internal/voice/pitch.go): YIN finds the note being sung, and TD-PSOLA moves it toward the
// nearest semitone by laying two-period grains down closer together or further apart. Grains
// are read at normal speed, so the voice keeps its formants. While it is on the voice is
// DELAY_MS late; at amount 0 it passes straight through.

const DELAY_MS = 8;
const DECIMATE = 3;
const MIN_HZ = 70;
const MAX_HZ = 800;
const YIN_WINDOW = 512;
const YIN_HOP = 80;
const YIN_CUTOFF = 0.15;
const QUIET_RMS = 0.003;

class Biquad {
  constructor() { this.b0 = 1; this.b1 = this.b2 = this.a1 = this.a2 = 0; this.x1 = this.x2 = this.y1 = this.y2 = 0; }

  lowpass(rate, hz, q) {
    const w = (2 * Math.PI * hz) / rate;
    const c = Math.cos(w);
    const alpha = Math.sin(w) / (2 * q);
    const a0 = 1 + alpha;
    this.b0 = (1 - c) / 2 / a0; this.b1 = (1 - c) / a0; this.b2 = (1 - c) / 2 / a0;
    this.a1 = (-2 * c) / a0; this.a2 = (1 - alpha) / a0;
  }

  process(x) {
    const y = this.b0 * x + this.b1 * this.x1 + this.b2 * this.x2 - this.a1 * this.y1 - this.a2 * this.y2;
    this.x2 = this.x1; this.x1 = x;
    this.y2 = this.y1; this.y1 = y;
    return y;
  }
}

// Detector is YIN (de Cheveigné and Kawahara, 2002) on a decimated copy of the input.
class Detector {
  constructor(rate) {
    const detectRate = rate / DECIMATE;
    this.minLag = Math.floor(detectRate / MAX_HZ);
    this.maxLag = Math.ceil(detectRate / MIN_HZ);
    this.buf = new Float64Array(1024);
    this.span = new Float64Array(YIN_WINDOW + this.maxLag + 1);
    this.diff = new Float64Array(this.maxLag + 2);
    this.pos = 0;
    this.phase = 0;
    this.count = 0;
    this.period = 0; // in input samples; 0 when there is no note
    this.updated = false;
    this.aa1 = new Biquad();
    this.aa2 = new Biquad();
    this.aa1.lowpass(rate, 5000, 0.54);
    this.aa2.lowpass(rate, 5000, 1.31);
  }

  push(x) {
    this.updated = false;
    const y = this.aa2.process(this.aa1.process(x));
    if (++this.phase < DECIMATE) return;
    this.phase = 0;
    this.buf[this.pos] = y;
    this.pos = (this.pos + 1) % this.buf.length;
    if (++this.count >= YIN_HOP) {
      this.count = 0;
      this.period = this.estimate() * DECIMATE;
      this.updated = true;
    }
  }

  estimate() {
    const n = this.buf.length;
    const x = this.span;
    const span = x.length;
    const start = (this.pos - span + n) % n;
    for (let i = 0; i < span; i++) x[i] = this.buf[(start + i) % n];
    let energy = 0;
    for (let j = 0; j < YIN_WINDOW; j++) energy += x[j] * x[j];
    if (Math.sqrt(energy / YIN_WINDOW) < QUIET_RMS) return 0;
    const d = this.diff;
    d[0] = 1;
    let running = 0;
    for (let tau = 1; tau <= this.maxLag; tau++) {
      let sum = 0;
      for (let j = 0; j < YIN_WINDOW; j++) {
        const delta = x[j] - x[j + tau];
        sum += delta * delta;
      }
      running += sum;
      d[tau] = running > 0 ? (sum * tau) / running : 1;
    }
    let tau = 0;
    for (let t = this.minLag; t < this.maxLag; t++) {
      if (d[t] < YIN_CUTOFF) {
        while (t + 1 < this.maxLag && d[t + 1] < d[t]) t++;
        tau = t;
        break;
      }
    }
    if (tau === 0) return 0;
    const a = d[tau - 1];
    const b = d[tau];
    const c = d[tau + 1];
    const den = a - 2 * b + c;
    const shift = den > 0 ? (0.5 * (a - c)) / den : 0;
    return tau + Math.max(-0.5, Math.min(0.5, shift));
  }
}

// allowed says whether a MIDI note is one of notes (12 bits, C first; 0 allows every note).
function allowed(note, notes) {
  return notes === 0 || (notes & (1 << (((note % 12) + 12) % 12))) !== 0;
}

// nearestNote is the allowed note nearest a pitch; a tie goes to the lower.
function nearestNote(midi, notes) {
  let best = Math.round(midi);
  let dist = Infinity;
  for (let n = Math.floor(midi) - 6; n <= Math.ceil(midi) + 6; n++) {
    const d = Math.abs(midi - n);
    if (d < dist && allowed(n, notes)) [best, dist] = [n, d];
  }
  return best;
}

const NOTE_HOLD = 0.3; // semitones nearer another note before leaving the held one

// noteFor holds the current note until the singer is clearly nearer another.
function noteFor(midi, current, notes) {
  const best = nearestNote(midi, notes);
  if (current > 0 && allowed(current, notes) && Math.abs(midi - current) < Math.abs(midi - best) + NOTE_HOLD) return current;
  return best;
}

class Tuner {
  constructor(rate) {
    this.rate = rate;
    this.det = new Detector(rate);
    this.line = new Float64Array(4096);
    this.pos = 0;
    this.grains = Array.from({ length: 4 }, () => ({ active: false, pos: 0, length: 1, delay: 0 }));
    this.centre = (DELAY_MS * rate) / 1000;
    this.unvoicedHop = rate / 100;
    this.mix = 0;
    this.amount = 0;
    this.smooth = 0;
    this.notes = 0;
    this.reset();
  }

  // setPlan takes mic-core's tunePlan: how much of the way to the note, how quickly, and to
  // which notes.
  setPlan({ amount = 0, seconds = 0, notes = 0 } = {}) {
    this.amount = Math.max(0, Math.min(1, amount));
    this.smooth = seconds > 0 ? 1 - Math.exp(-1 / (seconds * this.rate)) : 1;
    this.notes = Number.isInteger(notes) ? notes & 0xfff : 0;
  }

  reset() {
    for (const g of this.grains) g.active = false;
    this.line.fill(0);
    this.next = 0;
    this.last = this.centre;
    this.ratio = 1;
    this.target = 1;
    this.note = 0;
    this.silent = 0;
  }

  read(delay) {
    const n = this.line.length;
    const d = Math.max(1, Math.min(n - 2, delay));
    let p = this.pos - d;
    while (p < 0) p += n;
    const i = Math.floor(p);
    const frac = p - i;
    const a = this.line[i % n];
    const b = this.line[(i + 1) % n];
    return a + (b - a) * frac;
  }

  process(x) {
    const enabled = this.amount > 0;
    if (!enabled && this.mix === 0) return x;
    this.mix += ((enabled ? 1 : 0) - this.mix) * (1 - Math.exp(-1 / (0.03 * this.rate)));
    if (!enabled && this.mix < 1e-4) {
      this.mix = 0;
      this.reset();
      return x;
    }
    this.det.push(x);
    this.line[this.pos] = x;
    this.pos = (this.pos + 1) % this.line.length;
    if (this.det.updated) this.retarget();
    this.ratio += (this.target - this.ratio) * this.smooth;
    if (--this.next <= 0) this.startGrain();
    let acc = 0;
    let weights = 0;
    for (const g of this.grains) {
      if (!g.active) continue;
      const w = 0.5 - 0.5 * Math.cos((2 * Math.PI * g.pos) / g.length);
      acc += w * this.read(g.delay);
      weights += w;
      if (++g.pos >= g.length) g.active = false;
    }
    const out = weights > 1e-6 ? acc / weights : 0;
    return x + (out - x) * this.mix;
  }

  retarget() {
    const period = this.det.period;
    if (period <= 0) {
      this.silent += YIN_HOP * DECIMATE;
      if (this.silent > this.rate / 10) this.note = 0;
      this.target = 1;
      return;
    }
    this.silent = 0;
    const midi = 69 + 12 * Math.log2(this.rate / period / 440);
    this.note = noteFor(midi, this.note, this.notes);
    this.target = Math.pow(2, (this.amount * (this.note - midi)) / 12);
  }

  startGrain() {
    const period = this.det.period > 0 ? this.det.period : this.unvoicedHop;
    const hop = period / this.ratio;
    let delay = this.last + hop;
    delay -= Math.round((delay - this.centre) / period) * period;
    delay = Math.max(2, Math.min(this.line.length - 4, delay));
    let slot = this.grains.find((g) => !g.active);
    if (!slot) slot = this.grains.reduce((a, b) => (b.pos / b.length > a.pos / a.length ? b : a));
    Object.assign(slot, { active: true, pos: 0, length: Math.max(32, Math.round(2 * period)), delay });
    this.last = delay;
    this.next += hop;
  }
}

class PitchTuner extends AudioWorkletProcessor {
  constructor() {
    super();
    this.tuner = new Tuner(sampleRate);
    this.port.onmessage = (e) => {
      if (e.data?.plan) this.tuner.setPlan(e.data.plan);
      if (e.data?.stop) this.stopped = true; // the mic is done with it: let it go
    };
  }

  process(inputs, outputs) {
    const input = inputs[0]?.[0];
    const output = outputs[0];
    const out = output[0];
    for (let j = 0; j < out.length; j++) out[j] = this.tuner.process(input ? input[j] : 0);
    for (let ch = 1; ch < output.length; ch++) output[ch].set(out);
    return !this.stopped;
  }
}

registerProcessor('pitch-tuner', PitchTuner);
