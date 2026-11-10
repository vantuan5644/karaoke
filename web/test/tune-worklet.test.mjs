// The browser's pitch correction, run outside a browser: the same checks as the Go engine's
// tests (internal/voice/voice_test.go), so the two stay alike.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { tunePlan } from '../static/mic-core.js';

const source = readFileSync(new URL('../static/tune-worklet.js', import.meta.url), 'utf8');
const RATE = 48000;

function processor(tune) {
  let Processor;
  const sandbox = {
    sampleRate: RATE,
    registerProcessor: (_, cls) => { Processor = cls; },
    AudioWorkletProcessor: class { constructor() { this.port = { onmessage: null }; } },
    Math, Float64Array, Array, Object,
  };
  vm.runInNewContext(source, sandbox);
  const p = new Processor();
  p.port.onmessage({ data: { plan: tunePlan(tune) } });
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

test('tune moves a note to the nearest semitone', () => {
  for (const { hz, want, tune } of [
    { hz: 452.1, want: 440, tune: 100 },
    { hz: 430, want: 440, tune: 100 },
    { hz: 200, want: 196, tune: 100 },
    { hz: 452.1, want: 440 * 2 ** ((0.47 * (1 - 0.5 / 0.6)) / 12), tune: 50 },
  ]) {
    const p = processor(tune);
    const got = pitchOf(p, run(p, saw(hz, RATE / 2)));
    assert.ok(Math.abs(cents(got, want)) < 4, `${hz} Hz at tune ${tune}: ${got.toFixed(1)} Hz, want ${want.toFixed(1)}`);
  }
});

test('tune keeps the level', () => {
  const x = saw(452.1, RATE / 2);
  const y = run(processor(100), x);
  const db = 20 * Math.log10(rms(y.subarray(RATE / 4)) / rms(x.subarray(RATE / 4)));
  assert.ok(Math.abs(db) < 1, `level changed by ${db.toFixed(2)} dB`);
});
