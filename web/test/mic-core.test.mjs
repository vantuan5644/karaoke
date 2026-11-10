import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULTS, HowlGuard, PRESETS, SCALES, TUNE_PRESETS, howlScore, voiced, impulse, keyName, latencyVerdict, majorNotes, medianMs,
  normalize, notesFor, params, scaleLabel, shiftNotes, tunePlan,
  commandChanges, commandSaid, engineText, knobText, upgrade,
} from '../static/mic-core.js';

test('normalize clamps and fills in', () => {
  assert.deepEqual(normalize(), { ...DEFAULTS });
  assert.deepEqual(normalize({ on: 1, volume: 999, echo: -3, reverb: 50.4, deviceId: 7, tune: 101, lowLatency: 0 }),
    { ...DEFAULTS, on: true, volume: 200, echo: 0, reverb: 50, deviceId: '', tune: 100, lowLatency: false });
  assert.deepEqual(
    (({ boost, speed, scale, notes }) => ({ boost, speed, scale, notes }))(normalize({ boost: 30, speed: -5, scale: 'lydian', notes: 99999 })),
    { boost: 24, speed: 0, scale: 'chromatic', notes: 4095 });
  assert.equal(normalize({ scale: 'key-9' }).scale, 'key-9');
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

test('params match the Go engine', () => {
  // The same numbers internal/voice's TestParamsMatchTheBrowser checks.
  const p = params({ on: true, volume: 150, echo: 50, reverb: 40, warmth: 50, bright: 25, double: 50, tune: 30, speed: 70, notes: 2741, boost: 12 });
  const near = (a, b) => Math.abs(a - b) < 1e-9;
  assert.ok(near(p.micGain, 1.5) && near(p.echoSend, 0.35) && near(p.echoFeedback, 0.285) && near(p.reverbSend, 0.34));
  assert.ok(near(p.warmthDb, 3) && near(p.brightDb, 2) && near(p.doubleMix, 0.35) && p.tune === 30, JSON.stringify(p));
  assert.ok(near(p.boostGain, 3.981071705534973) && p.speed === 70 && p.notes === 2741, JSON.stringify(p));
  const t = tunePlan(30, 50);
  assert.ok(near(t.amount, 0.3) && near(t.seconds, 0.4 * 0.25 + 0.001), JSON.stringify(t));
  assert.deepEqual(tunePlan(0, 100), { amount: 0, seconds: 0, notes: 0 });
  assert.equal(tunePlan(100, 100).amount, 1);
  assert.ok(near(tunePlan(100, 100).seconds, 0.001));
  for (const [name, preset] of Object.entries(TUNE_PRESETS)) assert.deepEqual(normalize({ ...DEFAULTS, ...preset }).tune, preset.tune, name);
});

test('presets are valid settings', () => {
  for (const [name, preset] of Object.entries(PRESETS)) {
    const n = normalize({ ...DEFAULTS, ...preset });
    assert.equal(n.echo, preset.echo, name);
    assert.equal(n.reverb, preset.reverb, name);
  }
});

test('the notes pitch correction pulls to', () => {
  assert.equal(majorNotes(0), 2741); // C D E F G A B, as the Go tests' cMajor
  assert.equal(majorNotes(2), shiftNotes(2741, 2)); // D major
  assert.equal(shiftNotes(2741, -1), majorNotes(11));
  assert.equal(shiftNotes(2741, 12), 2741);
  assert.equal(shiftNotes(0, 3), 0); // every semitone stays every semitone
  assert.equal(notesFor('chromatic', { notes: 2741 }, 2), 0);
  assert.equal(notesFor('song', null, 2), 0, 'an unknown song key pulls to every semitone');
  assert.equal(notesFor('song', { notes: 2741 }, 2), majorNotes(2), "the song's key follows its key change");
  assert.equal(notesFor('key-7', null, 0), majorNotes(7));
  assert.equal(notesFor('key-7', null, -2), majorNotes(5), 'a key picked by hand follows the key change');
  assert.equal(keyName(9, true), 'A minor');
  assert.equal(keyName(10, false), 'B♭ major');
  assert.equal(scaleLabel('key-0'), 'C major / A minor');
  assert.equal(SCALES.length, 14);
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

test('HowlGuard lets a loud held note be: its octave says it is a voice', () => {
  const run = (spectrum) => {
    const g = new HowlGuard();
    return Array.from({ length: 15 }).findIndex((_, i) => g.update(spectrum, 0.9, i * 100));
  };
  const withPartials = (partials) => {
    const s = new Float32Array(512).fill(-90);
    for (const [bin, db] of partials) s[bin] = db;
    return s;
  };
  // A boosted "ooh" at bin 10: its second harmonic 12 dB down, its third 19.
  const hum = withPartials([[10, -5], [20, -17], [30, -24]]);
  assert.ok(howlScore(hum).score > 0.7, 'one narrow peak, as far as howlScore can tell');
  assert.equal(run(hum), -1, 'a hum is a voice');
  // A vowel whose loudest partial is its second harmonic: the note is an octave below.
  assert.equal(run(withPartials([[20, -5], [10, -15], [40, -22]])), -1, 'a peak with its note an octave down is a voice');
  // Feedback that clips has odd harmonics, hardly a second: still feedback.
  assert.equal(run(withPartials([[40, -5], [120, -15], [200, -20]])), 10, 'clipped feedback');
  // Far from the octave, a weak second harmonic does not save a tone.
  assert.equal(run(withPartials([[40, -5], [80, -40]])), 10, 'a tone with a faint octave');
  assert.equal(voiced(withPartials([[4, -5], [8, -10]]), 4), false, 'below 6 bins the octave sits in the main lobe: not counted');
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

test('knobText shows boost in dB and volume in percent', () => {
  assert.equal(knobText('boost', 0), '0 dB');
  assert.equal(knobText('boost', 12), '+12 dB');
  assert.equal(knobText('volume', 80), '80%');
  assert.equal(knobText('tune', 40), '40');
});

test('engineText says where the voice is processed', () => {
  assert.match(engineText({ native: true, engineMs: 5.4, tune: 0 }), /about 5 ms/);
  assert.match(engineText({ native: true, tune: 50 }), /a few ms \(8 ms of it is Tune\)/);
  assert.match(engineText({ native: false, offered: true }), /Low latency cuts that/);
  assert.equal(engineText({ native: false, tune: 30 }), 'In the browser (8 ms of it is Tune).');
});

test('upgrade keeps the sound of settings saved before Speed', () => {
  // One knob then: 100 pulled all the way in 5 ms, 30 half way and slowly.
  const robot = upgrade({ tune: 100, echo: 40 });
  assert.deepEqual({ ...robot, speed: undefined }, { tune: 100, speed: undefined, echo: 40 });
  assert.ok(Math.abs(tunePlan(robot.tune, robot.speed).seconds - 0.005) < 0.001, 'the same 5 ms glide');
  const old = upgrade({ tune: 30 });
  assert.equal(old.tune, 50);
  const before = 0.3 * 0.7 ** 2 + 0.005;
  assert.ok(Math.abs(tunePlan(old.tune, old.speed).seconds - before) < 0.01, 'the same glide');
  // Settings with a Speed, or with Tune off, are this version's already.
  assert.deepEqual(upgrade({ tune: 30, speed: 80 }), { tune: 30, speed: 80 });
  assert.deepEqual(upgrade({ tune: 0 }), { tune: 0 });
  assert.deepEqual(upgrade(), {});
});

test("phones' mic commands", () => {
  const s = normalize({ tune: 40, speed: 50, boost: 6, scale: 'chromatic' });
  assert.deepEqual(commandChanges('mic-boost', 2, s, true), { boost: 8 });
  assert.deepEqual(commandChanges('mic-speed', -10, s, true), { speed: 40 });
  assert.deepEqual(commandChanges('mic-tune-preset', 3, s, true), TUNE_PRESETS.robot);
  assert.deepEqual(commandChanges('mic-scale', 0, s, true), { scale: 'song' });
  assert.deepEqual(commandChanges('mic-scale', 0, s, false), { scale: 'chromatic' }, "no song's key without ffmpeg");
  assert.deepEqual(commandChanges('mic-scale', 0, { ...s, scale: 'key-7' }, true), { scale: 'chromatic' });
  assert.deepEqual(commandChanges('mic-volume', 10, s, true), { volume: 110, on: true });
  assert.equal(commandChanges('mic-nope', 1, s, true), null);
  assert.equal(commandSaid('mic-boost', 2, { ...s, boost: 8 }), 'Mic boost +8 dB');
  assert.equal(commandSaid('mic-tune-preset', 1, s), 'Auto-tune: natural');
  assert.equal(commandSaid('mic-scale', 0, { ...s, scale: 'song' }), "Auto-tune notes: The song's key");
});
