import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DEFAULTS, HowlGuard, PRESETS, howlScore, impulse, latencyVerdict, medianMs, normalize, params } from '../static/mic-core.js';

test('normalize clamps and fills in', () => {
  assert.deepEqual(normalize(), { ...DEFAULTS });
  assert.deepEqual(normalize({ on: 1, volume: 999, echo: -3, reverb: 50.4, deviceId: 7 }),
    { on: true, volume: 200, echo: 0, reverb: 50, deviceId: '' });
  assert.equal(normalize({ volume: NaN }).volume, 0);
});

test('params keep the echo from running away', () => {
  for (const echo of [0, 50, 100, 1000]) {
    const p = params({ echo });
    assert.ok(p.echoFeedback < 0.5, `feedback ${p.echoFeedback} at echo ${echo}`);
    assert.ok(p.echoSend <= 0.7);
  }
  assert.equal(params({ volume: 150 }).micGain, 1.5);
  assert.equal(params({ echo: 0 }).echoSend, 0);
});

test('presets are valid settings', () => {
  for (const [name, preset] of Object.entries(PRESETS)) {
    const n = normalize({ ...DEFAULTS, ...preset });
    assert.equal(n.echo, preset.echo, name);
    assert.equal(n.reverb, preset.reverb, name);
  }
});

test('impulse decays and differs per channel', () => {
  let seed = 1;
  const random = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const [l, r] = impulse(1000, 1, random);
  assert.equal(l.length, 1000);
  const energy = (a, from, to) => a.slice(from, to).reduce((s, x) => s + x * x, 0);
  assert.ok(energy(l, 0, 100) > 10 * energy(l, 800, 900), 'the tail is quieter than the start');
  assert.notDeepEqual(Array.from(l.slice(0, 10)), Array.from(r.slice(0, 10)));
});

const toneAt = (bin) => {
  const s = new Float32Array(512).fill(-90);
  s[bin] = -5; s[bin + 1] = -12;
  return s;
};

test('howlScore tells a feedback tone from a voice', () => {
  const voice = new Float32Array(512).map((_, i) => -20 - (i % 37) / 4);
  assert.ok(howlScore(toneAt(200)).score > 0.9);
  assert.equal(howlScore(toneAt(200)).bin, 200);
  assert.ok(howlScore(voice).score < 0.2, `voice ${howlScore(voice).score}`);
  assert.equal(howlScore(new Float32Array(512).fill(-80)).score, 0);
});

test('HowlGuard waits for a loud tone that holds still for a second', () => {
  const run = (frames) => {
    const g = new HowlGuard();
    return frames.findIndex(([spectrum, level], i) => g.update(spectrum, level, i * 100));
  };
  const steady = Array.from({ length: 15 }, () => [toneAt(200), 0.9]);
  assert.equal(run(steady), 10, 'fires once the tone has held for 1 s');
  const quiet = Array.from({ length: 15 }, () => [toneAt(200), 0.2]);
  assert.equal(run(quiet), -1, 'a quiet held note is not feedback');
  // A singer's note moves: a slide or a new note every half second never holds long enough.
  const moving = Array.from({ length: 30 }, (_, i) => [toneAt(150 + Math.floor(i / 5) * 6), 0.9]);
  assert.equal(run(moving), -1, 'a moving pitch is not feedback');
});

test('medianMs uses the tries that heard the click', () => {
  assert.equal(medianMs([0.021, null, 0.019, 0.08]), 21);
  assert.equal(medianMs([0.02, 0.03]), 25);
  assert.equal(medianMs([null, null]), null);
});

test('latencyVerdict', () => {
  assert.match(latencyVerdict(18), /natural/);
  assert.match(latencyVerdict(40), /noticeable/);
  assert.match(latencyVerdict(90), /too slow/);
  assert.match(latencyVerdict(null), /No click/);
});
