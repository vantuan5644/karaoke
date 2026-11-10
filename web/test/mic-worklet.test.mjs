// The delay probe, run against a simulated room: whatever it plays comes back to its input
// some frames later, quieter, over a little noise.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('../static/mic-worklet.js', import.meta.url), 'utf8');

function room({ delayFrames, gain = 0.3, noise = 0.005, sampleRate = 48000, block = 128 }) {
  let Processor;
  const sandbox = {
    sampleRate,
    currentFrame: 0,
    registerProcessor: (_, cls) => { Processor = cls; },
    AudioWorkletProcessor: class { constructor() { this.port = { postMessage: (m) => this.sent.push(m), onmessage: null }; this.sent = []; } },
    Math,
  };
  vm.runInNewContext(source, sandbox);
  const probe = new Processor();
  const played = [];
  let seed = 7;
  const random = () => ((seed = (seed * 16807) % 2147483647) / 2147483647) * 2 - 1;
  const measure = () => {
    probe.port.onmessage({ data: { type: 'measure' } });
    for (let blocks = 0; blocks < 2000; blocks++) {
      const start = sandbox.currentFrame;
      const input = new Float32Array(block).map((_, j) => {
        const f = start + j - delayFrames;
        return (f >= 0 && f < played.length ? played[f] * gain : 0) + random() * noise;
      });
      const output = [new Float32Array(block), new Float32Array(block)];
      probe.process([[input]], [output]);
      played.push(...output[0]);
      sandbox.currentFrame += block;
      const result = probe.sent.shift();
      if (result) return result.seconds;
    }
    throw new Error('the probe never answered');
  };
  return { measure };
}

test('the probe times the round trip', () => {
  for (const ms of [8, 21, 47, 130]) {
    const r = room({ delayFrames: Math.round(48 * ms) });
    const seconds = r.measure();
    assert.ok(Math.abs(seconds * 1000 - ms) < 1, `${ms} ms measured as ${seconds * 1000}`);
    assert.ok(Math.abs(r.measure() * 1000 - ms) < 1, 'and again');
  }
});

test('the probe gives up when nothing comes back', () => {
  assert.equal(room({ delayFrames: 960, gain: 0 }).measure(), null);
});

test('loud room noise does not count as the click', () => {
  // Noise at 0.1 sets a threshold of 0.4, which the click (0.8 x 0.3 = 0.24) never reaches,
  // so the honest answer is null, not a figure the noise made up.
  assert.equal(room({ delayFrames: 960, noise: 0.1 }).measure(), null);
});
