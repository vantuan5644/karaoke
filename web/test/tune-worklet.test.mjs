// The browser's pitch correction, run outside a browser: the same checks as the Go engine's
// tests (internal/voice/voice_test.go), so the two stay alike.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { tunePlan } from '../static/mic-core.js';

const source = readFileSync(new URL('../static/tune-worklet.js', import.meta.url), 'utf8');
const RATE = 48000;

function processor(tune, speed = 100, notes = 0) {
  let Processor;
  const sandbox = {
    sampleRate: RATE,
    registerProcessor: (_, cls) => { Processor = cls; },
    AudioWorkletProcessor: class { constructor() { this.port = { onmessage: null }; } },
    Math, Float64Array, Array, Object,
  };
  vm.runInNewContext(source, sandbox);
  const p = new Processor();
  p.port.onmessage({ data: { plan: tunePlan(tune, speed, notes) } });
  return p;
}

// run feeds x through in 128-frame blocks, as a browser would.
function run(p, x) {
  const out = new Float32Array(x.length);
  for (let i = 0; i < x.length; i += 128) {
    const input = Float32Array.from(x.subarray(i, i + 128));
    const output = [new Float32Array(input.length)];
    p.process([[input]], [output]);
    out.set(output[0], i);
  }
  return out;
}

function saw(hz, n) {
  const out = new Float32Array(n);
  for (let k = 1; hz * k < 8000; k++) {
    for (let i = 0; i < n; i++) out[i] += (0.3 * Math.sin((2 * Math.PI * hz * k * i) / RATE)) / k;
  }
  return out;
}

// pitchOf is the median of what a fresh detector hears in the second half of x.
function pitchOf(p, x) {
  const d = new p.tuner.det.constructor(RATE);
  const heard = [];
  x.forEach((v, i) => {
    d.push(v);
    if (d.updated && i > x.length / 2 && d.period > 0) heard.push(RATE / d.period);
  });
  heard.sort((a, b) => a - b);
  return heard[Math.floor(heard.length / 2)] || 0;
}

const cents = (a, b) => 1200 * Math.log2(a / b);
const rms = (x) => Math.sqrt(x.reduce((s, v) => s + v * v, 0) / x.length);

test('tune 0 passes the voice straight through', () => {
  const x = saw(452, RATE / 10);
  const y = run(processor(0), x);
  assert.deepEqual(y, x);
});

const C_MAJOR = 2741;

test('tune moves a note to the nearest allowed note', () => {
  for (const { hz, want, tune, speed = 100, notes = 0 } of [
    { hz: 452.1, want: 440, tune: 100 },
    { hz: 430, want: 440, tune: 100 },
    { hz: 200, want: 196, tune: 100 },
    { hz: 452.1, want: 440 * 2 ** ((0.47 * 0.5) / 12), tune: 50 }, // half way
    { hz: 452.1, want: 440, tune: 100, speed: 70 },
    { hz: 375, want: 370, tune: 100 }, // F#4 + 24 cents: every semitone
    { hz: 375, want: 392, tune: 100, notes: C_MAJOR }, // in C major, F# is no note: G
    { hz: 440, want: 440, tune: 100, notes: C_MAJOR },
  ]) {
    const p = processor(tune, speed, notes);
    const got = pitchOf(p, run(p, saw(hz, RATE / 2)));
    assert.ok(Math.abs(cents(got, want)) < 4,
      `${hz} Hz at tune ${tune} speed ${speed} notes ${notes}: ${got.toFixed(1)} Hz, want ${want.toFixed(1)}`);
  }
});

test('tune keeps the level', () => {
  const x = saw(452.1, RATE / 2);
  const y = run(processor(100), x);
  const db = 20 * Math.log10(rms(y.subarray(RATE / 4)) / rms(x.subarray(RATE / 4)));
  assert.ok(Math.abs(db) < 1, `level changed by ${db.toFixed(2)} dB`);
});

// vibratoSaw is a sung note with vibrato: hz, swinging depth cents either way at 5.5 Hz.
function vibratoSaw(hz, depth, n) {
  const out = new Float32Array(n);
  let phase = 0;
  for (let i = 0; i < n; i++) {
    const f = hz * 2 ** ((depth / 1200) * Math.sin((2 * Math.PI * 5.5 * i) / RATE));
    phase += (2 * Math.PI * f) / RATE;
    for (let k = 1; k * f < 8000; k++) out[i] += (0.3 * Math.sin(k * phase)) / k;
  }
  return out;
}

// wobble is how far, in cents (a standard deviation), the pitch of x's second half swings.
function wobble(p, x) {
  const d = new p.tuner.det.constructor(RATE);
  const c = [];
  x.forEach((v, i) => {
    d.push(v);
    if (d.updated && i > x.length / 2 && d.period > 0) c.push(cents(RATE / d.period, 220));
  });
  const mean = c.reduce((s, v) => s + v, 0) / c.length;
  return Math.sqrt(c.reduce((s, v) => s + (v - mean) ** 2, 0) / c.length);
}

test('at full speed a vibrato comes out flattened, and Strong keeps it no wider', () => {
  // As the Go engine's TestTuneFlattensVibratoAtFullSpeed.
  const x = vibratoSaw(220, 60, 2 * RATE);
  const sung = wobble(processor(100, 100), x);
  const robot = wobble(processor(100, 100), run(processor(100, 100), x));
  const strong = wobble(processor(100, 65), run(processor(100, 65), x));
  assert.ok(robot < 0.5 * sung, `robot: ${robot.toFixed(1)} cents from ${sung.toFixed(1)}`);
  assert.ok(strong < 1.1 * sung, `strong: ${strong.toFixed(1)} cents from ${sung.toFixed(1)}`);
});
